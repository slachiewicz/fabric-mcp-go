package onelake

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const shortcutsWS = "11111111-1111-1111-1111-111111111111"
const shortcutsItem = "22222222-2222-2222-2222-222222222222"

func TestListShortcuts(t *testing.T) {
	body := `{"value":[
		{"path":"Tables/dbo.T1","name":"managed","target":{"type":"OneLake","oneLake":{"workspaceId":"w","itemId":"i","path":"p"}}},
		{"path":"Files/raw","name":"user-made","target":{"type":"AdlsGen2","adlsGen2":{"location":"https://a","subpath":"s","connectionId":"c"}}}
	],"continuationToken":null,"continuationUri":null}`
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		if got, want := r.URL.Path, "/fabric/workspaces/"+shortcutsWS+"/items/"+shortcutsItem+"/shortcuts"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
		_, _ = io.WriteString(w, body)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-shortcuts", map[string]any{"workspace-id": shortcutsWS, "item-id": shortcutsItem})
	if isErr {
		t.Fatalf("default call: %s", jsonOf(env))
	}
	results := env["results"].(map[string]any)
	shortcuts := results["shortcuts"].([]any)
	if len(shortcuts) != 1 {
		t.Fatalf("default call: got %d shortcut(s), want 1 (managed filtered out): %s", len(shortcuts), jsonOf(env))
	}
	if name := shortcuts[0].(map[string]any)["name"]; name != "user-made" {
		t.Errorf("default call: kept shortcut %q, want %q", name, "user-made")
	}

	env, isErr = call(t, cs, "onelake_list-shortcuts", map[string]any{"workspace-id": shortcutsWS, "item-id": shortcutsItem, "include-managed": true})
	if isErr {
		t.Fatalf("include-managed call: %s", jsonOf(env))
	}
	shortcuts = env["results"].(map[string]any)["shortcuts"].([]any)
	if len(shortcuts) != 2 {
		t.Errorf("include-managed call: got %d shortcut(s), want 2: %s", len(shortcuts), jsonOf(env))
	}
}

func TestGetShortcut(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		// EscapedPath (the wire form) must keep the shortcut path's slash
		// percent-encoded as %2F, matching Uri.EscapeDataString; a raw "/"
		// there would split "Tables/T1" into two path segments.
		if got, want := r.URL.EscapedPath(), "/fabric/workspaces/"+shortcutsWS+"/items/"+shortcutsItem+"/shortcuts/Tables%2FT1/my%20shortcut"; got != want {
			t.Errorf("escaped path = %q, want %q", got, want)
		}
		_, _ = io.WriteString(w, `{"path":"Tables/T1","name":"my shortcut","target":{"type":"OneLake","oneLake":{"workspaceId":"w","itemId":"i","path":"p"}}}`)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_get-shortcut", map[string]any{
		"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Tables/T1", "shortcut-name": "my shortcut",
	})
	if isErr {
		t.Fatalf("get-shortcut: %s", jsonOf(env))
	}
	sc := env["results"].(map[string]any)["shortcut"].(map[string]any)
	if sc["name"] != "my shortcut" {
		t.Errorf("shortcut name = %v, want %q", sc["name"], "my shortcut")
	}
	target := sc["target"].(map[string]any)
	for _, k := range []string{"adlsGen2", "amazonS3", "googleCloudStorage", "dataverse", "s3Compatible", "externalDataShare", "azureBlobStorage", "oneDriveSharePoint"} {
		if v, ok := target[k]; !ok || v != nil {
			t.Errorf("target[%q] = %v, want explicit null", k, v)
		}
	}
}

func TestDeleteShortcut(t *testing.T) {
	var gotMethod string
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		if got, want := r.URL.Path, "/fabric/workspaces/"+shortcutsWS+"/items/"+shortcutsItem+"/shortcuts/Files/x"; got != want {
			t.Errorf("path = %q, want %q", got, want)
		}
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_delete-shortcut", map[string]any{
		"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "x",
	})
	if isErr {
		t.Fatalf("delete-shortcut: %s", jsonOf(env))
	}
	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	results := env["results"].(map[string]any)
	if results["shortcutPath"] != "Files" || results["shortcutName"] != "x" || results["message"] != "Shortcut deleted successfully." {
		t.Errorf("results = %s", jsonOf(results))
	}
}

