package docs

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
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

// Results carry upstream main's per-command result records
// ({"publicApi":...}, {"definition":...}, ...); the v1.4.0 release returned
// some of them unwrapped.

// listItemTypesHandler ports ListItemTypesCommand.
func listItemTypesHandler(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	types, err := listItemTypes()
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(itemTypeListResult{ItemTypes: types}), nil, nil
}

// itemAPISpecHandler ports GetItemApisCommand, including its "common"
// special case: "common" is a schema-fragment directory, not an item type,
// and upstream steers the caller to "platform" instead of returning a bare
// not-found.
func itemAPISpecHandler(_ context.Context, _ *mcp.CallToolRequest, in itemTypeInput) (*mcp.CallToolResult, any, error) {
	if strings.EqualFold(in.ItemType, "common") {
		return response.Fail(http.StatusNotFound,
			"No item type 'common' exists. Did you mean 'platform'? A full list of supported item types can be found using the 'list-item-types' tool."), nil, nil
	}
	api, err := publicAPI(in.ItemType)
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"publicApi": api}), nil, nil
}

// platformAPISpecHandler ports GetPlatformApisCommand: always the
// "platform" item type, with no input.
func platformAPISpecHandler(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
	api, err := publicAPI("platform")
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"publicApi": api}), nil, nil
}

// itemDefinitionHandler ports GetItemDefinitionCommand.
func itemDefinitionHandler(_ context.Context, _ *mcp.CallToolRequest, in itemTypeInput) (*mcp.CallToolResult, any, error) {
	def, err := itemDefinition(in.ItemType)
	if err != nil {
		return response.Fail(http.StatusNotFound, fmt.Sprintf("No item definition found for item type %s.", in.ItemType)), nil, nil
	}
	return response.Success(map[string]any{"definition": def}), nil, nil
}

// bestPracticesHandler ports GetBestPracticesCommand.
func bestPracticesHandler(_ context.Context, _ *mcp.CallToolRequest, in topicInput) (*mcp.CallToolResult, any, error) {
	practices, err := topicBestPractices(in.Topic)
	if err != nil {
		return response.Fail(http.StatusNotFound, fmt.Sprintf("No best practice resources found for %s", in.Topic)), nil, nil
	}
	return response.Success(map[string]any{"bestPractices": practices}), nil, nil
}

// apiExamplesHandler ports GetExamplesCommand. Unlike the other item-type
// tools, a missing or empty examples directory is not an error upstream -
// ListResourcesInPath returns an empty list rather than throwing - so this
// always succeeds with (possibly empty) results.
func apiExamplesHandler(_ context.Context, _ *mcp.CallToolRequest, in itemTypeInput) (*mcp.CallToolResult, any, error) {
	ex, err := examples(in.ItemType)
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(exampleFileResult{Examples: ex}), nil, nil
}
