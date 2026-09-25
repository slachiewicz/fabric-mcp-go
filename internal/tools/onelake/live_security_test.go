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

// TestLiveSecurity exercises the OneLake data access role and settings
// tools against a real Fabric workspace (FABMCP_E2E_WORKSPACE): it creates
// its own Lakehouse, exercises list/get/create-or-update/delete-data-access-
// role and get-settings/modify-diagnostics against it, restores the
// workspace's original diagnostics settings, and deletes the Lakehouse.
//
// modify-immutability-policy is never called against the tenant here — an
// immutability policy cannot be shortened or removed once set. It is
// covered by TestModifyImmutabilityPolicy (a fake) for the request/response
// shape and by the validation cases below (which never reach the network).
//
//	FABMCP_E2E_WORKSPACE=<id> go test -tags live ./internal/tools/onelake/ -run LiveSecurity -v
func TestLiveSecurity(t *testing.T) {
	ws := os.Getenv("FABMCP_E2E_WORKSPACE")
	if ws == "" {
		t.Skip("FABMCP_E2E_WORKSPACE not set")
	}
	cred, err := auth.NewCredential()
	if err != nil {
		t.Fatal(err)
	}
	fc, err := fabric.NewClient(cred, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items := fabcore.NewClientFactoryWithClient(*fc).NewItemsClient()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	name := fmt.Sprintf("fabmcp_security_%d", time.Now().Unix())
	lhType := fabcore.ItemTypeLakehouse
	created, err := items.CreateItem(ctx, ws, fabcore.CreateItemRequest{DisplayName: &name, Type: &lhType}, nil)
	if err != nil {
		t.Fatalf("create lakehouse: %v", err)
	}
	if created.ID == nil {
		t.Fatal("created lakehouse has no ID")
	}
	itemID := *created.ID
	t.Cleanup(func() {
		if _, err := items.DeleteItem(context.Background(), ws, itemID, nil); err != nil {
			t.Errorf("cleanup: delete lakehouse %s (%s): %v", name, itemID, err)
		} else {
			t.Logf("cleanup: deleted lakehouse %s (%s)", name, itemID)
		}
	})
	t.Logf("created Lakehouse %s (%s) in workspace %s", name, itemID, ws)

	a := New(cred)
	cs := session(t, a)

	// --- data access roles ---
	env, isErr := call(t, cs, "onelake_list-data-access-roles", map[string]any{"workspace-id": ws, "item-id": itemID})
	if isErr {
		t.Logf("list-data-access-roles failed on the fresh Lakehouse (OneLake security may need enabling, or be unavailable, on this capacity): %s", jsonOf(env))
		t.Log("skipping data access role read/write checks; the flat-option builder, JSON round trip, Graph principal resolution and LRO polling are covered by fakes in security_test.go")
	} else {
		// A OneLake role name must start with a letter and contain only
		// letters and numbers (verified live: RequestBodyValidationFailed
		// otherwise).
		roleName := "fabmcpSecurityTestRole"
		env, isErr = call(t, cs, "onelake_create-or-update-data-access-role", map[string]any{
			"workspace-id": ws, "item-id": itemID, "role-name": roleName,
			"entra-members": "sylwester@lachiewicz.com", "permitted-paths": "Files/*",
		})
		if isErr {
			t.Fatalf("create-or-update-data-access-role: %s", jsonOf(env))
		}
		t.Cleanup(func() {
			env, isErr := call(t, cs, "onelake_delete-data-access-role", map[string]any{"workspace-id": ws, "item-id": itemID, "role-name": roleName})
			if isErr {
				t.Errorf("cleanup: delete-data-access-role %s: %s", roleName, jsonOf(env))
			} else {
				t.Logf("cleanup: deleted data access role %s", roleName)
			}
		})

		env, isErr = call(t, cs, "onelake_get-data-access-role", map[string]any{"workspace-id": ws, "item-id": itemID, "role-name": roleName})
		if isErr {
			t.Errorf("get-data-access-role: %s", jsonOf(env))
		} else if role, _ := env["results"].(map[string]any)["role"].(map[string]any); role["name"] != roleName {
			t.Errorf("role name = %v, want %q", role["name"], roleName)
		}

		env, isErr = call(t, cs, "onelake_list-data-access-roles", map[string]any{"workspace-id": ws, "item-id": itemID})
		if isErr {
			t.Errorf("list-data-access-roles after create: %s", jsonOf(env))
		}
	}

	// --- settings: get, modify-diagnostics (restored on cleanup) ---
	env, isErr = call(t, cs, "onelake_get-settings", map[string]any{"workspace-id": ws})
	if isErr {
		t.Fatalf("get-settings: %s", jsonOf(env))
	}
	settings, _ := env["results"].(map[string]any)["settings"].(map[string]any)
	diag, _ := settings["diagnostics"].(map[string]any)
	origStatus, _ := diag["status"].(string)
	if origStatus == "" {
		origStatus = "Disabled"
	}
	var origWorkspaceID, origItemID string
	if dest, ok := diag["destination"].(map[string]any); ok {
		if lh, ok := dest["lakehouse"].(map[string]any); ok {
			origWorkspaceID, _ = lh["workspaceId"].(string)
			origItemID, _ = lh["itemId"].(string)
		}
	}
	t.Logf("original diagnostics: status=%q destination-workspace=%q destination-item=%q", origStatus, origWorkspaceID, origItemID)

	t.Cleanup(func() {
		restore := map[string]any{"workspace-id": ws, "status": origStatus}
		if strings.EqualFold(origStatus, "Enabled") {
			restore["destination-lakehouse-workspace-id"] = origWorkspaceID
			restore["destination-lakehouse-item-id"] = origItemID
		}
		env, isErr := call(t, cs, "onelake_modify-diagnostics", restore)
		if isErr {
			t.Errorf("cleanup: restore diagnostics to status=%q: %s", origStatus, jsonOf(env))
		} else {
			t.Logf("cleanup: restored diagnostics to status=%q", origStatus)
		}
	})

	env, isErr = call(t, cs, "onelake_modify-diagnostics", map[string]any{
		"workspace-id": ws, "status": "Enabled",
		"destination-lakehouse-workspace-id": ws, "destination-lakehouse-item-id": itemID,
	})
	if isErr {
		t.Fatalf("modify-diagnostics enable: %s", jsonOf(env))
	}

	env, isErr = call(t, cs, "onelake_get-settings", map[string]any{"workspace-id": ws})
	if isErr {
		t.Errorf("get-settings after enable: %s", jsonOf(env))
	} else {
		settings, _ = env["results"].(map[string]any)["settings"].(map[string]any)
		diag, _ = settings["diagnostics"].(map[string]any)
		if status, _ := diag["status"].(string); !strings.EqualFold(status, "Enabled") {
			t.Errorf("diagnostics status after enable = %q, want Enabled", status)
		}
	}

	// --- modify-immutability-policy: validation-only, never hits the network.
	// An immutability policy cannot be shortened or removed once set, so this
	// tool is never called against the tenant, live or otherwise.
	env, isErr = call(t, cs, "onelake_modify-immutability-policy", map[string]any{"workspace-id": ws, "scope": "Bogus", "retention-days": 5})
	if !isErr || env["status"] != 400.0 {
		t.Errorf("modify-immutability-policy invalid scope: %s", jsonOf(env))
	}
	env, isErr = call(t, cs, "onelake_modify-immutability-policy", map[string]any{"workspace-id": ws, "scope": "DiagnosticLogs", "retention-days": 0})
	if !isErr || env["status"] != 400.0 {
		t.Errorf("modify-immutability-policy invalid retention: %s", jsonOf(env))
	}
}
