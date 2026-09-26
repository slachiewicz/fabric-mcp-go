package onelake

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// TestTableWarehousePrefix locks in GetWarehousePrefixAsync's split
// behavior: the item is resolved through the listing/cache only when the
// workspace identifier itself parses as a GUID; a display-name workspace
// leaves the item exactly as given.
func TestTableWarehousePrefix(t *testing.T) {
	var listed int
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		listed++
		_, _ = io.WriteString(w, itemsXML)
	})

	// Non-GUID workspace: item passed through unresolved, no listing call.
	ws, item, prefix, err := a.tableWarehousePrefix(t.Context(), "ws-name", "sales.Lakehouse")
	if err != nil {
		t.Fatalf("tableWarehousePrefix: %v", err)
	}
	if ws != "ws-name" || item != "sales.Lakehouse" || prefix != "ws-name/sales.Lakehouse" {
		t.Errorf("got (%q, %q, %q)", ws, item, prefix)
	}
	if listed != 0 {
		t.Errorf("listed %d times for a non-GUID workspace, want 0", listed)
	}

	// GUID workspace: bare item name is resolved via the listing, same as
	// resolveItem.
	guid := "22222222-2222-2222-2222-222222222222"
	ws, item, prefix, err = a.tableWarehousePrefix(t.Context(), guid, "SALES")
	if err != nil {
		t.Fatalf("tableWarehousePrefix: %v", err)
	}
	if ws != guid || item != "sales.Lakehouse" || prefix != guid+"/sales.Lakehouse" {
		t.Errorf("got (%q, %q, %q)", ws, item, prefix)
	}
	if listed != 1 {
		t.Errorf("listed %d times for a GUID workspace, want 1", listed)
	}

	// GUID workspace, unresolvable item: opError, mapped to 422 by response.Error.
	_, _, _, err = a.tableWarehousePrefix(t.Context(), guid, "no-such-item")
	if err == nil || !strings.Contains(err.Error(), "Unable to resolve item 'no-such-item'") {
		t.Errorf("unresolvable item: err = %v", err)
	}
	res := response.Error(err)
	if !res.IsError {
		t.Fatal("expected an error result")
	}
	env := decodeResult(t, res)
	if env["status"] != 422.0 {
		t.Errorf("unresolvable item status: %v", env)
	}
}

// decodeResult decodes a *mcp.CallToolResult's envelope the way the
// in-process call helper does for a session-mediated call.
func decodeResult(t *testing.T, res *mcp.CallToolResult) map[string]any {
	t.Helper()
	var env map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].(*mcp.TextContent).Text), &env); err != nil {
		t.Fatal(err)
	}
	return env
}

