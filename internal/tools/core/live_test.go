//go:build live

package core_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/microsoft/fabric-sdk-go/fabric"
	fabcore "github.com/microsoft/fabric-sdk-go/fabric/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/core"
)

// TestLiveCreateItem creates a Lakehouse in FABMCP_E2E_WORKSPACE (a
// workspace ID reserved for these tests) and deletes it again:
//
//	FABMCP_E2E_WORKSPACE=<id> go test -tags live ./internal/tools/core/ -run Live -v
func TestLiveCreateItem(t *testing.T) {
	ws := os.Getenv("FABMCP_E2E_WORKSPACE")
	if ws == "" {
		t.Skip("FABMCP_E2E_WORKSPACE not set")
	}
	cred, err := auth.NewCredential()
	if err != nil {
		t.Fatal(err)
	}
	client, err := fabric.NewClient(cred, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := server.New(server.Options{}, core.New(client))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "live"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()

	name := fmt.Sprintf("fabmcp_test_%d", time.Now().Unix())
	env, isErr := call(t, cs, "core_create-item", map[string]any{
		"workspace-id": ws, "display-name": name, "item-type": "Lakehouse", "description": "fabric-mcp-go live test",
	})
	if isErr {
		t.Fatalf("create failed: %s", jsonOf(env))
	}
	item, _ := env["results"].(map[string]any)["item"].(map[string]any)
	id, _ := item["id"].(string)
	t.Cleanup(func() {
		if _, err := fabcore.NewClientFactoryWithClient(*client).NewItemsClient().DeleteItem(context.Background(), ws, id, nil); err != nil {
			t.Errorf("cleanup: delete item %s: %v", id, err)
		}
	})

	// The shape upstream main returns for a created Lakehouse.
	var keys []string
	for k := range item {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if got, want := jsonOf(keys), `["description","displayName","id","type","workspaceId"]`; got != want {
		t.Errorf("item keys = %s, want %s", got, want)
	}
	if item["displayName"] != name || item["type"] != "Lakehouse" || item["workspaceId"] != ws {
		b, _ := json.Marshal(item)
		t.Errorf("unexpected item %s", b)
	}
}
