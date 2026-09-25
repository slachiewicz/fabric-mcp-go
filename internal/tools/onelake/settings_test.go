package onelake

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"
)

// decodeBody reads and JSON-decodes a request body into v.
func decodeBody(r *http.Request, v any) error {
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func TestGetSettings(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fabric/workspaces/11111111-1111-1111-1111-111111111111/onelake/settings" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		_, _ = io.WriteString(w, `{"diagnostics":{"status":"Disabled"},"immutabilityPolicies":null,"lifecycle":{"defaultTier":"Hot","policy":"Inactive"}}`)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_get-settings", map[string]any{"workspace-id": "11111111-1111-1111-1111-111111111111"})
	if isErr {
		t.Fatalf("get-settings: %s", jsonOf(env))
	}
	settings, _ := env["results"].(map[string]any)["settings"].(map[string]any)
	diag, _ := settings["diagnostics"].(map[string]any)
	if diag["status"] != "Disabled" {
		t.Errorf("diagnostics = %v", diag)
	}
	if _, ok := settings["immutabilityPolicies"]; !ok {
		t.Errorf("immutabilityPolicies key missing (should serialize as null): %v", settings)
	}

	env, isErr = call(t, cs, "onelake_get-settings", map[string]any{})
	if !isErr || env["message"] != errWorkspaceRequired {
		t.Errorf("missing workspace: %s", jsonOf(env))
	}
	env, isErr = call(t, cs, "onelake_get-settings", map[string]any{"workspace": "not-a-guid"})
	if !isErr || env["message"] != "Workspace must be a valid GUID. Name-based resolution is not supported for this command." {
		t.Errorf("bad guid: %s", jsonOf(env))
	}
}

// TestModifyDiagnosticsLRO exercises client.fabric's 202+Location polling
// path (ported from PollFabricLroAsync) through the modify-diagnostics tool.
func TestModifyDiagnosticsLRO(t *testing.T) {
	var polls int
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		origin := "https://" + r.Host
		switch r.URL.Path {
		case "/fabric/workspaces/11111111-1111-1111-1111-111111111111/onelake/settings/modifyDiagnostics":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			var body oneLakeDiagnosticSettings
			if err := decodeBody(r, &body); err != nil {
				t.Fatal(err)
			}
			if body.Status == nil || *body.Status != "Enabled" || body.Destination == nil ||
				body.Destination.Lakehouse == nil || body.Destination.Lakehouse.ItemID == nil ||
				*body.Destination.Lakehouse.ItemID != "22222222-2222-2222-2222-222222222222" {
				t.Errorf("posted diagnostics settings = %+v", body)
			}
			w.Header().Set("Location", origin+"/fabric/operations/op1")
			w.WriteHeader(http.StatusAccepted)
		case "/fabric/operations/op1":
			polls++
			if polls < 2 {
				_, _ = io.WriteString(w, `{"status":"Running"}`)
				return
			}
			w.Header().Set("Location", origin+"/fabric/operations/op1/result")
			_, _ = io.WriteString(w, `{"status":"Succeeded"}`)
		case "/fabric/operations/op1/result":
			w.WriteHeader(http.StatusOK)
		default:
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_modify-diagnostics", map[string]any{
		"workspace-id": "11111111-1111-1111-1111-111111111111", "status": "Enabled",
		"destination-lakehouse-workspace-id": "11111111-1111-1111-1111-111111111111",
		"destination-lakehouse-item-id":      "22222222-2222-2222-2222-222222222222",
	})
	if isErr {
		t.Fatalf("modify-diagnostics: %s", jsonOf(env))
	}
	if polls < 2 {
		t.Errorf("polls = %d, want at least 2 (proves the 202/Location loop ran)", polls)
	}
	if env["results"].(map[string]any)["message"] != "Diagnostics settings modified successfully." {
		t.Errorf("results = %v", env["results"])
	}
}

func TestModifyDiagnosticsValidation(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request %s", r.URL)
	})
	cs := session(t, a)

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"bad status", map[string]any{"workspace-id": "11111111-1111-1111-1111-111111111111", "status": "Bogus"},
			"--status must be 'Enabled' or 'Disabled'."},
		{"enabled missing destination", map[string]any{"workspace-id": "11111111-1111-1111-1111-111111111111", "status": "Enabled"},
			"--destination-lakehouse-workspace-id is required when --status is Enabled.\n--destination-lakehouse-item-id is required when --status is Enabled."},
		{"disabled with destination", map[string]any{"workspace-id": "11111111-1111-1111-1111-111111111111", "status": "Disabled", "destination-lakehouse-item-id": "22222222-2222-2222-2222-222222222222"},
			"Destination options must be omitted when --status is Disabled."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, isErr := call(t, cs, "onelake_modify-diagnostics", tt.args)
			if !isErr || env["message"] != tt.want {
				t.Errorf("got %s, want message %q", jsonOf(env), tt.want)
			}
		})
	}
}

func TestModifyImmutabilityPolicy(t *testing.T) {
	var posted immutabilityPolicy
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/fabric/workspaces/11111111-1111-1111-1111-111111111111/onelake/settings/modifyImmutabilityPolicy" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if err := decodeBody(r, &posted); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusOK)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_modify-immutability-policy", map[string]any{
		"workspace-id": "11111111-1111-1111-1111-111111111111", "scope": "DiagnosticLogs", "retention-days": 30,
	})
	if isErr {
		t.Fatalf("modify-immutability-policy: %s", jsonOf(env))
	}
	if posted.Scope == nil || *posted.Scope != "DiagnosticLogs" || posted.RetentionDays == nil || *posted.RetentionDays != 30 {
		t.Errorf("posted policy = %+v", posted)
	}
	if env["results"].(map[string]any)["message"] != "Immutability policy modified successfully." {
		t.Errorf("results = %v", env["results"])
	}

	env, isErr = call(t, cs, "onelake_modify-immutability-policy", map[string]any{
		"workspace-id": "11111111-1111-1111-1111-111111111111", "scope": "Bogus", "retention-days": 5,
	})
	if !isErr || env["message"] != "--scope must be 'DiagnosticLogs'. No other scopes are currently supported." {
		t.Errorf("bad scope: %s", jsonOf(env))
	}
	env, isErr = call(t, cs, "onelake_modify-immutability-policy", map[string]any{
		"workspace-id": "11111111-1111-1111-1111-111111111111", "scope": "DiagnosticLogs", "retention-days": 0,
	})
	if !isErr || env["message"] != "--retention-days must be at least 1." {
		t.Errorf("bad retention: %s", jsonOf(env))
	}
}
