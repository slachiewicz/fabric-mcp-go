//go:build live

package onelake

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/fabric-sdk-go/fabric"
	fabcore "github.com/microsoft/fabric-sdk-go/fabric/core"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
)

// TestLiveShortcuts exercises the shortcut lifecycle against two Lakehouses
// it creates in FABMCP_E2E_WORKSPACE and deletes again:
// create-shortcut-onelake (dst -> src), get-shortcut, list-shortcuts,
// reset-shortcut-cache and delete-shortcut.
//
//	FABMCP_E2E_WORKSPACE=<id> go test -tags live ./internal/tools/onelake/ -run LiveShortcuts -v
func TestLiveShortcuts(t *testing.T) {
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
	items := fabcore.NewClientFactoryWithClient(*client).NewItemsClient()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	now := time.Now().Unix()
	srcID := createLakehouse(t, ctx, items, ws, fmt.Sprintf("fabmcp_sc_src_%d", now))
	dstID := createLakehouse(t, ctx, items, ws, fmt.Sprintf("fabmcp_sc_dst_%d", now))
	t.Cleanup(func() {
		for _, id := range []string{srcID, dstID} {
			if _, err := items.DeleteItem(context.Background(), ws, id, nil); err != nil {
				t.Errorf("cleanup: delete item %s: %v", id, err)
			}
		}
	})

	a := New(cred)
	cs := session(t, a)

	// create-shortcut-onelake: dst/Files/to-src -> src/Files
	env, isErr := call(t, cs, "onelake_create-shortcut-onelake", map[string]any{
		"workspace-id": ws, "item-id": dstID, "shortcut-path": "Files", "shortcut-name": "to-src",
		"target-workspace-id": ws, "target-item-id": srcID, "target-path": "Files",
	})
	if isErr {
		t.Fatalf("create-shortcut-onelake: %s", jsonOf(env))
	}
	sc, _ := env["results"].(map[string]any)["shortcut"].(map[string]any)
	if sc["name"] != "to-src" || sc["path"] != "Files" {
		t.Errorf("created shortcut = %s", jsonOf(sc))
	}
	target, _ := sc["target"].(map[string]any)["oneLake"].(map[string]any)
	if target["workspaceId"] != ws || target["itemId"] != srcID {
		t.Errorf("created shortcut target = %s", jsonOf(target))
	}

	// get-shortcut: the reference returns the same shape back.
	env, isErr = call(t, cs, "onelake_get-shortcut", map[string]any{
		"workspace-id": ws, "item-id": dstID, "shortcut-path": "Files", "shortcut-name": "to-src",
	})
	if isErr {
		t.Fatalf("get-shortcut: %s", jsonOf(env))
	}
	if got, _ := env["results"].(map[string]any)["shortcut"].(map[string]any)["name"].(string); got != "to-src" {
		t.Errorf("get-shortcut name = %q, want %q", got, "to-src")
	}

	// list-shortcuts: the created shortcut is in the (unfiltered) result.
	env, isErr = call(t, cs, "onelake_list-shortcuts", map[string]any{"workspace-id": ws, "item-id": dstID})
	if isErr {
		t.Fatalf("list-shortcuts: %s", jsonOf(env))
	}
	found := false
	for _, s := range env["results"].(map[string]any)["shortcuts"].([]any) {
		if s.(map[string]any)["name"] == "to-src" {
			found = true
		}
	}
	if !found {
		t.Errorf("list-shortcuts didn't return the created shortcut: %s", jsonOf(env))
	}

	// reset-shortcut-cache is workspace-wide; only run it against our own
	// e2e workspace. Some capacities/tiers report the shortcut cache as
	// disabled (errorCode "ExternalShortcutCacheDisabled") rather than
	// resetting it; that's a tenant capability, not a wiring bug, so it's
	// accepted here as evidence the request reached the endpoint and the
	// error mapped through the envelope correctly.
	env, isErr = call(t, cs, "onelake_reset-shortcut-cache", map[string]any{"workspace-id": ws})
	switch {
	case !isErr && env["results"].(map[string]any)["message"] == "Shortcut cache reset successfully.":
		// expected success shape.
	case isErr && strings.Contains(jsonOf(env), "ExternalShortcutCacheDisabled"):
		t.Logf("reset-shortcut-cache: cache disabled on this capacity, accepted: %s", jsonOf(env))
	default:
		t.Errorf("reset-shortcut-cache: isErr=%v env=%s", isErr, jsonOf(env))
	}

	// delete-shortcut
	env, isErr = call(t, cs, "onelake_delete-shortcut", map[string]any{
		"workspace-id": ws, "item-id": dstID, "shortcut-path": "Files", "shortcut-name": "to-src",
	})
	if isErr {
		t.Fatalf("delete-shortcut: %s", jsonOf(env))
	}
	if env["results"].(map[string]any)["message"] != "Shortcut deleted successfully." {
		t.Errorf("delete-shortcut results = %s", jsonOf(env["results"]))
	}

	// list-shortcuts again: the shortcut is gone.
	env, isErr = call(t, cs, "onelake_list-shortcuts", map[string]any{"workspace-id": ws, "item-id": dstID})
	if isErr {
		t.Fatalf("list-shortcuts (after delete): %s", jsonOf(env))
	}
	for _, s := range env["results"].(map[string]any)["shortcuts"].([]any) {
		if s.(map[string]any)["name"] == "to-src" {
			t.Errorf("list-shortcuts still returns the deleted shortcut: %s", jsonOf(env))
		}
	}
}

func createLakehouse(t *testing.T, ctx context.Context, items *fabcore.ItemsClient, ws, name string) string {
	t.Helper()
	typ := fabcore.ItemTypeLakehouse
	resp, err := items.CreateItem(ctx, ws, fabcore.CreateItemRequest{DisplayName: &name, Type: &typ}, nil)
	if err != nil {
		t.Fatalf("create lakehouse %s: %v", name, err)
	}
	if resp.ID == nil {
		t.Fatalf("create lakehouse %s: response has no id", name)
	}
	return *resp.ID
}
