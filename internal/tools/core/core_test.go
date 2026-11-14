package core_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/core"
)

type staticCred struct{ err error }

func (c staticCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "t", ExpiresOn: time.Now().Add(time.Hour)}, c.err
}

// session starts the core area against a fake Fabric API served by h.
func session(t *testing.T, cred azcore.TokenCredential, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	api := httptest.NewTLSServer(h)
	t.Cleanup(api.Close)
	endpoint := api.URL
	client, err := fabric.NewClient(cred, &endpoint, &fabric.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: api.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	// This suite exercises the core_* tools directly, so it asks for mode
	// "all" explicitly rather than relying on the default (now "namespace",
	// which would collapse them into one "core" proxy tool).
	s, err := server.New(ctx, server.Options{Mode: server.ModeAll, DisableElicitation: true}, core.New(client), core.NewJobs(client))
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// call returns the decoded envelope of a tool call.
func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &env); err != nil {
		t.Fatal(err)
	}
	return env, res.IsError
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestSearchCatalog(t *testing.T) {
	var body map[string]any
	cs := session(t, staticCred{}, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/catalog/search" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value":[{"id":"1","type":"Lakehouse","catalogEntryType":"FabricItem","displayName":"lh",
			"description":"","tags":[{"id":"x"}],"hierarchy":{"workspace":{"id":"w","displayName":"ws"}}}]}`)
	})
	env, isErr := call(t, cs, "core_search-catalog", map[string]any{"search": "lh", "page-size": 5})
	if isErr {
		t.Fatalf("unexpected error: %v", env)
	}
	if got, want := jsonOf(body), `{"pageSize":5,"search":"lh"}`; got != want {
		t.Errorf("request body = %s, want %s", got, want)
	}
	// Fields upstream's model doesn't know (tags) are dropped; "" is kept.
	want := `{"results":{"value":[{"catalogEntryType":"FabricItem","description":"","displayName":"lh",` +
		`"hierarchy":{"workspace":{"displayName":"ws","id":"w"}},"id":"1","type":"Lakehouse"}]}}`
	if got := jsonOf(env["results"]); got != want {
		t.Errorf("results = %s\nwant %s", got, want)
	}
}

func TestSearchCatalogPageSize(t *testing.T) {
	cs := session(t, staticCred{}, func(http.ResponseWriter, *http.Request) { t.Error("API must not be called") })
	env, isErr := call(t, cs, "core_search-catalog", map[string]any{"page-size": 0})
	if got := jsonOf(env); !isErr || got != `{"duration":0,"message":"Page size must be between 1 and 1000.","status":400}` {
		t.Errorf("got %s (isError=%v)", got, isErr)
	}
}

func TestCreateItem(t *testing.T) {
	var body map[string]any
	cs := session(t, staticCred{}, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/workspaces/"+ws1+"/items" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"`+item1+`","displayName":"lh","description":"d","type":"Lakehouse","workspaceId":"`+ws1+`","folderId":"f"}`)
	})
	env, isErr := call(t, cs, "core_create-item", map[string]any{
		"workspace": ws1, "workspace-id": " ", "display-name": "lh", "item-type": "Lakehouse", "description": "d",
		"folder-id": fold1, "creation-payload": `{"enableSchemas": true}`,
		"sensitivity-label-settings": `{"labelId":"` + op1 + `"}`,
	})
	want := `{"item":{"description":"d","displayName":"lh","id":"` + item1 + `","type":"Lakehouse","workspaceId":"` + ws1 + `"}}`
	if got := jsonOf(env["results"]); isErr || got != want || env["status"] != 200.0 {
		t.Errorf("env = %s (isError=%v)\nwant results %s", jsonOf(env), isErr, want)
	}
	if !sameJSON(body, `{"displayName":"lh","type":"Lakehouse","description":"d","folderId":"`+fold1+`","creationPayload":{"enableSchemas":true},"sensitivityLabelSettings":{"labelId":"`+op1+`"}}`) {
		t.Errorf("body = %s", jsonOf(body))
	}
}

func TestCreateItemErrors(t *testing.T) {
	apiError := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"requestId":"r","errorCode":"BadRequest","message":"bad","isRetriable":false}`)
	}
	args := map[string]any{"workspace-id": ws1, "display-name": "x", "item-type": "Lakehouse"}
	tests := []struct {
		name    string
		cred    staticCred
		args    map[string]any
		status  int
		message string
	}{
		{"missing workspace", staticCred{}, map[string]any{"display-name": "x", "item-type": "Lakehouse"}, 400,
			"Provide a nonempty workspace UUID using --workspace-id or --workspace; workspace names are not supported."},
		{"api error", staticCred{}, args, 400, "Fabric rejected the item creation request. Check the workspace UUID, item type, display name, and description; " +
			"also check any definition, creation-payload, folder-id, options, or sensitivity-label-settings" + help},
		{"not signed in", staticCred{err: &auth.CredentialError{Err: errors.New("no credential"), Unavailable: true}}, args, 401,
			"Fabric credentials are unavailable. Authenticate the configured Fabric identity before creating an item" + help},
		{"payload and definition", staticCred{}, map[string]any{"workspace-id": ws1, "display-name": "x", "item-type": "Notebook",
			"creation-payload": "{}", "definition": "{}"}, 400,
			"--creation-payload cannot be combined with any inline or saved-file definition option." + help},
		{"bad label strategy", staticCred{}, map[string]any{"workspace-id": ws1, "display-name": "x", "item-type": "Notebook",
			"sensitivity-label-settings": `{"labelId":"` + op1 + `","sensitivityLabelApplyStrategy":"Downgrade"}`}, 400,
			"A supplied sensitivityLabelApplyStrategy must be ApplyOrFail or Ignore. Omit it to retain Fabric's ApplyOrFail default." + help},
		{"bad definition", staticCred{}, map[string]any{"workspace-id": ws1, "display-name": "x", "item-type": "Notebook",
			"definition": `{"parts":[{"path":"../a","payload":"","payloadType":"InlineBase64"}]}`}, 400,
			"Definition paths must be portable relative file paths with '/' separators and no traversal. (Parameter 'path')" + help},
		{"unknown definition field", staticCred{}, map[string]any{"workspace-id": ws1, "display-name": "x", "item-type": "Notebook",
			"definition": `{"parts":[],"extra":1}`}, 400,
			"--definition must be a JSON object with parts and optional format, not a file path or a request envelope. Required fields must be present; unknown and duplicate fields are rejected." + help},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, isErr := call(t, session(t, tt.cred, apiError), "core_create-item", tt.args)
			if !isErr {
				t.Error("isError = false")
			}
			expect(t, env, tt.status, tt.message)
			if _, ok := env["results"]; ok {
				t.Errorf("results = %s", jsonOf(env["results"]))
			}
		})
	}
}
