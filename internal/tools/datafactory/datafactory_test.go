package datafactory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/decimal128"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/datafactory"
)

type staticCred struct{}

func (staticCred) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "t", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

// session starts the datafactory area against a fake Fabric API served by h.
func session(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	api := httptest.NewTLSServer(h)
	t.Cleanup(api.Close)
	endpoint := api.URL
	client, err := fabric.NewClient(staticCred{}, &endpoint, &fabric.ClientOptions{
		ClientOptions: azcore.ClientOptions{Transport: api.Client(), Retry: policy.RetryOptions{MaxRetries: -1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := server.New(server.Options{}, datafactory.New(client))
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

func TestListPipelines(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/workspaces/ws1/dataPipelines" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value":[{"id":"p1","displayName":"pl","description":"d","type":"DataPipeline",`+
			`"workspaceId":"ws1","folderId":"f1"}]}`)
	})
	env, isErr := call(t, cs, "datafactory_list-pipelines", map[string]any{"workspace-id": "ws1"})
	want := `{"pipelines":[{"description":"d","displayName":"pl","folderId":"f1","id":"p1","type":"DataPipeline","workspaceId":"ws1"}],"totalCount":1}`
	if got := jsonOf(env["results"]); isErr || got != want {
		t.Errorf("results = %s (isError=%v)\nwant %s", got, isErr, want)
	}
}

func TestListPipelinesEmpty(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value":[]}`)
	})
	env, isErr := call(t, cs, "datafactory_list-pipelines", map[string]any{"workspace-id": "ws1"})
	want := `{"pipelines":[],"totalCount":0}`
	if got := jsonOf(env["results"]); isErr || got != want {
		t.Errorf("results = %s (isError=%v)\nwant %s", got, isErr, want)
	}
}

func TestListPipelinesMissingWorkspace(t *testing.T) {
	cs := session(t, func(http.ResponseWriter, *http.Request) { t.Error("API must not be called") })
	env, isErr := call(t, cs, "datafactory_list-pipelines", map[string]any{"workspace-id": ""})
	want := `{"duration":0,"message":"workspaceId is required and cannot be empty","status":400}`
	if got := jsonOf(env); !isErr || got != want {
		t.Errorf("got %s (isError=%v), want %s", got, isErr, want)
	}
}

func TestCreatePipeline(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workspaces/ws1/dataPipelines" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body map[string]any
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		if got, want := jsonOf(body), `{"description":"d","displayName":"pl"}`; got != want {
			t.Errorf("request body = %s, want %s", got, want)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, `{"id":"p1","displayName":"pl","description":"d","type":"DataPipeline","workspaceId":"ws1"}`)
	})
	env, isErr := call(t, cs, "datafactory_create-pipeline", map[string]any{"workspace-id": "ws1", "display-name": "pl", "description": "d"})
	want := `{"pipeline":{"description":"d","displayName":"pl","id":"p1","type":"DataPipeline","workspaceId":"ws1"}}`
	if got := jsonOf(env["results"]); isErr || got != want {
		t.Errorf("results = %s (isError=%v)\nwant %s", got, isErr, want)
	}
}

func TestGetPipeline(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/workspaces/ws1/dataPipelines/p1" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":"p1","displayName":"pl","type":"DataPipeline","workspaceId":"ws1"}`)
	})
	env, isErr := call(t, cs, "datafactory_get-pipeline", map[string]any{"workspace-id": "ws1", "pipeline-id": "p1"})
	want := `{"pipeline":{"displayName":"pl","id":"p1","type":"DataPipeline","workspaceId":"ws1"}}`
	if got := jsonOf(env["results"]); isErr || got != want {
		t.Errorf("results = %s (isError=%v)\nwant %s", got, isErr, want)
	}
}

func TestGetPipelineNotFound(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"requestId":"r","errorCode":"ItemNotFound","message":"not found","isRetriable":false}`)
	})
	env, isErr := call(t, cs, "datafactory_get-pipeline", map[string]any{"workspace-id": "ws1", "pipeline-id": "missing"})
	if !isErr || env["status"] != float64(503) {
		t.Fatalf("got %s (isError=%v), want status 503", jsonOf(env), isErr)
	}
	results, _ := env["results"].(map[string]any)
	if results["type"] != "HttpRequestException" {
		t.Errorf("results.type = %v, want HttpRequestException", results["type"])
	}
}

