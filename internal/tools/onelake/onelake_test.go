package onelake

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

type staticCred struct{}

func (staticCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "t", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// fakeArea returns an Area whose every endpoint is served by h. Endpoints
// get distinct path prefixes: /fabric, /api, /dfs, /blob and /table.
func fakeArea(t *testing.T, h http.HandlerFunc) *Area {
	t.Helper()
	srv := httptest.NewTLSServer(h)
	t.Cleanup(srv.Close)
	a := New(staticCred{})
	a.c.http = srv.Client()
	a.c.lroDelay = time.Millisecond
	a.c.ep = endpoints{
		fabric: srv.URL + "/fabric", api: srv.URL + "/api", dfs: srv.URL + "/dfs",
		blob: srv.URL + "/blob", table: srv.URL + "/table",
	}
	return a
}

// session connects an in-memory MCP client to a server exposing a.
func session(t *testing.T, a *Area) *mcp.ClientSession {
	t.Helper()
	s, err := server.New(context.Background(), server.Options{Mode: server.ModeAll, DisableElicitation: true}, a)
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	ctx := context.Background()
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

// call returns the decoded envelope of a tool call and whether it is an error.
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

const itemsXML = `<?xml version="1.0" encoding="utf-8"?><EnumerationResults><Blobs>
<BlobPrefix><Name>sales.Lakehouse/</Name><Metadata><ArtifactId>11111111-1111-1111-1111-111111111111</ArtifactId></Metadata></BlobPrefix>
</Blobs></EnumerationResults>`

func TestResolveItem(t *testing.T) {
	var listed int
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		listed++
		if !strings.HasPrefix(r.URL.Path, "/api/ws") || r.URL.Query().Get("comp") != "list" {
			t.Errorf("unexpected request %s", r.URL)
		}
		_, _ = io.WriteString(w, itemsXML)
	})
	tests := []struct{ in, want string }{
		{"sales.Lakehouse/", "sales.Lakehouse"},
		{"22222222-2222-2222-2222-222222222222", "22222222-2222-2222-2222-222222222222"},
		{"SALES", "sales.Lakehouse"},
		{"11111111111111111111111111111111", "11111111111111111111111111111111"},
	}
	for _, tt := range tests {
		got, err := a.resolveItem(context.Background(), " ws ", tt.in)
		if err != nil || got != tt.want {
			t.Errorf("resolveItem(%q) = %q, %v; want %q", tt.in, got, err, tt.want)
		}
	}
	if _, err := a.resolveItem(context.Background(), "ws", "missing"); err == nil || !strings.Contains(err.Error(), "Unable to resolve item 'missing'") {
		t.Errorf("unknown item: err = %v", err)
	}
	if listed != 2 {
		t.Errorf("listed %d times, want 2 (the cache serves repeat lookups)", listed)
	}
}

func TestValidatePathForTraversal(t *testing.T) {
	for _, p := range []string{"../x", "Files/%2e%2e/x", `a\..\b`, "a/ ~ /b", "./x"} {
		if validatePathForTraversal(p, "path") == nil {
			t.Errorf("%q accepted", p)
		}
	}
	for _, p := range []string{"Files/a.b/c", "Tables/t", "..x/y", ""} {
		if err := validatePathForTraversal(p, "path"); err != nil {
			t.Errorf("%q rejected: %v", p, err)
		}
	}
	if got := resolveDirectoryPath("/raw/2024"); got != "Files/raw/2024" {
		t.Errorf("resolveDirectoryPath = %q", got)
	}
	if got := resolveDirectoryPath("tables/t"); got != "tables/t" {
		t.Errorf("resolveDirectoryPath = %q", got)
	}
}

func TestListItemsErrors(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/fabric/workspaces/"):
			_, _ = io.WriteString(w, `{"displayName":"my ws"}`)
		case r.URL.Path == "/api/my ws":
			_, _ = io.WriteString(w, itemsXML)
		default:
			http.Error(w, "nope", http.StatusNotFound)
		}
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-items", nil)
	if got := jsonOf(env); !isErr || got != `{"duration":0,"message":"Workspace identifier is required. Provide --workspace or --workspace-id.","status":400}` {
		t.Errorf("missing workspace: %s", got)
	}
	// A GUID the data plane rejects is retried by workspace name.
	env, isErr = call(t, cs, "onelake_list-items", map[string]any{"workspace-id": "33333333-3333-3333-3333-333333333333"})
	if isErr || !strings.Contains(jsonOf(env), "sales.Lakehouse") {
		t.Errorf("fallback: %s", jsonOf(env))
	}
	env, isErr = call(t, cs, "onelake_list-items", map[string]any{"workspace": "other"})
	if !isErr || env["status"] != 404.0 || !strings.HasPrefix(env["message"].(string), "HTTP request failed: Response status code does not indicate success: 404 (Not Found).") {
		t.Errorf("not found: %s", jsonOf(env))
	}
}
