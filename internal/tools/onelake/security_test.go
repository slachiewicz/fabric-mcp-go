package onelake

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

func TestListDataAccessRoles(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fabric/workspaces/f295f70e-fc97-4cf7-89c2-a0c75005ef3b/items/0049cf83-d5a9-40f0-9dc4-598a1386945d/dataAccessRoles":
			_, _ = io.WriteString(w, `{"value":[{"name":"DefaultReader","decisionRules":[{"effect":"Permit","permission":[{"attributeName":"Action","attributeValueIncludedIn":["Read"]}]}],"members":{"fabricItemMembers":[{"sourcePath":"ws/item","itemAccess":["ReadAll"]}]}}],"continuationToken":"tok"}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-data-access-roles", map[string]any{
		"workspace-id": "f295f70e-fc97-4cf7-89c2-a0c75005ef3b", "item-id": "0049cf83-d5a9-40f0-9dc4-598a1386945d",
	})
	if isErr {
		t.Fatalf("list: %s", jsonOf(env))
	}
	results, _ := env["results"].(map[string]any)
	roles, _ := results["roles"].([]any)
	if len(roles) != 1 {
		t.Fatalf("roles = %v", roles)
	}
	if results["continuationToken"] != "tok" {
		t.Errorf("continuationToken = %v", results["continuationToken"])
	}

	env, isErr = call(t, cs, "onelake_list-data-access-roles", map[string]any{"item-id": "x"})
	if !isErr || env["message"] != errWorkspaceRequired {
		t.Errorf("missing workspace: %s", jsonOf(env))
	}
	env, isErr = call(t, cs, "onelake_list-data-access-roles", map[string]any{"workspace": "not-a-guid", "item-id": "x"})
	if !isErr || env["message"] != "Workspace must be a valid GUID. Name-based resolution is not supported for this command." {
		t.Errorf("bad guid: %s", jsonOf(env))
	}
}

func TestGetDataAccessRole(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/fabric/workspaces/11111111-1111-1111-1111-111111111111/items/22222222-2222-2222-2222-222222222222/dataAccessRoles/DefaultReader":
			if r.URL.Query().Get("preview") != "true" {
				t.Errorf("missing preview=true: %s", r.URL)
			}
			_, _ = io.WriteString(w, `{"name":"DefaultReader","kind":"Policy"}`)
		case "/fabric/workspaces/11111111-1111-1111-1111-111111111111/items/22222222-2222-2222-2222-222222222222/dataAccessRoles/Missing":
			http.Error(w, `{"errorCode":"EntityNotFound"}`, http.StatusNotFound)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_get-data-access-role", map[string]any{
		"workspace-id": "11111111-1111-1111-1111-111111111111", "item-id": "22222222-2222-2222-2222-222222222222", "role-name": "DefaultReader",
	})
	if isErr {
		t.Fatalf("get: %s", jsonOf(env))
	}
	role, _ := env["results"].(map[string]any)["role"].(map[string]any)
	if role["name"] != "DefaultReader" {
		t.Errorf("role = %v", role)
	}

	env, isErr = call(t, cs, "onelake_get-data-access-role", map[string]any{
		"workspace-id": "11111111-1111-1111-1111-111111111111", "item-id": "22222222-2222-2222-2222-222222222222", "role-name": "Missing",
	})
	if !isErr || env["status"] != 404.0 {
		t.Errorf("not found: %s", jsonOf(env))
	}
}

func TestDeleteDataAccessRole(t *testing.T) {
	var method, query string
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		method, query = r.Method, r.URL.RawQuery
		w.WriteHeader(http.StatusNoContent)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_delete-data-access-role", map[string]any{
		"workspace-id": "11111111-1111-1111-1111-111111111111", "item-id": "22222222-2222-2222-2222-222222222222", "role-name": "MyRole",
	})
	if isErr {
		t.Fatalf("delete: %s", jsonOf(env))
	}
	if method != http.MethodDelete {
		t.Errorf("method = %s", method)
	}
	if query != "preview=true" {
		t.Errorf("query = %q", query)
	}
	results, _ := env["results"].(map[string]any)
	if results["roleName"] != "MyRole" || results["message"] != "Data access role deleted successfully." {
		t.Errorf("results = %v", results)
	}

	env, isErr = call(t, cs, "onelake_delete-data-access-role", map[string]any{"item-id": "x", "role-name": "y"})
	if !isErr || env["message"] != errWorkspaceRequired {
		t.Errorf("missing workspace: %s", jsonOf(env))
	}
}

func TestCreateOrUpdateDataAccessRoleFlatOptions(t *testing.T) {
	var graphCalls int
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/graph/users/"):
			graphCalls++
			_, _ = io.WriteString(w, `{"id":"66666666-6666-6666-6666-666666666666"}`)
		case r.URL.Path == "/fabric/workspaces/11111111-1111-1111-1111-111111111111/items/22222222-2222-2222-2222-222222222222/dataAccessRoles":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			if got := r.URL.Query().Get("dataAccessRoleConflictPolicy"); got != "Overwrite" {
				t.Errorf("conflict policy = %q", got)
			}
			b, _ := io.ReadAll(r.Body)
			var role dataAccessRole
			if err := json.Unmarshal(b, &role); err != nil {
				t.Fatal(err)
			}
			if role.Name != "MyRole" {
				t.Errorf("posted role name = %q", role.Name)
			}
			if role.Members == nil || len(role.Members.MicrosoftEntraMembers) != 1 ||
				*role.Members.MicrosoftEntraMembers[0].ObjectID != "66666666-6666-6666-6666-666666666666" {
				t.Errorf("posted members = %+v", role.Members)
			}
			if len(role.DecisionRules) != 1 || len(role.DecisionRules[0].Permission) != 2 {
				t.Errorf("posted decision rules = %+v", role.DecisionRules)
			}
			_, _ = w.Write(b)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	restoreGraph := useFakeGraph(a)
	defer restoreGraph()

	cs := session(t, a)
	env, isErr := call(t, cs, "onelake_create-or-update-data-access-role", map[string]any{
		"workspace-id": "11111111-1111-1111-1111-111111111111", "item-id": "22222222-2222-2222-2222-222222222222",
		"role-name": "MyRole", "entra-members": "alice@contoso.com", "permitted-paths": "Files/images/*",
	})
	if isErr {
		t.Fatalf("create-or-update: %s", jsonOf(env))
	}
	if graphCalls != 1 {
		t.Errorf("graph calls = %d, want 1", graphCalls)
	}
	role, _ := env["results"].(map[string]any)["role"].(map[string]any)
	if role["name"] != "MyRole" {
		t.Errorf("role = %v", role)
	}
}

func TestCreateOrUpdateDataAccessRoleRoleDefinition(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_, _ = w.Write(b)
	})
	cs := session(t, a)

	def := `{"name":"Custom","members":{"microsoftEntraMembers":[{"objectId":"11111111-1111-1111-1111-111111111111"}]},"decisionRules":[{"effect":"Permit","permission":[{"attributeName":"Action","attributeValueIncludedIn":["Read"]}]}]}`
	env, isErr := call(t, cs, "onelake_create-or-update-data-access-role", map[string]any{
		"workspace-id": "22222222-2222-2222-2222-222222222222", "item-id": "33333333-3333-3333-3333-333333333333",
		"role-definition": def,
	})
	if isErr {
		t.Fatalf("create-or-update: %s", jsonOf(env))
	}
	role, _ := env["results"].(map[string]any)["role"].(map[string]any)
	if role["name"] != "Custom" {
		t.Errorf("role = %v", role)
	}
}

func TestCreateOrUpdateDataAccessRoleValidation(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	cs := session(t, a)

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"neither flat nor json", map[string]any{"workspace-id": "11111111-1111-1111-1111-111111111111", "item-id": "x"},
			"Provide either flat options (--role-name + --entra-members/--fabric-item-members) or --role-definition."},
		{"flat without members", map[string]any{"workspace-id": "11111111-1111-1111-1111-111111111111", "item-id": "x", "role-name": "R"},
			"At least one of --entra-members or --fabric-item-members is required."},
		{"bad entra member", map[string]any{"workspace-id": "11111111-1111-1111-1111-111111111111", "item-id": "x", "role-name": "R", "entra-members": "not-a-guid-no-at"},
			"Invalid --entra-members value 'not-a-guid-no-at'. Must be a GUID, email, or UPN."},
		{"bad permitted action", map[string]any{"workspace-id": "11111111-1111-1111-1111-111111111111", "item-id": "x", "role-name": "R", "entra-members": "11111111-1111-1111-1111-111111111111", "permitted-actions": "Write"},
			"Unsupported --permitted-actions value 'Write'. Only 'Read' is currently supported."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, isErr := call(t, cs, "onelake_create-or-update-data-access-role", tt.args)
			if !isErr || env["message"] != tt.want {
				t.Errorf("got %s, want message %q", jsonOf(env), tt.want)
			}
		})
	}
}

// jwtCred issues a token whose payload carries a "tid" claim, for testing
// tenantIDFromToken without a live token.
type jwtCred struct{ tid string }

func (c jwtCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"tid":"` + c.tid + `"}`))
	return azcore.AccessToken{Token: "h." + payload + ".s", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestTenantIDFromToken(t *testing.T) {
	c := &client{cred: jwtCred{tid: "33333333-3333-3333-3333-333333333333"}}
	if got := c.tenantIDFromToken(context.Background()); got != "33333333-3333-3333-3333-333333333333" {
		t.Errorf("tenantIDFromToken = %q", got)
	}

	c2 := &client{cred: staticCred{}}
	if got := c2.tenantIDFromToken(context.Background()); got != "" {
		t.Errorf("malformed token: got %q, want empty", got)
	}
}

func TestResolvePrincipals(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/graph/users/"):
			if strings.Contains(r.URL.Path, "alice") {
				_, _ = io.WriteString(w, `{"id":"44444444-4444-4444-4444-444444444444"}`)
				return
			}
			http.Error(w, "not found", http.StatusNotFound)
		case r.URL.Path == "/graph/groups":
			if strings.Contains(r.URL.Query().Get("$filter"), "readers@contoso.com") {
				_, _ = io.WriteString(w, `{"value":[{"id":"55555555-5555-5555-5555-555555555555","displayName":"readers"}]}`)
				return
			}
			_, _ = io.WriteString(w, `{"value":[]}`)
		default:
			t.Errorf("unexpected request %s", r.URL)
		}
	})
	restoreGraph := useFakeGraph(a)
	defer restoreGraph()

	members := []microsoftEntraMember{
		{ObjectID: strPtr("alice@contoso.com")},
		{ObjectID: strPtr("readers@contoso.com")},
		{ObjectID: strPtr("11111111-1111-1111-1111-111111111111")},
	}
	if err := a.resolvePrincipals(context.Background(), members); err != nil {
		t.Fatalf("resolvePrincipals: %v", err)
	}
	if *members[0].ObjectID != "44444444-4444-4444-4444-444444444444" || *members[0].ObjectType != "User" {
		t.Errorf("user resolution: id=%v type=%v", *members[0].ObjectID, members[0].ObjectType)
	}
	if *members[1].ObjectID != "55555555-5555-5555-5555-555555555555" || *members[1].ObjectType != "Group" {
		t.Errorf("group resolution: id=%v type=%v", *members[1].ObjectID, members[1].ObjectType)
	}
	if *members[2].ObjectID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("guid left untouched: %v", *members[2].ObjectID)
	}

	unmatched := []microsoftEntraMember{{ObjectID: strPtr("nobody@contoso.com")}}
	err := a.resolvePrincipals(context.Background(), unmatched)
	if err == nil || !strings.Contains(err.Error(), "No Entra principal matched 'nobody@contoso.com'") {
		t.Errorf("no match: err = %v", err)
	}
}

// useFakeGraph points graphBaseURL at a's fake server under "/graph" for the
// duration of a test; call the returned func to restore it.
func useFakeGraph(a *Area) func() {
	orig := graphBaseURL
	graphBaseURL = strings.TrimSuffix(a.c.ep.fabric, "/fabric") + "/graph"
	return func() { graphBaseURL = orig }
}
