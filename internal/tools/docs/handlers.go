package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// itemTypeInput is the parameter shape shared by item-api-spec,
// item-definitions and api-examples, porting Options.PublicApis.ItemTypeOptions.
// The JSON property name is upstream's kebab-case option name; it doubles
// as the "--item-type" CLI flag name in the C# original.
type itemTypeInput struct {
	ItemType string `json:"item-type" jsonschema:"The Microsoft Fabric item type (e.g. 'notebook', 'lakehouse', 'dataPipeline', 'report', 'warehouse'). Also accepts a few non-item areas: 'platform' and 'admin' (platform and tenant APIs), 'spark' (workspace Spark settings), and 'realTimeIntelligence' (workload-level copilot APIs). Call the 'list-item-types' tool for the full list of supported values."`
}

// topicInput is the best-practices parameter, porting
// Options.BestPractices.GetBestPracticesOptions.
type topicInput struct {
	Topic string `json:"topic" jsonschema:"The best practice topic to retrieve documentation for."`
}

// itemTypeListResult ports ListItemTypesCommand.ItemTypeListCommandResult.
// System.Text.Json's source generator serializes an unmarked record
// property as declared, with no naming policy applied, so the JSON key
// here is the C# member name verbatim rather than camelCase.
type itemTypeListResult struct {
	ItemTypes []string `json:"ItemTypes"`
}

// exampleFileResult ports GetExamplesCommand.ExampleFileResult, for the
// same reason as itemTypeListResult above.
type exampleFileResult struct {
	Examples map[string]string `json:"Examples"`
}

// Every tool in this area answers with a single JSON text content block
// shaped like Microsoft.Mcp.Core's generic command response envelope -
// {"status":<code>,"message":<text>,"results":<value>,"duration":<ms>} -
// and no structuredContent. This was ported from the actual running
// reference server (github.com/microsoft/mcp's fabmcp v1.4.0), not from
// the upstream C# source tree: that source (unreleased, ahead of v1.4.0)
// wraps every result in a per-command record - {"definition":...},
// {"bestPractices":[...]}, {"publicApi":{...}} - but the released binary
// does not, so the wrapper types those records would need
// (GetItemDefinitionCommandResult, GetBestPracticesCommandResult,
// GetItemApisCommandResult/GetPlatformApisCommandResult) are intentionally
// absent here; their result values are ported bare. ItemTypeListCommandResult
// and ExampleFileResult are the exception: v1.4.0 does wrap those two in
// {"ItemTypes":[...]} and {"Examples":{...}}, so itemTypeListResult and
// exampleFileResult above are kept.

// successEnvelope ports the {"status":200,"message":"Success","results":
// ...,"duration":0} shape every successful call returns.
func successEnvelope(results any) *mcp.CallToolResult {
	return jsonResult(false, map[string]any{
		"status": 200, "message": "Success", "results": results, "duration": 0,
	})
}

// notFoundEnvelope ports the plain {"status":404,"message":...,"duration":0}
// shape (no "results" key) that GetItemDefinitionCommand,
// GetBestPracticesCommand, and GetItemApisCommand's "common" pre-check
// produce when they catch a known, expected failure and replace it with
// their own message.
func notFoundEnvelope(message string) *mcp.CallToolResult {
	return jsonResult(true, map[string]any{
		"status": 404, "message": message, "duration": 0,
	})
}

// handleExceptionEnvelope ports Microsoft.Mcp.Core's generic
// exception-to-response fallback: a command that does not catch a
// particular error itself (GetItemApisCommand and GetPlatformApisCommand
// only catch HttpRequestException, which the embedded provider never
// raises) falls through to this shape instead - status 400, the original
// exception's message with a troubleshooting pointer appended, and the
// bare exception message plus its .NET type name echoed under "results".
// Every error this package can actually produce here is the *notFoundError
// from resources.go, i.e. what upstream's FindEmbeddedResource raises as an
// ArgumentException, so "type" is hardcoded to match.
func handleExceptionEnvelope(err error) *mcp.CallToolResult {
	msg := err.Error()
	return jsonResult(true, map[string]any{
		"status": 400,
		"message": msg + ". To mitigate this issue, please refer to the troubleshooting guidelines here at " +
			"https://aka.ms/azmcp/troubleshooting.",
		"results":  map[string]string{"message": msg, "type": "ArgumentException"},
		"duration": 0,
	})
}

func jsonResult(isError bool, envelope map[string]any) *mcp.CallToolResult {
	b, err := json.Marshal(envelope)
	if err != nil {
		panic(fmt.Sprintf("docs: marshal result envelope: %v", err)) // envelope is always JSON-safe
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(b)}},
		IsError: isError,
	}
}

// listItemTypesHandler ports ListItemTypesCommand.
func listItemTypesHandler(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	types, err := listItemTypes()
	if err != nil {
		return handleExceptionEnvelope(err), nil, nil
	}
	return successEnvelope(itemTypeListResult{ItemTypes: types}), nil, nil
}

// itemAPISpecHandler ports GetItemApisCommand, including its "common"
// special case: "common" is a schema-fragment directory, not an item type,
// and upstream steers the caller to "platform" instead of returning a bare
// not-found.
func itemAPISpecHandler(_ context.Context, _ *mcp.CallToolRequest, in itemTypeInput) (*mcp.CallToolResult, any, error) {
	if strings.EqualFold(in.ItemType, "common") {
		return notFoundEnvelope(
			"No item type 'common' exists. Did you mean 'platform'? A full list of supported item types can be found using the 'list-item-types' tool."), nil, nil
	}
	api, err := publicAPI(in.ItemType)
	if err != nil {
		return handleExceptionEnvelope(err), nil, nil
	}
	return successEnvelope(api), nil, nil
}

// platformAPISpecHandler ports GetPlatformApisCommand: always the
// "platform" item type, with no input.
func platformAPISpecHandler(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	api, err := publicAPI("platform")
	if err != nil {
		return handleExceptionEnvelope(err), nil, nil
	}
	return successEnvelope(api), nil, nil
}

// itemDefinitionHandler ports GetItemDefinitionCommand.
func itemDefinitionHandler(_ context.Context, _ *mcp.CallToolRequest, in itemTypeInput) (*mcp.CallToolResult, any, error) {
	def, err := itemDefinition(in.ItemType)
	if err != nil {
		return notFoundEnvelope(fmt.Sprintf("No item definition found for item type %s.", in.ItemType)), nil, nil
	}
	return successEnvelope(def), nil, nil
}

// bestPracticesHandler ports GetBestPracticesCommand.
func bestPracticesHandler(_ context.Context, _ *mcp.CallToolRequest, in topicInput) (*mcp.CallToolResult, any, error) {
	practices, err := topicBestPractices(in.Topic)
	if err != nil {
		return notFoundEnvelope(fmt.Sprintf("No best practice resources found for %s", in.Topic)), nil, nil
	}
	return successEnvelope(practices), nil, nil
}

// apiExamplesHandler ports GetExamplesCommand. Unlike the other item-type
// tools, a missing or empty examples directory is not an error upstream -
// ListResourcesInPath returns an empty list rather than throwing - so this
// always succeeds with (possibly empty) results.
func apiExamplesHandler(_ context.Context, _ *mcp.CallToolRequest, in itemTypeInput) (*mcp.CallToolResult, any, error) {
	ex, err := examples(in.ItemType)
	if err != nil {
		return handleExceptionEnvelope(err), nil, nil
	}
	return successEnvelope(exampleFileResult{Examples: ex}), nil, nil
}
