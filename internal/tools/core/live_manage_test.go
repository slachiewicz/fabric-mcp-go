//go:build live

package core_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/core"
)

// TestLiveManage runs the management tools against FABMCP_E2E_WORKSPACE:
// reads of the workspace, its capacity and role assignments, then a
// folder, a notebook with a definition and a disabled schedule, all of
// which it deletes again. It never changes the workspace itself, its role
// assignments or its capacity, and never runs a job.
//
//	FABMCP_E2E_WORKSPACE=<id> go test -tags live ./internal/tools/core/ -run LiveManage -v
func TestLiveManage(t *testing.T) {
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	// Cleanups below call tools, so the session must outlive them; t.Cleanup
	// runs in reverse order, after deferred calls.
	t.Cleanup(cancel)
	s, err := server.New(ctx, server.Options{Mode: server.ModeAll, DisableElicitation: true}, core.New(client), core.NewJobs(client))
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "live"}, nil).Connect(ctx, ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })

	ok := func(tool string, args map[string]any) map[string]any {
		t.Helper()
		env, isErr := call(t, cs, tool, args)
		if isErr {
			t.Fatalf("%s: %s", tool, jsonOf(env))
		}
		res, _ := env["results"].(map[string]any)
		return res
	}

	wsRes := ok("core_get-workspace", map[string]any{"workspace-id": ws})
	workspace := wsRes["workspace"].(map[string]any)
	ok("core_list-workspaces", map[string]any{"roles": "admin,member,contributor,viewer"})
	ok("core_list-capacities", nil)
	if c, has := workspace["capacityId"].(string); has {
		ok("core_get-capacity", map[string]any{"capacity-id": c})
	}
	roles := ok("core_list-workspace-role-assignments", map[string]any{"workspace-id": ws})
	if list, _ := roles["roleAssignments"].([]any); len(list) > 0 {
		id := list[0].(map[string]any)["id"].(string)
		ok("core_get-workspace-role-assignment", map[string]any{"workspace-id": ws, "workspace-role-assignment-id": id})
	}

	suffix := time.Now().Unix()
	folder := ok("core_create-folder", map[string]any{"workspace-id": ws, "display-name": fmt.Sprintf("fabmcp_test_%d", suffix)})["folder"].(map[string]any)["id"].(string)
	t.Cleanup(func() {
		if env, isErr := call(t, cs, "core_delete-folder", map[string]any{"workspace-id": ws, "folder-id": folder}); isErr {
			t.Errorf("cleanup: delete folder %s: %s", folder, jsonOf(env))
		}
	})
	ok("core_get-folder", map[string]any{"workspace-id": ws, "folder-id": folder})
	ok("core_update-folder", map[string]any{"workspace-id": ws, "folder-id": folder, "display-name": fmt.Sprintf("fabmcp_test_%d_renamed", suffix)})
	ok("core_list-folders", map[string]any{"workspace-id": ws})

	nb := `{"nbformat":4,"nbformat_minor":5,"cells":[{"cell_type":"code","source":["print(1)"],"metadata":{},"outputs":[],"execution_count":null}],"metadata":{"language_info":{"name":"python"}}}`
	def := fmt.Sprintf(`{"format":"ipynb","parts":[{"path":"notebook-content.ipynb","payload":"%s","payloadType":"InlineBase64"}]}`,
		base64.StdEncoding.EncodeToString([]byte(nb)))
	created := ok("core_create-item", map[string]any{
		"workspace-id": ws, "display-name": fmt.Sprintf("fabmcp_test_nb_%d", suffix), "item-type": "Notebook",
		"folder-id": folder, "definition": def, "sync": true, "max-wait-seconds": 180,
	})
	itemMeta, _ := created["item"].(map[string]any)
	if itemMeta == nil {
		t.Fatalf("create-item returned no item: %s", jsonOf(created))
	}
	item := itemMeta["id"].(string)
	t.Cleanup(func() {
		if env, isErr := call(t, cs, "core_delete-item", map[string]any{"workspace-id": ws, "item-id": item}); isErr {
			t.Errorf("cleanup: delete item %s: %s", item, jsonOf(env))
		}
	})
	ok("core_get-item", map[string]any{"workspace-id": ws, "item-id": item})
	ok("core_update-item", map[string]any{"workspace-id": ws, "item-id": item, "description": "fabric-mcp-go live test"})
	ok("core_list-items", map[string]any{"workspace-id": ws, "type": "Notebook", "root-folder-id": folder})
	got := ok("core_get-item-definition", map[string]any{"workspace-id": ws, "item-id": item, "format": "ipynb", "sync": true})
	if got["definition"] == nil {
		t.Fatalf("get-item-definition returned no definition: %s", jsonOf(got))
	}
	ok("core_update-item-definition", map[string]any{"workspace-id": ws, "item-id": item, "definition": def, "sync": true})
	ok("core_move-item", map[string]any{"workspace-id": ws, "item-id": item})
	ok("jobs_list-item-job-instances", map[string]any{"workspace-id": ws, "item-id": item})

	// A disabled schedule a year ahead never triggers a job.
	start := time.Now().UTC().AddDate(1, 0, 0).Truncate(time.Second)
	cfg := fmt.Sprintf(`{"type":"Cron","startDateTime":"%s","endDateTime":"%s","localTimeZoneId":"UTC","interval":1440}`,
		start.Format("2006-01-02T15:04:05Z"), start.AddDate(0, 1, 0).Format("2006-01-02T15:04:05Z"))
	sched := ok("jobs_create-item-schedule", map[string]any{
		"workspace-id": ws, "item-id": item, "job-type": "RunNotebook", "enabled": false, "configuration": cfg,
	})["schedule"].(map[string]any)["id"].(string)
	deleted := false
	t.Cleanup(func() {
		if deleted {
			return
		}
		if env, isErr := call(t, cs, "jobs_delete-item-schedule", map[string]any{"workspace-id": ws, "item-id": item, "job-type": "RunNotebook", "schedule-id": sched}); isErr {
			t.Errorf("cleanup: delete schedule %s: %s", sched, jsonOf(env))
		}
	})
	ok("jobs_get-item-schedule", map[string]any{"workspace-id": ws, "item-id": item, "job-type": "RunNotebook", "schedule-id": sched})
	ok("jobs_list-item-schedules", map[string]any{"workspace-id": ws, "item-id": item, "job-type": "RunNotebook"})
	ok("jobs_update-item-schedule", map[string]any{"workspace-id": ws, "item-id": item, "job-type": "RunNotebook", "schedule-id": sched, "enabled": false, "configuration": cfg})
	ok("jobs_delete-item-schedule", map[string]any{"workspace-id": ws, "item-id": item, "job-type": "RunNotebook", "schedule-id": sched})
	deleted = true
}
