// Package datafactory ports the upstream Fabric.Mcp.Tools.DataFactory area:
// pipelines and dataflows (list, create, get, run, and dataflow query
// execution).
package datafactory

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/microsoft/fabric-sdk-go/fabric/dataflow"
	"github.com/microsoft/fabric-sdk-go/fabric/datapipeline"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// Area is the "datafactory" tool namespace.
type Area struct {
	dpItems *datapipeline.ItemsClient
	dpJobs  *datapipeline.BackgroundJobsClient
	dfItems *dataflow.ItemsClient
	dfQuery *dataflow.QueryExecutionClient
}

// New returns the datafactory area backed by client.
func New(client *fabric.Client) *Area {
	dp := datapipeline.NewClientFactoryWithClient(*client)
	df := dataflow.NewClientFactoryWithClient(*client)
	return &Area{
		dpItems: dp.NewItemsClient(),
		dpJobs:  dp.NewBackgroundJobsClient(),
		dfItems: df.NewItemsClient(),
		dfQuery: df.NewQueryExecutionClient(),
	}
}

// Name implements server.Area.
func (*Area) Name() string { return "datafactory" }

func boolPtr(b bool) *bool { return &b }

// Register implements server.Area.
func (a *Area) Register(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name:        "list-pipelines",
		Description: "Lists all pipelines in a specified Microsoft Fabric workspace. Requires the workspace ID.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Pipelines", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.listPipelines)

	server.AddTool(r, &mcp.Tool{
		Name: "create-pipeline",
		Description: "Creates a new pipeline in a Microsoft Fabric workspace. Requires workspace ID and display name. " +
			"Optionally provide a description.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Create Pipeline",
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.createPipeline)

	server.AddTool(r, &mcp.Tool{
		Name:        "get-pipeline",
		Description: "Gets details of a specific pipeline in a Microsoft Fabric workspace. Requires workspace ID and pipeline ID.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Pipeline", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.getPipeline)

	server.AddTool(r, &mcp.Tool{
		Name: "run-pipeline",
		Description: "Triggers a run of a specified pipeline in a Microsoft Fabric workspace. Requires workspace ID and " +
			"pipeline ID. Returns the run instance ID.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Run Pipeline",
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.runPipeline)

	server.AddTool(r, &mcp.Tool{
		Name:        "list-dataflows",
		Description: "Lists all dataflows in a specified Microsoft Fabric workspace.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Dataflows", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.listDataflows)

	server.AddTool(r, &mcp.Tool{
		Name:        "create-dataflow",
		Description: "Creates a new dataflow in a specified Microsoft Fabric workspace.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Create Dataflow",
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.createDataflow)

	server.AddTool(r, &mcp.Tool{
		Name:        "execute-query",
		Description: "Executes an M (Power Query) expression against a dataflow in a Microsoft Fabric workspace.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Execute Dataflow Query", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.executeQuery)
}

// item is the upstream Pipeline / Dataflow / Create*Response shape: the
// fields every one of those C# models keeps, in this order. Fields the API
// may omit are pointers so a missing value is dropped from the result
// rather than serialized as null.
type item struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"displayName"`
	Description *string `json:"description,omitempty"`
	Type        string  `json:"type"`
	WorkspaceID string  `json:"workspaceId"`
	FolderID    *string `json:"folderId,omitempty"`
}

// itemTag mirrors upstream's ItemTag.
type itemTag struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// dataflowProperties mirrors upstream's DataflowProperties.
type dataflowProperties struct {
	IsParametric bool `json:"isParametric"`
}

// dataflowEntry is the upstream Dataflow shape used by list-dataflows: an
// item plus the tags/properties fields Pipeline doesn't have.
type dataflowEntry struct {
	item
	Tags       []itemTag           `json:"tags,omitempty"`
	Properties *dataflowProperties `json:"properties,omitempty"`
}

type listResponse[T any] struct {
	Value             []T     `json:"value"`
	ContinuationToken *string `json:"continuationToken,omitempty"`
	ContinuationURI   *string `json:"continuationUri,omitempty"`
}

// --- pipelines ---

type workspaceInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
}

func (a *Area) listPipelines(ctx context.Context, _ *mcp.CallToolRequest, in workspaceInput) (*mcp.CallToolResult, any, error) {
	if bad := requireNonEmpty("workspaceId", in.WorkspaceID); bad != nil {
		return bad, nil, nil
	}
	var raw *http.Response
	pager := a.dpItems.NewListDataPipelinesPager(in.WorkspaceID, nil)
	if _, err := pager.NextPage(policy.WithCaptureResponse(ctx, &raw)); err != nil {
		return response.Error(err), nil, nil
	}
	out, err := decode[listResponse[item]](raw)
	if err != nil {
		return response.Error(err), nil, nil
	}
	if out.Value == nil {
		out.Value = []item{}
	}
	return response.Success(map[string]any{"pipelines": out.Value, "totalCount": len(out.Value)}), nil, nil
}

type createInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	DisplayName string `json:"display-name" jsonschema:"The display name for the item."`
	Description string `json:"description,omitempty" jsonschema:"Optional description for the item."`
}

func (a *Area) createPipeline(ctx context.Context, _ *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, any, error) {
	if bad := requireNonEmpty("workspaceId", in.WorkspaceID); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("displayName", in.DisplayName); bad != nil {
		return bad, nil, nil
	}
	req := datapipeline.CreateDataPipelineRequest{DisplayName: &in.DisplayName, Description: nonEmpty(in.Description)}
	var raw *http.Response
	if _, err := a.dpItems.CreateDataPipeline(policy.WithCaptureResponse(ctx, &raw), in.WorkspaceID, req, nil); err != nil {
		return response.Error(err), nil, nil
	}
	pipeline, err := decode[item](raw)
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"pipeline": pipeline}), nil, nil
}

type pipelineIDInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	PipelineID  string `json:"pipeline-id" jsonschema:"The ID of the pipeline."`
}

func (a *Area) getPipeline(ctx context.Context, _ *mcp.CallToolRequest, in pipelineIDInput) (*mcp.CallToolResult, any, error) {
	if bad := requireNonEmpty("workspaceId", in.WorkspaceID); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("pipelineId", in.PipelineID); bad != nil {
		return bad, nil, nil
	}
	var raw *http.Response
	if _, err := a.dpItems.GetDataPipeline(policy.WithCaptureResponse(ctx, &raw), in.WorkspaceID, in.PipelineID, nil); err != nil {
		return response.Error(err), nil, nil
	}
	pipeline, err := decode[item](raw)
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"pipeline": pipeline}), nil, nil
}

func (a *Area) runPipeline(ctx context.Context, _ *mcp.CallToolRequest, in pipelineIDInput) (*mcp.CallToolResult, any, error) {
	if bad := requireNonEmpty("workspaceId", in.WorkspaceID); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("pipelineId", in.PipelineID); bad != nil {
		return bad, nil, nil
	}
	var raw *http.Response
	if _, err := a.dpJobs.RunOnDemandExecute(policy.WithCaptureResponse(ctx, &raw), in.WorkspaceID, in.PipelineID, nil); err != nil {
		return response.Error(err), nil, nil
	}
	// Like upstream, the job instance ID is the last segment of the
	// Location header the API returns for the accepted run request; no
	// response body is read.
	var runID *string
	if raw != nil {
		if loc := raw.Header.Get("Location"); loc != "" {
			runID = nonEmpty(lastPathSegment(loc))
		}
	}
	return response.Success(map[string]any{"runId": runID}), nil, nil
}

// --- dataflows ---

func (a *Area) listDataflows(ctx context.Context, _ *mcp.CallToolRequest, in workspaceInput) (*mcp.CallToolResult, any, error) {
	if bad := requireNonEmpty("workspaceId", in.WorkspaceID); bad != nil {
		return bad, nil, nil
	}
	var raw *http.Response
	pager := a.dfItems.NewListDataflowsPager(in.WorkspaceID, nil)
	if _, err := pager.NextPage(policy.WithCaptureResponse(ctx, &raw)); err != nil {
		return response.Error(err), nil, nil
	}
	out, err := decode[listResponse[dataflowEntry]](raw)
	if err != nil {
		return response.Error(err), nil, nil
	}
	if out.Value == nil {
		out.Value = []dataflowEntry{}
	}
	return response.Success(map[string]any{"dataflows": out.Value, "totalCount": len(out.Value)}), nil, nil
}

func (a *Area) createDataflow(ctx context.Context, _ *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, any, error) {
	if bad := requireNonEmpty("workspaceId", in.WorkspaceID); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("displayName", in.DisplayName); bad != nil {
		return bad, nil, nil
	}
	req := dataflow.CreateDataflowRequest{DisplayName: &in.DisplayName, Description: nonEmpty(in.Description)}
	var raw *http.Response
	if _, err := a.dfItems.CreateDataflow(policy.WithCaptureResponse(ctx, &raw), in.WorkspaceID, req, nil); err != nil {
		return response.Error(err), nil, nil
	}
	// Like upstream's CreateDataflowAsync, the create response only ever
	// carries the base item fields (no tags/properties), even though the
	// list response can.
	df, err := decode[item](raw)
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"dataflow": df}), nil, nil
}

// --- dataflow query ---

type executeQueryInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	DataflowID  string `json:"dataflow-id" jsonschema:"The ID of the dataflow."`
	QueryName   string `json:"query-name" jsonschema:"The name of the query to execute."`
	Query       string `json:"query" jsonschema:"The M (Power Query) expression to execute."`
}

// querySummary mirrors upstream's QueryResultSummary. fabric-mcp-go doesn't
// decode the Apache Arrow response body upstream parses into rows (see
// arrowParsingError below), so Columns/EstimatedRowCount/StructuredSampleData
// are always absent and BatchCount is always 0.
type querySummary struct {
	Columns              []string         `json:"columns,omitempty"`
	EstimatedRowCount    *int             `json:"estimatedRowCount,omitempty"`
	StructuredSampleData map[string][]any `json:"structuredSampleData,omitempty"`
	BatchCount           int              `json:"batchCount"`
	ArrowParsingSuccess  bool             `json:"arrowParsingSuccess"`
	ArrowParsingError    *string          `json:"arrowParsingError,omitempty"`
}

func (a *Area) executeQuery(ctx context.Context, _ *mcp.CallToolRequest, in executeQueryInput) (*mcp.CallToolResult, any, error) {
	if bad := requireNonEmpty("workspaceId", in.WorkspaceID); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("dataflowId", in.DataflowID); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("queryName", in.QueryName); bad != nil {
		return bad, nil, nil
	}
	if bad := requireNonEmpty("customMashupDocument", in.Query); bad != nil {
		return bad, nil, nil
	}

	wrapped := wrapForDataflowQuery(in.Query, in.QueryName)
	req := dataflow.ExecuteQueryRequest{QueryName: &in.QueryName, CustomMashupDocument: &wrapped}
	poller, err := a.dfQuery.BeginExecuteQuery(ctx, in.WorkspaceID, in.DataflowID, req, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	// ExecuteQuery streams back an Apache Arrow payload, not JSON, so the
	// SDK's typed poller.Result (which json.Unmarshals the final body) can't
	// be used here; drive the poller manually and read the raw response.
	raw, err := pollRaw(ctx, poller)
	if err != nil {
		return response.Error(err), nil, nil
	}
	body, err := runtime.Payload(raw)
	if err != nil {
		return response.Error(err), nil, nil
	}

	contentType := raw.Header.Get("Content-Type")
	contentLength := len(body)
	arrowErr := "Arrow IPC stream decoding is not implemented; fabric-mcp-go reports the raw response size only."
	summary := querySummary{ArrowParsingSuccess: false, ArrowParsingError: &arrowErr}
	data := map[string]any{
		"table": map[string]any{
			"format":      "Table",
			"rowCount":    0,
			"columnCount": 0,
			"summary":     "0 rows × 0 columns",
			"columns":     []any{},
			"rows":        []any{},
		},
		"executionSummary": map[string]any{
			"success":       true,
			"contentType":   contentType,
			"contentLength": contentLength,
			"dataSize":      formatBytes(contentLength),
		},
	}
	return response.Success(map[string]any{"success": true, "data": data, "summary": summary}), nil, nil
}

// wrapForDataflowQuery ports upstream's MQueryExtensions.WrapForDataflowQuery:
// a raw M expression is wrapped in a minimal section document unless it's
// already in that format.
func wrapForDataflowQuery(query, queryName string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" {
		return query
	}
	if len(trimmed) >= len("section ") && strings.EqualFold(trimmed[:len("section ")], "section ") {
		return query
	}
	return fmt.Sprintf("section Section1;\n\nshared %s = %s;", queryName, strings.TrimRight(query, " \t\n\r"))
}

func formatBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.2f KB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.2f MB", float64(n)/(1024*1024))
	}
}

// lastPathSegment returns the last non-empty "/"-separated segment of a URL
// or path, matching upstream's use of Uri.Segments to pull the job instance
// ID off the Location header.
func lastPathSegment(s string) string {
	s = strings.TrimRight(s, "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// requireNonEmpty returns upstream's InvalidParameterEmpty validation
// failure when v is blank, or nil when the call should proceed.
func requireNonEmpty(paramName, v string) *mcp.CallToolResult {
	if strings.TrimSpace(v) != "" {
		return nil
	}
	return response.Fail(http.StatusBadRequest, paramName+" is required and cannot be empty")
}

// decode reads the raw API response into the upstream result type, which
// keeps only the fields upstream returns.
// pollRaw drives an LRO poller to completion and returns the final raw HTTP
// response. Unlike (*runtime.Poller[T]).Result, it never JSON-decodes the
// body, which matters for operations whose final payload isn't JSON (e.g.
// dataflow query execution, which streams back Apache Arrow).
func pollRaw[T any](ctx context.Context, poller *runtime.Poller[T]) (*http.Response, error) {
	for {
		resp, err := poller.Poll(ctx)
		if err != nil {
			return nil, err
		}
		if poller.Done() {
			return resp, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

func decode[T any](resp *http.Response) (T, error) {
	var out T
	b, err := runtime.Payload(resp)
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal(b, &out)
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
