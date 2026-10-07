package datafactory_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/apache/arrow-go/v18/arrow"
	"github.com/apache/arrow-go/v18/arrow/array"
	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/apache/arrow-go/v18/arrow/memory"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/datafactory"
)

// writeStream encodes one record batch built by fill as an Arrow stream.
func writeStream(t *testing.T, fields []arrow.Field, fill func(b *array.RecordBuilder)) []byte {
	t.Helper()
	schema := arrow.NewSchema(fields, nil)
	b := array.NewRecordBuilder(memory.DefaultAllocator, schema)
	defer b.Release()
	fill(b)
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

// describeAPI fakes executeQuery for the Schema, Sample and RowCount queries
// and records which ones were asked for, and the mashup document sent.
type describeAPI struct {
	mu      sync.Mutex
	queries []string
	doc     string
	streams map[string][]byte
}

func newDescribeAPI(t *testing.T) *describeAPI {
	schema := writeStream(t, []arrow.Field{
		{Name: "Name", Type: arrow.BinaryTypes.String},
		{Name: "Position", Type: arrow.PrimitiveTypes.Int64},
		{Name: "TypeName", Type: arrow.BinaryTypes.String},
		{Name: "Kind", Type: arrow.BinaryTypes.String},
		{Name: "IsNullable", Type: arrow.FixedWidthTypes.Boolean},
		{Name: "NumericPrecision", Type: arrow.PrimitiveTypes.Int64, Nullable: true},
		{Name: "Description", Type: arrow.BinaryTypes.String, Nullable: true},
	}, func(b *array.RecordBuilder) {
		b.Field(0).(*array.StringBuilder).AppendValues([]string{"id", "name", "day"}, nil)
		b.Field(1).(*array.Int64Builder).AppendValues([]int64{0, 1, 2}, nil)
		b.Field(2).(*array.StringBuilder).AppendValues([]string{"Int64.Type", "Text.Type", "Date.Type"}, nil)
		b.Field(3).(*array.StringBuilder).AppendValues([]string{"number", "text", "date"}, nil)
		b.Field(4).(*array.BooleanBuilder).AppendValues([]bool{false, true, false}, nil)
		b.Field(5).(*array.Int64Builder).AppendValues([]int64{19, 0, 0}, []bool{true, false, false})
		b.Field(6).(*array.StringBuilder).AppendValues([]string{"", "", ""}, []bool{false, false, false})
	})
	// Like the live service, the sample carries dates as timestamps and an
	// extra metadata column.
	day := &arrow.TimestampType{Unit: arrow.Microsecond}
	sample := writeStream(t, []arrow.Field{
		{Name: "id", Type: arrow.PrimitiveTypes.Int64},
		{Name: "name", Type: arrow.BinaryTypes.String, Nullable: true},
		{Name: "day", Type: day},
		{Name: "PQ Arrow Metadata", Type: arrow.BinaryTypes.String, Nullable: true},
	}, func(b *array.RecordBuilder) {
		b.Field(0).(*array.Int64Builder).AppendValues([]int64{1, 2}, nil)
		b.Field(1).(*array.StringBuilder).AppendValues([]string{"a", ""}, []bool{true, false})
		ts := arrow.Timestamp(time.Date(2026, 9, 26, 0, 0, 0, 0, time.UTC).UnixMicro())
		b.Field(2).(*array.TimestampBuilder).AppendValues([]arrow.Timestamp{ts, ts}, nil)
		b.Field(3).(*array.StringBuilder).AppendValues([]string{"", ""}, []bool{false, false})
	})
	count := writeStream(t, []arrow.Field{{Name: "RowCount", Type: arrow.PrimitiveTypes.Int64}}, func(b *array.RecordBuilder) {
		b.Field(0).(*array.Int64Builder).Append(1234)
	})
	return &describeAPI{streams: map[string][]byte{"Schema": schema, "Sample": sample, "RowCount": count}}
}

func (api *describeAPI) handle(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/workspaces/ws1/dataflows/df1/executeQuery" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		var body struct {
			QueryName            string `json:"queryName"`
			CustomMashupDocument string `json:"customMashupDocument"`
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		api.mu.Lock()
		api.queries = append(api.queries, body.QueryName)
		api.doc = body.CustomMashupDocument
		api.mu.Unlock()
		w.Header().Set("Content-Type", "application/vnd.apache.arrow.stream")
		_, _ = w.Write(api.streams[body.QueryName])
	}
}