func TestRunPipeline(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workspaces/ws1/dataPipelines/p1/jobs/execute/instances" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Location", "https://api.fabric.microsoft.com/v1/workspaces/ws1/items/p1/jobs/instances/job-abc123")
		w.WriteHeader(http.StatusAccepted)
	})
	env, isErr := call(t, cs, "datafactory_run-pipeline", map[string]any{"workspace-id": "ws1", "pipeline-id": "p1"})
	want := `{"runId":"job-abc123"}`
	if got := jsonOf(env["results"]); isErr || got != want {
		t.Errorf("results = %s (isError=%v)\nwant %s", got, isErr, want)
	}
}

func TestListDataflows(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/workspaces/ws1/dataflows" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"value":[{"id":"d1","displayName":"df","type":"Dataflow","workspaceId":"ws1",`+
			`"tags":[{"id":"t1","displayName":"tag1"}],"properties":{"isParametric":true}}]}`)
	})
	env, isErr := call(t, cs, "datafactory_list-dataflows", map[string]any{"workspace-id": "ws1"})
	want := `{"dataflows":[{"displayName":"df","id":"d1","properties":{"isParametric":true},` +
		`"tags":[{"displayName":"tag1","id":"t1"}],"type":"Dataflow","workspaceId":"ws1"}],"totalCount":1}`
	if got := jsonOf(env["results"]); isErr || got != want {
		t.Errorf("results = %s (isError=%v)\nwant %s", got, isErr, want)
	}
}

func TestCreateDataflow(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workspaces/ws1/dataflows" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		// The real API's create response also carries tags/properties, but
		// upstream's CreateDataflowResponse model (and ours) drops them.
		_, _ = io.WriteString(w, `{"id":"d1","displayName":"df","type":"Dataflow","workspaceId":"ws1",`+
			`"tags":[{"id":"t1","displayName":"tag1"}],"properties":{"isParametric":true}}`)
	})
	env, isErr := call(t, cs, "datafactory_create-dataflow", map[string]any{"workspace-id": "ws1", "display-name": "df"})
	want := `{"dataflow":{"displayName":"df","id":"d1","type":"Dataflow","workspaceId":"ws1"}}`
	if got := jsonOf(env["results"]); isErr || got != want {
		t.Errorf("results = %s (isError=%v)\nwant %s", got, isErr, want)
	}
}