func TestResetShortcutCache(t *testing.T) {
	var gotMethod, gotPath string
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_reset-shortcut-cache", map[string]any{"workspace-id": shortcutsWS})
	if isErr {
		t.Fatalf("reset-shortcut-cache: %s", jsonOf(env))
	}
	if gotMethod != http.MethodPost || gotPath != "/fabric/workspaces/"+shortcutsWS+"/onelake/resetShortcutCache" {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	if env["results"].(map[string]any)["message"] != "Shortcut cache reset successfully." {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
}

// TestCreateShortcuts covers all eight create-shortcut-* tools against a
// fake Fabric API, standing in for the external connections (ADLS, S3,
// Blob, GCS, S3-compatible, Dataverse, OneDrive/SharePoint) that don't
// exist in the test tenant: it asserts the exact request URL/body each
// command builds and that the shortcutTarget's other eight fields come
// through as explicit JSON nulls, the way OneLake's JSON context writes it.
func TestCreateShortcuts(t *testing.T) {
	tests := []struct {
		tool       string
		args       map[string]any
		wantQuery  string // "" when no shortcutConflictPolicy is sent
		wantTarget string // the one non-null key expected under "target"
		checkBody  func(t *testing.T, target map[string]any)
	}{
		{
			tool: "onelake_create-shortcut-onelake",
			args: map[string]any{
				"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "n",
				"target-workspace-id": "tw", "target-item-id": "ti", "target-path": "tp",
			},
			wantTarget: "oneLake",
			checkBody: func(t *testing.T, target map[string]any) {
				got := target["oneLake"].(map[string]any)
				if got["workspaceId"] != "tw" || got["itemId"] != "ti" || got["path"] != "tp" || got["connectionId"] != nil {
					t.Errorf("oneLake target = %v", got)
				}
			},
		},
		{
			tool: "onelake_create-shortcut-adls-gen2",
			args: map[string]any{
				"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "n",
				"shortcut-conflict-policy": "GenerateUniqueName",
				"target-location":          "https://a", "target-subpath": "s", "target-connection-id": "c",
			},
			wantQuery:  "shortcutConflictPolicy=GenerateUniqueName",
			wantTarget: "adlsGen2",
			checkBody: func(t *testing.T, target map[string]any) {
				got := target["adlsGen2"].(map[string]any)
				if got["location"] != "https://a" || got["subpath"] != "s" || got["connectionId"] != "c" {
					t.Errorf("adlsGen2 target = %v", got)
				}
			},
		},
		{
			tool: "onelake_create-shortcut-amazon-s3",
			args: map[string]any{
				"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "n",
				"target-location": "https://a", "target-connection-id": "c",
			},
			wantTarget: "amazonS3",
			checkBody: func(t *testing.T, target map[string]any) {
				got := target["amazonS3"].(map[string]any)
				if got["location"] != "https://a" || got["subpath"] != nil || got["connectionId"] != "c" {
					t.Errorf("amazonS3 target = %v", got)
				}
			},
		},
		{
			tool: "onelake_create-shortcut-azure-blob",
			args: map[string]any{
				"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "n",
				"target-location": "https://a", "target-subpath": "s", "target-connection-id": "c",
			},
			wantTarget: "azureBlobStorage",
			checkBody: func(t *testing.T, target map[string]any) {
				got := target["azureBlobStorage"].(map[string]any)
				if got["location"] != "https://a" || got["subpath"] != "s" || got["connectionId"] != "c" {
					t.Errorf("azureBlobStorage target = %v", got)
				}
			},
		},
		{
			tool: "onelake_create-shortcut-gcs",
			args: map[string]any{
				"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "n",
				"target-location": "https://a", "target-connection-id": "c",
			},
			wantTarget: "googleCloudStorage",
			checkBody: func(t *testing.T, target map[string]any) {
				got := target["googleCloudStorage"].(map[string]any)
				if got["location"] != "https://a" || got["connectionId"] != "c" {
					t.Errorf("googleCloudStorage target = %v", got)
				}
			},
		},
		{
			tool: "onelake_create-shortcut-s3-compatible",
			args: map[string]any{
				"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "n",
				"target-location": "https://a", "target-connection-id": "c", "target-bucket": "b",
			},
			wantTarget: "s3Compatible",
			checkBody: func(t *testing.T, target map[string]any) {
				got := target["s3Compatible"].(map[string]any)
				if got["location"] != "https://a" || got["connectionId"] != "c" || got["bucket"] != "b" {
					t.Errorf("s3Compatible target = %v", got)
				}
			},
		},
		{
			tool: "onelake_create-shortcut-dataverse",
			args: map[string]any{
				"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "n",
				"target-environment-domain": "https://org.crm.dynamics.com", "target-connection-id": "c",
				"target-deltalake-folder": "f", "target-table-name": "ignored-by-upstream-too",
			},
			wantTarget: "dataverse",
			checkBody: func(t *testing.T, target map[string]any) {
				got := target["dataverse"].(map[string]any)
				if got["environmentDomain"] != "https://org.crm.dynamics.com" || got["connectionId"] != "c" || got["deltaLakeFolder"] != "f" {
					t.Errorf("dataverse target = %v", got)
				}
				if len(got) != 3 {
					t.Errorf("dataverse target has %d fields, want 3 (table-name is accepted but never sent): %v", len(got), got)
				}
			},
		},
		{
			tool: "onelake_create-shortcut-onedrive-sharepoint",
			args: map[string]any{
				"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "Files", "shortcut-name": "n",
				"target-location": "https://a", "target-connection-id": "c",
			},
			wantTarget: "oneDriveSharePoint",
			checkBody: func(t *testing.T, target map[string]any) {
				got := target["oneDriveSharePoint"].(map[string]any)
				if got["location"] != "https://a" || got["connectionId"] != "c" || got["updateFabricItemSensitivity"] != nil {
					t.Errorf("oneDriveSharePoint target (sensitivity unset) = %v", got)
				}
			},
		},
	}

	targetKeys := []string{"oneLake", "adlsGen2", "amazonS3", "googleCloudStorage", "dataverse", "s3Compatible", "externalDataShare", "azureBlobStorage", "oneDriveSharePoint"}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			var gotMethod, gotPath, gotQuery string
			var gotBody []byte
			a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
				gotMethod, gotPath, gotQuery = r.Method, r.URL.Path, r.URL.RawQuery
				gotBody, _ = io.ReadAll(r.Body)
				w.WriteHeader(http.StatusNoContent) // empty body: the command echoes back what it sent.
			})
			cs := session(t, a)

			env, isErr := call(t, cs, tt.tool, tt.args)
			if isErr {
				t.Fatalf("%s: %s", tt.tool, jsonOf(env))
			}
			if gotMethod != http.MethodPost {
				t.Errorf("method = %q, want POST", gotMethod)
			}
			if want := "/fabric/workspaces/" + shortcutsWS + "/items/" + shortcutsItem + "/shortcuts"; gotPath != want {
				t.Errorf("path = %q, want %q", gotPath, want)
			}
			if gotQuery != tt.wantQuery {
				t.Errorf("query = %q, want %q", gotQuery, tt.wantQuery)
			}

			var sentBody map[string]any
			if err := json.Unmarshal(gotBody, &sentBody); err != nil {
				t.Fatalf("request body not JSON: %v (%s)", err, gotBody)
			}
			if _, ok := sentBody["type"]; ok {
				t.Errorf("outgoing target carries a %q key; upstream marks it JsonIgnore(WhenWritingNull)", "type")
			}
			target := sentBody["target"].(map[string]any)
			for _, k := range targetKeys {
				v, ok := target[k]
				if !ok {
					t.Errorf("target missing key %q", k)
					continue
				}
				if k == tt.wantTarget {
					continue
				}
				if v != nil {
					t.Errorf("target[%q] = %v, want explicit null (only %q should be populated)", k, v, tt.wantTarget)
				}
			}
			tt.checkBody(t, target)

			// The fake answered with an empty (204) body, so the command
			// must echo back exactly what it sent, wrapped in "shortcut".
			results := env["results"].(map[string]any)
			echoed := results["shortcut"].(map[string]any)
			if echoed["path"] != "Files" || echoed["name"] != "n" {
				t.Errorf("echoed shortcut = %v", echoed)
			}
		})
	}
}

// TestShortcutErrors checks the shared error paths (missing item, and a
// malformed response body) against the OneLakeCommandValidators-free
// mapping every shortcut command uses (response.Error, not errorResult).
func TestShortcutErrors(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"errorCode":"ItemNotFound","message":"nope"}`, http.StatusNotFound)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-shortcuts", map[string]any{"workspace-id": shortcutsWS, "item-id": shortcutsItem})
	if !isErr || env["status"] != 404.0 {
		t.Errorf("not found: isErr=%v env=%s", isErr, jsonOf(env))
	}
	if typ := env["results"].(map[string]any)["type"]; typ != "HttpRequestException" {
		t.Errorf("results.type = %v, want HttpRequestException", typ)
	}

	a = fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `not json`)
	})
	cs = session(t, a)
	env, isErr = call(t, cs, "onelake_get-shortcut", map[string]any{
		"workspace-id": shortcutsWS, "item-id": shortcutsItem, "shortcut-path": "p", "shortcut-name": "n",
	})
	if !isErr || !strings.Contains(env["message"].(string), "Failed to parse shortcut response") {
		t.Errorf("malformed body: isErr=%v env=%s", isErr, jsonOf(env))
	}
}
