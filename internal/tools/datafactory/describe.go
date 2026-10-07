package datafactory

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/apache/arrow-go/v18/arrow/ipc"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/microsoft/fabric-sdk-go/fabric/dataflow"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/sync/errgroup"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// ExtArea is the "dataflow-ext" tool namespace: dataflow tools that upstream
// doesn't have. It's registered only with --extensions, so the default tool
// list stays identical to upstream's.
type ExtArea struct {
	dfQuery *dataflow.QueryExecutionClient
}

// NewExt returns the dataflow-ext area backed by client.
func NewExt(client *fabric.Client) *ExtArea {
	return &ExtArea{dfQuery: dataflow.NewClientFactoryWithClient(*client).NewQueryExecutionClient()}
}

// Name implements server.Area.
func (*ExtArea) Name() string { return "dataflow-ext" }

// Description implements server.Describer.
func (*ExtArea) Description() string {
	return "Dataflow exploration tools beyond the upstream Fabric MCP server.\n" +
		"Use this tool when you need to:\n" +
		"- Describe a table reachable from a dataflow: its columns, types, row count and sample rows"
}

// Title implements server.Describer.
func (*ExtArea) Title() string { return "Dataflow Extensions" }

// Register implements server.Area.
func (a *ExtArea) Register(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "describe-table",
		Description: "Describes a table in one call: column names, Power Query types and nullability (from Table.Schema), " +
			"the row count, and the first rows. The source is an M expression that evaluates to a table, evaluated " +
			"with the dataflow's connections, for example " +
			"Lakehouse.Contents(null){[workspaceId=\"...\"]}[Data]{[lakehouseId=\"...\"]}[Data]{[Id=\"sales\",ItemKind=\"Table\"]}[Data].",
		Annotations: &mcp.ToolAnnotations{
			Title: "Describe Table", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.describeTable)
}

const (
	defaultSampleSize = 5
	maxSampleSize     = 100
)

type describeTableInput struct {
	WorkspaceID     string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	DataflowID      string `json:"dataflow-id" jsonschema:"The ID of the dataflow whose connections the source uses."`
	Source          string `json:"source" jsonschema:"An M (Power Query) expression that evaluates to the table to describe."`
	SampleSize      *int   `json:"sample-size,omitempty" jsonschema:"Number of rows to return, 0 to 100. Defaults to 5."`
	IncludeRowCount *bool  `json:"include-row-count,omitempty" jsonschema:"Whether to count the table's rows, which may scan it. Defaults to true."`
}

// schemaFields are the Table.Schema columns describe-table reports, keyed by
// the name it reports them under. Name, Position and Kind are always present.
var schemaFields = []struct{ from, to string }{
	{"Name", "name"},
	{"Position", "position"},
	{"TypeName", "type"},
	{"Kind", "kind"},
	{"IsNullable", "nullable"},
	{"NativeTypeName", "nativeType"},
	{"NumericPrecision", "precision"},
	{"NumericScale", "scale"},
	{"MaxLength", "maxLength"},
}

func (a *ExtArea) describeTable(ctx context.Context, _ *mcp.CallToolRequest, in describeTableInput) (*mcp.CallToolResult, any, error) {
	if bad := requireNonEmpty("workspaceId", in.WorkspaceID); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("dataflowId", in.DataflowID); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("source", in.Source); bad != nil {
		return bad, nil, nil
	}
	sampleSize := defaultSampleSize
	if in.SampleSize != nil {
		sampleSize = *in.SampleSize
	}
	if sampleSize < 0 || sampleSize > maxSampleSize {
		return response.Fail(http.StatusBadRequest, fmt.Sprintf("sampleSize must be between 0 and %d", maxSampleSize)), nil, nil
	}
	countRows := in.IncludeRowCount == nil || *in.IncludeRowCount

	doc := describeDocument(in.Source, sampleSize)
	var schema, sample, count []map[string]any
	g, gctx := errgroup.WithContext(ctx)
	run := func(queryName string, out *[]map[string]any) {
		g.Go(func() error {
			body, err := runQuery(gctx, a.dfQuery, in.WorkspaceID, in.DataflowID, queryName, doc)
			if err != nil {
				return err
			}
			rows, err := readArrowRows(body)
			if err != nil {
				return fmt.Errorf("%s: %w", queryName, err)
			}
			*out = rows
			return nil
		})
	}
	run("Schema", &schema)
	if sampleSize > 0 {
		run("Sample", &sample)
	}
	if countRows {
		run("RowCount", &count)
	}
	if err := g.Wait(); err != nil {
		return response.Error(err), nil, nil
	}

	columns := make([]map[string]any, 0, len(schema))
	for _, row := range schema {
		col := map[string]any{}
		for _, f := range schemaFields {
			if v, ok := row[f.from]; ok && v != nil {
				col[f.to] = v
			}
		}
		columns = append(columns, col)
	}
	// The service sends date columns as midnight timestamps; report them the
	// way the schema types them.
	for _, row := range schema {
		if row["Kind"] != "date" {
			continue
		}
		for _, r := range sample {
			if v, ok := r[row["Name"].(string)].(string); ok {
				r[row["Name"].(string)] = strings.TrimSuffix(v, " 00:00:00")
			}
		}
	}
	if sample == nil {
		sample = []map[string]any{}
	}
	results := map[string]any{"columns": columns, "sample": sample}
	if countRows {
		if len(count) != 1 {
			return response.Fail(http.StatusInternalServerError, "RowCount query returned no row"), nil, nil
		}
		results["rowCount"] = count[0]["RowCount"]
	}
	return response.Success(results), nil, nil
}

// describeDocument returns the mashup document whose Schema, Sample and
// RowCount queries describe the table source evaluates to.
func describeDocument(source string, sampleSize int) string {
	source = strings.TrimRight(strings.TrimSpace(source), ";")
	return fmt.Sprintf(`section Section1;

shared Source = %s;

shared Schema = Table.Schema(Source);

shared Sample = Table.FirstN(Source, %d);

shared RowCount = #table(type table [RowCount = Int64.Type], {{Table.RowCount(Source)}});`, source, sampleSize)
}

// arrowMetadataColumn is a column the service appends to every query result.
const arrowMetadataColumn = "PQ Arrow Metadata"

// readArrowRows reads an Arrow stream into one map per row, keeping nulls as
// nil rather than as readArrow's "", and leaving out arrowMetadataColumn.
func readArrowRows(data []byte) ([]map[string]any, error) {
	r, err := ipc.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer r.Release()
	fields := r.Schema().Fields()
	var rows []map[string]any
	for r.Next() {
		rec := r.RecordBatch()
		for ri := 0; ri < int(rec.NumRows()); ri++ {
			row := make(map[string]any, len(fields))
			for ci, f := range fields {
				if f.Name == arrowMetadataColumn {
					continue
				}
				row[f.Name] = arrowValue(rec.Column(ci), ri)
			}
			rows = append(rows, row)
		}
	}
	return rows, r.Err()
}