// arrowStream returns an Arrow IPC stream with one row per column type
// upstream's reader handles, plus a null.
func arrowStream(t *testing.T) []byte {
	t.Helper()
	schema := arrow.NewSchema([]arrow.Field{
		{Name: "name", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "n", Type: arrow.PrimitiveTypes.Int32, Nullable: true},
		{Name: "price", Type: arrow.PrimitiveTypes.Float64},
		{Name: "ok", Type: arrow.FixedWidthTypes.Boolean},
		{Name: "day", Type: arrow.FixedWidthTypes.Date32},
		{Name: "amount", Type: &arrow.Decimal128Type{Precision: 10, Scale: 2}},
	}, nil)
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()
	b.Field(0).(*array.StringBuilder).AppendValues([]string{"a", ""}, []bool{true, false})
	b.Field(1).(*array.Int32Builder).AppendValues([]int32{7, 0}, []bool{true, false})
	b.Field(2).(*array.Float64Builder).AppendValues([]float64{1.5, 1e20}, nil)
	b.Field(3).(*array.BooleanBuilder).AppendValues([]bool{true, false}, nil)
	b.Field(4).(*array.Date32Builder).AppendValues([]arrow.Date32{arrow.Date32FromTime(time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC)), 0}, nil)
	b.Field(5).(*array.Decimal128Builder).AppendValues([]decimal128.Num{decimal128.FromI64(1250), decimal128.FromI64(-5)}, nil)
	rec := b.NewRecordBatch()
	defer rec.Release()
	var buf bytes.Buffer
	w := ipc.NewWriter(&buf, ipc.WithSchema(schema))
	if err := w.Write(rec); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestExecuteQuery(t *testing.T) {
	var body map[string]any
	stream := arrowStream(t)
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workspaces/ws1/dataflows/df1/executeQuery" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/vnd.apache.arrow.stream")
		_, _ = w.Write(stream)
	})
	env, isErr := call(t, cs, "datafactory_execute-query", map[string]any{
		"workspace-id": "ws1", "dataflow-id": "df1", "query-name": "Query1", "query": "let a = 1 in a",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if got, want := body["customMashupDocument"], "section Section1;\n\nshared Query1 = let a = 1 in a;"; got != want {
		t.Errorf("request customMashupDocument = %q, want %q", got, want)
	}
	results, _ := env["results"].(map[string]any)
	summary, _ := results["summary"].(map[string]any)
	if got, want := jsonOf(summary), `{"arrowParsingSuccess":true,"batchCount":1,"columns":["name","n","price","ok","day","amount"],`+
		`"estimatedRowCount":2,"structuredSampleData":{"amount":["12.50","-0.05"],"day":["2026-09-26","1970-01-01"],`+
		`"n":[7,""],"name":["a",""],"ok":[true,false],"price":[1.5,100000000000000000000]}}`; got != want {
		t.Errorf("summary = %s\nwant %s", got, want)
	}
	table, _ := results["data"].(map[string]any)["table"].(map[string]any)
	if got, want := jsonOf(table["rows"]), `[{"amount":"12.50","day":"2026-09-26","n":"7","name":"a","ok":"True","price":"1.5"},`+
		`{"amount":"-0.05","day":"1970-01-01","n":"","name":"","ok":"False","price":"1E+20"}]`; got != want {
		t.Errorf("rows = %s\nwant %s", got, want)
	}
	if got, want := jsonOf(table["columns"]), `[{"dataType":"String","name":"name"},{"dataType":"Int32","name":"n"},`+
		`{"dataType":"String","name":"price"},{"dataType":"String","name":"ok"},{"dataType":"String","name":"day"},{"dataType":"String","name":"amount"}]`; got != want {
		t.Errorf("columns = %s", got)
	}
	exec, _ := results["data"].(map[string]any)["executionSummary"].(map[string]any)
	meta, _ := exec["executionMetadata"].(map[string]any)
	if exec["contentLength"] != float64(len(stream)) || exec["contentType"] != "application/octet-stream" || meta["queryName"] != "Query1" {
		t.Errorf("executionSummary = %s", jsonOf(exec))
	}
}

func TestExecuteQueryNotArrow(t *testing.T) {
	cs := session(t, func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("not arrow")) })
	env, _ := call(t, cs, "datafactory_execute-query", map[string]any{
		"workspace-id": "ws1", "dataflow-id": "df1", "query-name": "Q", "query": "1",
	})
	summary, _ := env["results"].(map[string]any)["summary"].(map[string]any)
	if summary["arrowParsingSuccess"] != false || summary["arrowParsingError"] == nil {
		t.Errorf("summary = %s", jsonOf(summary))
	}
}

func TestExecuteQueryAlreadyWrapped(t *testing.T) {
	var body map[string]any
	cs := session(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		w.Header().Set("Content-Type", "application/vnd.apache.arrow.stream")
		_, _ = w.Write([]byte("x"))
	})
	query := "section Section1; shared Query1 = 1;"
	_, isErr := call(t, cs, "datafactory_execute-query", map[string]any{
		"workspace-id": "ws1", "dataflow-id": "df1", "query-name": "Query1", "query": query,
	})
	if isErr {
		t.Fatal("unexpected error")
	}
	if got := body["customMashupDocument"]; got != query {
		t.Errorf("request customMashupDocument = %q, want unchanged %q", got, query)
	}
}