func TestTableCalls(t *testing.T) {
	const guid = "33333333-3333-3333-3333-333333333333"
	var lastPath string
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		lastPath = r.URL.Path + "?" + r.URL.RawQuery
		switch {
		case strings.HasSuffix(r.URL.Path, "/config"):
			_, _ = io.WriteString(w, `{"overrides":{"prefix":"warehouses/abc"}}`)
		case strings.HasSuffix(r.URL.Path, "/namespaces"):
			_, _ = io.WriteString(w, `{"namespaces":[["dbo"]]}`)
		case strings.HasSuffix(r.URL.Path, "/tables"):
			_, _ = io.WriteString(w, `{"identifiers":[{"namespace":["dbo"],"name":"sales"}]}`)
		case strings.HasSuffix(r.URL.Path, "/namespaces/dbo"):
			_, _ = io.WriteString(w, `{"namespace":["dbo"],"properties":{}}`)
		case strings.Contains(r.URL.Path, "/tables/sales"):
			_, _ = io.WriteString(w, `{"metadata-location":"abfss://x","metadata":{"table-uuid":"t"}}`)
		default:
			http.NotFound(w, r)
		}
	})
	cs := session(t, a)

	t.Run("get-table-config", func(t *testing.T) {
		env, isErr := call(t, cs, "onelake_get-table-config", map[string]any{"workspace": "ws-name", "item": "sales.Lakehouse"})
		if isErr {
			t.Fatalf("error result: %s", jsonOf(env))
		}
		if !strings.HasSuffix(lastPath, "/table/iceberg/v1/config?warehouse=ws-name/sales.Lakehouse") {
			t.Errorf("request path = %q", lastPath)
		}
		results := env["results"].(map[string]any)
		if results["workspace"] != "ws-name" || results["item"] != "sales.Lakehouse" {
			t.Errorf("results = %v", results)
		}
		if _, ok := results["configuration"].(map[string]any); !ok {
			t.Errorf("configuration not decoded: %v", results["configuration"])
		}
	})

	t.Run("list-table-namespaces", func(t *testing.T) {
		env, isErr := call(t, cs, "onelake_list-table-namespaces", map[string]any{"workspace-id": guid, "item-id": "sales.Lakehouse"})
		if isErr {
			t.Fatalf("error result: %s", jsonOf(env))
		}
		if !strings.HasSuffix(lastPath, "/table/iceberg/v1/"+guid+"/sales.Lakehouse/namespaces?") {
			t.Errorf("request path = %q", lastPath)
		}
	})

	t.Run("get-table-namespace via schema alias", func(t *testing.T) {
		env, isErr := call(t, cs, "onelake_get-table-namespace", map[string]any{
			"workspace": "ws-name", "item": "sales.Lakehouse", "schema": "dbo",
		})
		if isErr {
			t.Fatalf("error result: %s", jsonOf(env))
		}
		results := env["results"].(map[string]any)
		if results["namespace"] != "dbo" {
			t.Errorf("namespace = %v", results["namespace"])
		}
	})

	t.Run("list-tables", func(t *testing.T) {
		env, isErr := call(t, cs, "onelake_list-tables", map[string]any{
			"workspace": "ws-name", "item": "sales.Lakehouse", "namespace": "dbo",
		})
		if isErr {
			t.Fatalf("error result: %s", jsonOf(env))
		}
		if !strings.HasSuffix(lastPath, "/namespaces/dbo/tables?") {
			t.Errorf("request path = %q", lastPath)
		}
	})

	t.Run("get-table", func(t *testing.T) {
		env, isErr := call(t, cs, "onelake_get-table", map[string]any{
			"workspace": "ws-name", "item": "sales.Lakehouse", "namespace": "dbo", "table": "sales",
		})
		if isErr {
			t.Fatalf("error result: %s", jsonOf(env))
		}
		results := env["results"].(map[string]any)
		if results["table"] != "sales" || results["namespace"] != "dbo" {
			t.Errorf("results = %v", results)
		}
	})
}

func TestTableValidation(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_get-table-config", nil)
	if !isErr {
		t.Fatal("expected an error result")
	}
	want := "Workspace identifier is required. Provide --workspace or --workspace-id.\n" +
		"Item identifier is required. Provide --item or --item-id."
	if env["message"] != want {
		t.Errorf("message = %q, want %q", env["message"], want)
	}
	if env["status"] != 400.0 {
		t.Errorf("status = %v", env["status"])
	}

	env, isErr = call(t, cs, "onelake_list-tables", map[string]any{"workspace": "ws-name", "item": "sales.Lakehouse"})
	if !isErr {
		t.Fatal("expected an error result")
	}
	if env["message"] != "Namespace is required. Provide --namespace or --schema." {
		t.Errorf("message = %q", env["message"])
	}
}

func TestTableEmptyAndHTTPErrors(t *testing.T) {
	empty := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {})
	cs := session(t, empty)
	env, isErr := call(t, cs, "onelake_get-table-config", map[string]any{"workspace": "ws-name", "item": "sales.Lakehouse"})
	if !isErr || env["status"] != 422.0 || env["message"] != "Received empty table configuration response."+troubleshootingSuffix {
		t.Errorf("empty body: %s", jsonOf(env))
	}

	notFound := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", http.StatusNotFound) })
	cs = session(t, notFound)
	env, isErr = call(t, cs, "onelake_get-table", map[string]any{
		"workspace": "ws-name", "item": "sales.Lakehouse", "namespace": "dbo", "table": "sales",
	})
	if !isErr || env["status"] != 404.0 || !strings.HasPrefix(env["message"].(string), "Service unavailable or network connectivity issues. Details:") {
		t.Errorf("404 passthrough: %s", jsonOf(env))
	}
}

// troubleshootingSuffix mirrors response.troubleshooting, appended to every
// Exc-shaped message.
const troubleshootingSuffix = ". To mitigate this issue, please refer to the troubleshooting guidelines here at https://aka.ms/azmcp/troubleshooting."