func (api *describeAPI) sortedQueries() []string {
	api.mu.Lock()
	defer api.mu.Unlock()
	return slices.Sorted(slices.Values(api.queries))
}

func extSession(t *testing.T, h http.HandlerFunc) *mcp.ClientSession {
	t.Helper()
	return sessionWith(t, h, func(c *fabric.Client) server.Area { return datafactory.NewExt(c) })
}

func TestDescribeTable(t *testing.T) {
	api := newDescribeAPI(t)
	cs := extSession(t, api.handle(t))
	env, isErr := call(t, cs, "dataflow-ext_describe-table", map[string]any{
		"workspace-id": "ws1", "dataflow-id": "df1", "source": "  Sql.Database(\"s\", \"d\"){[Item=\"t\"]}[Data];\n", "sample-size": 2,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if got, want := jsonOf(api.sortedQueries()), `["RowCount","Sample","Schema"]`; got != want {
		t.Errorf("queries = %s, want %s", got, want)
	}
	for _, want := range []string{
		"shared Source = Sql.Database(\"s\", \"d\"){[Item=\"t\"]}[Data];\n",
		"shared Sample = Table.FirstN(Source, 2);",
	} {
		if !strings.Contains(api.doc, want) {
			t.Errorf("mashup document lacks %q:\n%s", want, api.doc)
		}
	}
	results, _ := env["results"].(map[string]any)
	if got, want := jsonOf(results), `{"columns":[`+
		`{"kind":"number","name":"id","nullable":false,"position":0,"precision":19,"type":"Int64.Type"},`+
		`{"kind":"text","name":"name","nullable":true,"position":1,"type":"Text.Type"},`+
		`{"kind":"date","name":"day","nullable":false,"position":2,"type":"Date.Type"}],`+
		`"rowCount":1234,"sample":[{"day":"2026-09-26","id":1,"name":"a"},{"day":"2026-09-26","id":2,"name":null}]}`; got != want {
		t.Errorf("results = %s\nwant %s", got, want)
	}
}

func TestDescribeTableSchemaOnly(t *testing.T) {
	api := newDescribeAPI(t)
	cs := extSession(t, api.handle(t))
	env, isErr := call(t, cs, "dataflow-ext_describe-table", map[string]any{
		"workspace-id": "ws1", "dataflow-id": "df1", "source": "Source", "sample-size": 0, "include-row-count": false,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if got, want := jsonOf(api.sortedQueries()), `["Schema"]`; got != want {
		t.Errorf("queries = %s, want %s", got, want)
	}
	results, _ := env["results"].(map[string]any)
	if _, ok := results["rowCount"]; ok {
		t.Errorf("rowCount present without include-row-count: %s", jsonOf(results))
	}
	if got := jsonOf(results["sample"]); got != "[]" {
		t.Errorf("sample = %s, want []", got)
	}
}

func TestDescribeTableBadSampleSize(t *testing.T) {
	cs := extSession(t, func(http.ResponseWriter, *http.Request) { t.Error("no request expected") })
	env, isErr := call(t, cs, "dataflow-ext_describe-table", map[string]any{
		"workspace-id": "ws1", "dataflow-id": "df1", "source": "x", "sample-size": 101,
	})
	if !isErr || env["status"] != float64(http.StatusBadRequest) {
		t.Errorf("want a 400 error, got %s", jsonOf(env))
	}
}

func TestDescribeTableQueryFails(t *testing.T) {
	cs := extSession(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"errorCode":"InvalidMashup","message":"Expression.Error: The name 'x' wasn't recognized."}`))
	})
	env, isErr := call(t, cs, "dataflow-ext_describe-table", map[string]any{
		"workspace-id": "ws1", "dataflow-id": "df1", "source": "x",
	})
	if !isErr || !strings.Contains(jsonOf(env), "wasn't recognized") {
		t.Errorf("want the service error passed through, got %s", jsonOf(env))
	}
}
