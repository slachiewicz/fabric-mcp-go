//go:build live

package datafactory_test

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/microsoft/fabric-sdk-go/fabric"
	fabcore "github.com/microsoft/fabric-sdk-go/fabric/core"
	"github.com/microsoft/fabric-sdk-go/fabric/dataflow"
	"github.com/microsoft/fabric-sdk-go/fabric/datapipeline"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/datafactory"
)

// liveSession starts the datafactory area against the real Fabric API,
// authenticated the same way the "fabmcp" binary is (az CLI / environment
// credential chain via internal/auth).
func liveSession(t *testing.T, ctx context.Context, client *fabric.Client) *mcp.ClientSession {
	t.Helper()
	s, err := server.New(server.Options{}, datafactory.New(client))
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
	return cs
}

func liveClient(t *testing.T) *fabric.Client {
	t.Helper()
	cred, err := auth.NewCredential()
	if err != nil {
		t.Fatal(err)
	}
	client, err := fabric.NewClient(cred, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// TestLivePipeline exercises create-pipeline, get-pipeline, and run-pipeline
// against the reserved e2e workspace, then deletes the pipeline it created:
//
//	FABMCP_E2E_WORKSPACE=<id> go test -tags live ./internal/tools/datafactory/ -run LivePipeline -v
func TestLivePipeline(t *testing.T) {
	ws := os.Getenv("FABMCP_E2E_WORKSPACE")
	if ws == "" {
		t.Skip("FABMCP_E2E_WORKSPACE not set")
	}
	client := liveClient(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cs := liveSession(t, ctx, client)

	name := fmt.Sprintf("fabmcp_test_pipeline_%d", time.Now().Unix())
	env, isErr := call(t, cs, "datafactory_create-pipeline", map[string]any{
		"workspace-id": ws, "display-name": name, "description": "fabric-mcp-go live test",
	})
	if isErr {
		t.Fatalf("create-pipeline failed: %s", jsonOf(env))
	}
	pipeline, _ := env["results"].(map[string]any)["pipeline"].(map[string]any)
	id, _ := pipeline["id"].(string)
	if id == "" {
		t.Fatalf("create-pipeline returned no id: %s", jsonOf(env))
	}
	t.Cleanup(func() {
		if _, err := datapipeline.NewClientFactoryWithClient(*client).NewItemsClient().
			DeleteDataPipeline(context.Background(), ws, id, nil); err != nil {
			t.Errorf("cleanup: delete pipeline %s: %v", id, err)
		}
	})

	// The result key shape the reference server returns for a created
	// pipeline: id, displayName, description, type, workspaceId (no
	// folderId, since none was requested).
	var keys []string
	for k := range pipeline {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if got, want := jsonOf(keys), `["description","displayName","id","type","workspaceId"]`; got != want {
		t.Errorf("pipeline keys = %s, want %s", got, want)
	}
	if pipeline["displayName"] != name || pipeline["type"] != "DataPipeline" || pipeline["workspaceId"] != ws {
		t.Errorf("unexpected pipeline %s", jsonOf(pipeline))
	}

	env, isErr = call(t, cs, "datafactory_get-pipeline", map[string]any{"workspace-id": ws, "pipeline-id": id})
	if isErr {
		t.Fatalf("get-pipeline failed: %s", jsonOf(env))
	}
	got, _ := env["results"].(map[string]any)["pipeline"].(map[string]any)
	if got["id"] != id || got["displayName"] != name {
		t.Errorf("get-pipeline returned %s, want id %q displayName %q", jsonOf(got), id, name)
	}

	env, isErr = call(t, cs, "datafactory_run-pipeline", map[string]any{"workspace-id": ws, "pipeline-id": id})
	if isErr {
		t.Fatalf("run-pipeline failed: %s", jsonOf(env))
	}
	results, _ := env["results"].(map[string]any)
	if got, want := jsonOf(mapKeys(results)), `["runId"]`; got != want {
		t.Errorf("run-pipeline result keys = %s, want %s", got, want)
	}
	if runID, _ := results["runId"].(string); runID == "" {
		t.Errorf("run-pipeline returned an empty runId: %s", jsonOf(env))
	}
}

// TestLiveDataflow exercises create-dataflow against the reserved e2e
// workspace, then deletes the dataflow it created:
//
//	FABMCP_E2E_WORKSPACE=<id> go test -tags live ./internal/tools/datafactory/ -run LiveDataflow -v
func TestLiveDataflow(t *testing.T) {
	ws := os.Getenv("FABMCP_E2E_WORKSPACE")
	if ws == "" {
		t.Skip("FABMCP_E2E_WORKSPACE not set")
	}
	client := liveClient(t)
	cs := liveSession(t, context.Background(), client)

	// Creating a dataflow makes Fabric provision staging Lakehouse and
	// Warehouse items that deleting the dataflow leaves behind; remove the
	// ones that appear during this test.
	before := stagingItems(t, client, ws)
	t.Cleanup(func() {
		items := fabcore.NewClientFactoryWithClient(*client).NewItemsClient()
		for id, name := range stagingItems(t, client, ws) {
			if _, ok := before[id]; ok {
				continue
			}
			if _, err := items.DeleteItem(context.Background(), ws, id, nil); err != nil {
				t.Errorf("cleanup: delete staging item %s: %v", name, err)
			}
		}
	})

	name := fmt.Sprintf("fabmcp_test_dataflow_%d", time.Now().Unix())
	env, isErr := call(t, cs, "datafactory_create-dataflow", map[string]any{"workspace-id": ws, "display-name": name})
	if isErr {
		t.Fatalf("create-dataflow failed: %s", jsonOf(env))
	}
	df, _ := env["results"].(map[string]any)["dataflow"].(map[string]any)
	id, _ := df["id"].(string)
	if id == "" {
		t.Fatalf("create-dataflow returned no id: %s", jsonOf(env))
	}
	t.Cleanup(func() {
		if _, err := dataflow.NewClientFactoryWithClient(*client).NewItemsClient().
			DeleteDataflow(context.Background(), ws, id, nil); err != nil {
			t.Errorf("cleanup: delete dataflow %s: %v", id, err)
		}
	})

	// The result key shape the reference server returns for a created
	// dataflow: id, displayName, description, type, workspaceId. Unlike
	// list-dataflows, the create response never carries tags/properties.
	var keys []string
	for k := range df {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if got, want := jsonOf(keys), `["description","displayName","id","type","workspaceId"]`; got != want {
		t.Errorf("dataflow keys = %s, want %s", got, want)
	}
	if df["displayName"] != name || df["type"] != "Dataflow" || df["workspaceId"] != ws {
		t.Errorf("unexpected dataflow %s", jsonOf(df))
	}
}

// stagingItems returns the ID and name of the workspace's dataflow staging
// Lakehouses and Warehouses.
func stagingItems(t *testing.T, client *fabric.Client, ws string) map[string]string {
	t.Helper()
	items, err := fabcore.NewClientFactoryWithClient(*client).NewItemsClient().ListItems(context.Background(), ws, nil)
	if err != nil {
		t.Fatalf("list items: %v", err)
	}
	out := map[string]string{}
	for _, it := range items {
		if n := *it.DisplayName; strings.HasPrefix(n, "StagingLakehouseForDataflows_") || strings.HasPrefix(n, "StagingWarehouseForDataflows_") {
			out[*it.ID] = n
		}
	}
	return out
}

func mapKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// execute-query needs a dataflow with a real, already-published mashup
// document connected to a data source with actual tables before it can
// return meaningful data; that setup is out of scope for the reserved e2e
// workspace, so this port's parity for execute-query is covered by
// datafactory_test.go's httptest fake instead of a live call (see also the
// documented Arrow-decoding divergence in datafactory.go).
