// Package docs ports the upstream Fabric.Mcp.Tools.Docs area: API specs,
// item definitions, best practices and examples served from embedded files.
package docs

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// Area is the "docs" tool namespace.
type Area struct{}

// New returns the docs area.
func New() *Area { return &Area{} }

// Name implements server.Area.
func (*Area) Name() string { return "docs" }

// Description implements server.Describer, porting FabricDocsSetup's
// CommandGroup description verbatim for namespace mode's "docs" proxy tool.
func (*Area) Description() string {
	return "Microsoft Fabric Documentation Tools - Access OpenAPI specifications, best practices, " +
		"and example files for Microsoft Fabric APIs. Use this tool when you need to:\n" +
		"- Discover available Fabric item types and their API specifications\n" +
		"- Retrieve detailed OpenAPI documentation for specific item types\n" +
		"- Access best practice guidance for Fabric development\n" +
		"- Get example API request/response files for implementation reference\n" +
		"This tool provides read-only access to Microsoft Fabric documentation and does NOT " +
		"interact with live Fabric resources or require authentication."
}

// Title implements server.Describer, porting FabricDocsSetup's CommandGroup title.
func (*Area) Title() string { return "Microsoft Fabric Documentation" }

// annotations builds the ToolAnnotations for one tool in this area, porting
// the CommandMetadata every upstream command here declares: read-only,
// non-destructive, idempotent, and closed-world (the embedded resource
// tree, not a live network call). Upstream carries the tool's display name
// in Annotations.Title rather than the top-level Tool.Title (confirmed
// against both the live v1.4.0 reference server and testdata/ref-tools.json
// in internal/parity), so title is set here and Tool.Title is left unset.
func annotations(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		IdempotentHint:  true,
		DestructiveHint: boolPtr(false),
		OpenWorldHint:   boolPtr(false),
	}
}

func boolPtr(b bool) *bool { return &b }

// Register implements server.Area.
func (*Area) Register(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "list-item-types",
		Description: "Lists the Microsoft Fabric item types that have public API specifications available. " +
			"Use this when the user needs to discover which Fabric APIs exist. Returns item type names such as " +
			"notebook, lakehouse, dataPipeline and report, plus a few non-item areas: platform and admin " +
			"(platform and tenant APIs), spark (workspace Spark settings) and realTimeIntelligence " +
			"(workload-level copilot APIs).",
		Annotations: annotations("Available Fabric Item Types"),
	}, listItemTypesHandler)

	server.AddTool(r, &mcp.Tool{
		Name: "item-api-spec",
		Description: "Retrieves the complete OpenAPI specification for a specific Microsoft Fabric item type. " +
			"Use this when the user needs detailed API documentation for an item type such as notebook, " +
			"lakehouse or report. Returns the full API spec in JSON format.",
		Annotations: annotations("Item API Specification"),
	}, itemAPISpecHandler)

	server.AddTool(r, &mcp.Tool{
		Name: "platform-api-spec",
		Description: "Retrieves the OpenAPI specification for core Fabric platform APIs. Use this when the " +
			"user needs documentation for cross-cutting platform APIs like workspace management. Returns " +
			"complete platform API specification.",
		Annotations: annotations("Platform API Specification"),
	}, platformAPISpecHandler)

	server.AddTool(r, &mcp.Tool{
		Name: "item-definitions",
		Description: "Retrieves the JSON schema definition for a Microsoft Fabric item type. Use this when " +
			"the user needs to understand an item's structure or validate an item definition. Returns the " +
			"schema definition for the specified item type.",
		Annotations: annotations("Item Definitions"),
	}, itemDefinitionHandler)

	server.AddTool(r, &mcp.Tool{
		Name: "best-practices",
		Description: "Retrieves embedded best practice documentation for a specific Fabric topic. Use this " +
			"when the user needs guidance, recommendations, or implementation patterns for Fabric features. " +
			"Returns detailed best practice content.",
		Annotations: annotations("Best Practices"),
	}, bestPracticesHandler)

	server.AddTool(r, &mcp.Tool{
		Name: "api-examples",
		Description: "Retrieves example API request and response files for a Microsoft Fabric item type. Use " +
			"this when the user needs sample API calls or implementation examples. Returns a dictionary of " +
			"example files with their contents.",
		Annotations: annotations("API Examples"),
	}, apiExamplesHandler)
}
