// Package core ports the upstream Fabric.Mcp.Tools.Core area: OneLake
// catalog search and item creation.
package core

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/microsoft/fabric-sdk-go/fabric"
	fabcore "github.com/microsoft/fabric-sdk-go/fabric/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// Area is the "core" tool namespace.
type Area struct {
	catalog *fabcore.CatalogClient
	items   *fabcore.ItemsClient
}

// New returns the core area backed by client.
func New(client *fabric.Client) *Area {
	f := fabcore.NewClientFactoryWithClient(*client)
	return &Area{catalog: f.NewCatalogClient(), items: f.NewItemsClient()}
}

// Name implements server.Area.
func (*Area) Name() string { return "core" }

// Description implements server.Describer, porting FabricCoreSetup's
// CommandGroup description verbatim for namespace mode's "core" proxy tool.
func (*Area) Description() string {
	return "Microsoft Fabric Core Operations - Search, create, and manage Fabric items.\n" +
		"Use this tool when you need to:\n" +
		"- Search the OneLake catalog to discover Fabric items across workspaces\n" +
		"- Create new Fabric items (Lakehouse, Notebook, etc.)\n" +
		"- Manage core Fabric workspace items\n" +
		"This tool provides core operations for working with Fabric resources."
}

// Title implements server.Describer. FabricCoreSetup's CommandGroup gets no
// title, so upstream falls back to the namespace name ("core"); returning ""
// here does the same via server.describe.
func (*Area) Title() string { return "" }

func boolPtr(b bool) *bool { return &b }

// Register implements server.Area.
func (a *Area) Register(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "create-item",
		Description: "Creates a new item in a Fabric workspace. Use this when the user wants to create a Lakehouse, " +
			"Notebook, or other Fabric item type. Requires workspace ID, item name, and item type.",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Create Fabric Item",
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.createItem)

	server.AddTool(r, &mcp.Tool{
		Name: "search-catalog",
		Description: "Searches the Microsoft Fabric OneLake catalog for items matching the specified criteria. " +
			"Supports cross-workspace search over catalog metadata and returns results filtered to entries the " +
			"calling principal is authorized to access. Use this when the user wants to discover or find Fabric " +
			"items (Lakehouse, Report, Notebook, and other item types) across workspaces by name, description, or " +
			"workspace name. Optionally filter by item type.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Search Catalog", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}, a.searchCatalog)
}

type searchInput struct {
	Search            string `json:"search,omitempty" jsonschema:"The text query for the search. Supports searching across display name, description and workspace name of the catalog entry."`
	Filter            string `json:"filter,omitempty" jsonschema:"The filter for the search. Supports filtering by type of entries. Supported operators: eq (Equals), ne (Not Equals), or (Logical OR), () (Parentheses for grouping). Example: \"Type eq 'Report' or Type eq 'Lakehouse'\". Supported item types include Report, Lakehouse, Notebook, Warehouse, SemanticModel, KQLDatabase, and DataPipeline; for the full list see the Catalog Search API reference at https://learn.microsoft.com/rest/api/fabric/core/catalog/search."`
	PageSize          *int   `json:"page-size,omitempty" jsonschema:"The page size that needs to be returned. Must be between 1 and 1000."`
	ContinuationToken string `json:"continuation-token,omitempty" jsonschema:"A token for retrieving the next page of results."`
}

// catalogSearchResponse and the types below port upstream's CoreModels,
// which keep only these fields of the API response.
type catalogSearchResponse struct {
	Value             []catalogEntry `json:"value"`
	ContinuationToken *string        `json:"continuationToken,omitempty"`
}

type catalogEntry struct {
	ID               string  `json:"id"`
	Type             string  `json:"type"`
	CatalogEntryType string  `json:"catalogEntryType"`
	DisplayName      string  `json:"displayName"`
	Description      *string `json:"description,omitempty"`
	Hierarchy        *struct {
		Workspace *struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"workspace,omitempty"`
	} `json:"hierarchy,omitempty"`
}

func (a *Area) searchCatalog(ctx context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, any, error) {
	if in.PageSize != nil && (*in.PageSize < 1 || *in.PageSize > 1000) {
		return response.Fail(http.StatusBadRequest, "Page size must be between 1 and 1000."), nil, nil
	}
	req := fabcore.CatalogQueryRequest{
		Search:            nonEmpty(in.Search),
		Filter:            nonEmpty(in.Filter),
		ContinuationToken: nonEmpty(in.ContinuationToken),
	}
	if in.PageSize != nil {
		n := int32(*in.PageSize)
		req.PageSize = &n
	}
	var raw *http.Response
	if _, err := a.catalog.Search(policy.WithCaptureResponse(ctx, &raw), req, nil); err != nil {
		return response.Error(err), nil, nil
	}
	out, err := decode[catalogSearchResponse](raw)
	if err != nil {
		return response.Error(err), nil, nil
	}
	if out.Value == nil {
		out.Value = []catalogEntry{}
	}
	return response.Success(map[string]any{"results": out}), nil, nil
}

type createInput struct {
	WorkspaceID string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace   string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	DisplayName string `json:"display-name" jsonschema:"The display name for the item."`
	ItemType    string `json:"item-type" jsonschema:"The type of the Fabric item (e.g., Lakehouse, Notebook, etc.)."`
	Description string `json:"description,omitempty" jsonschema:"The description for the item."`
}

type fabricItem struct {
	ID               string     `json:"id"`
	DisplayName      string     `json:"displayName"`
	Description      *string    `json:"description,omitempty"`
	Type             string     `json:"type"`
	WorkspaceID      string     `json:"workspaceId"`
	Definition       any        `json:"definition,omitempty"`
	CreatedDate      *time.Time `json:"createdDate,omitempty"`
	LastModifiedDate *time.Time `json:"lastModifiedDate,omitempty"`
}

func (a *Area) createItem(ctx context.Context, _ *mcp.CallToolRequest, in createInput) (*mcp.CallToolResult, any, error) {
	// Like upstream, --workspace is used as an ID; names are not resolved.
	ws := in.WorkspaceID
	if strings.TrimSpace(ws) == "" {
		ws = in.Workspace
	}
	if strings.TrimSpace(ws) == "" {
		return response.Fail(http.StatusBadRequest, "Workspace identifier is required. Provide --workspace or --workspace-id."), nil, nil
	}
	typ := fabcore.ItemType(in.ItemType)
	req := fabcore.CreateItemRequest{DisplayName: &in.DisplayName, Type: &typ, Description: nonEmpty(in.Description)}
	// CreateItem polls the long-running operation when the API answers 202;
	// the captured response is then the final result.
	var raw *http.Response
	if _, err := a.items.CreateItem(policy.WithCaptureResponse(ctx, &raw), ws, req, nil); err != nil {
		return response.Error(err), nil, nil
	}
	item, err := decode[fabricItem](raw)
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"item": item}), nil, nil
}

// decode reads the raw API response into the upstream result type, which
// keeps only the fields upstream returns. The SDK's typed models can't be
// used: they drop fields upstream keeps, such as "type" on workspace
// catalog entries.
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
