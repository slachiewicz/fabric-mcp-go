package onelake

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// registerTables ports the upstream Commands/Table area: five read-only
// commands over the OneLake table (Iceberg REST) API.
func (a *Area) registerTables(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "get-table-config",
		Description: "Retrieves table API configuration for OneLake. Use this when the user needs to understand " +
			"table access settings.",
		Annotations: readOnly("Get OneLake Table Configuration"),
	}, a.getTableConfig)

	server.AddTool(r, &mcp.Tool{
		Name: "list-table-namespaces",
		Description: "Lists table namespaces in OneLake. Use this when the user needs to discover available table " +
			"namespaces.",
		Annotations: readOnly("List OneLake Table Namespaces"),
	}, a.listTableNamespaces)

	server.AddTool(r, &mcp.Tool{
		Name:        "get-table-namespace",
		Description: "Retrieves metadata for a specific table namespace. Use this when the user needs details about a namespace.",
		Annotations: readOnly("Get OneLake Table Namespace"),
	}, a.getTableNamespace)

	server.AddTool(r, &mcp.Tool{
		Name:        "list-tables",
		Description: "Lists tables in OneLake. Use this when the user needs to see available tables.",
		Annotations: readOnly("List OneLake Tables"),
	}, a.listTables)

	server.AddTool(r, &mcp.Tool{
		Name:        "get-table",
		Description: "Retrieves table definition from OneLake. Use this when the user needs table schema or metadata.",
		Annotations: readOnly("Get OneLake Table"),
	}, a.getTable)
}

// Validation messages upstream's Table command ValidateOptions overrides
// share; errWorkspaceRequired is the one already declared in onelake.go.
const (
	errNamespaceRequired = "Namespace is required. Provide --namespace or --schema."
)

// workspaceItemInput is the workspace/item option pair every Table command
// takes, matching upstream's WorkspaceId/Workspace/ItemId/Item options.
type workspaceItemInput struct {
	WorkspaceID string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace   string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID      string `json:"item-id,omitempty" jsonschema:"The ID of the Fabric item."`
	Item        string `json:"item,omitempty" jsonschema:"The name or ID of the Fabric item. When using friendly names, MUST include the item type suffix (e.g., 'ItemName.Lakehouse', 'ItemName.Warehouse')."`
}

// namespaceInput is the --namespace/--schema alias pair the namespace- and
// table-scoped commands add; upstream treats them as interchangeable.
type namespaceInput struct {
	Namespace string `json:"namespace,omitempty" jsonschema:"The table namespace (schema) to inspect within the OneLake table API."`
	Schema    string `json:"schema,omitempty" jsonschema:"Alias for --namespace when specifying table schemas in the OneLake table API."`
}

// validate ports the workspace/item half of ValidateOptions, common to
// every Table command; errs accumulates upstream's ValidationResult.Errors,
// joined with "\n" by the framework before being reported as one message.
func (in workspaceItemInput) validate(errs []string) []string {
	if strings.TrimSpace(in.WorkspaceID) == "" && strings.TrimSpace(in.Workspace) == "" {
		errs = append(errs, errWorkspaceRequired)
	}
	if strings.TrimSpace(in.ItemID) == "" && strings.TrimSpace(in.Item) == "" {
		errs = append(errs, errItemRequired)
	}
	return errs
}

// validate ports the namespace half of ValidateOptions.
func (in namespaceInput) validate(errs []string) []string {
	if strings.TrimSpace(in.Namespace) == "" && strings.TrimSpace(in.Schema) == "" {
		errs = append(errs, errNamespaceRequired)
	}
	return errs
}

// getTableConfig ports TableConfigGetCommand / GetTableConfigurationAsync.
func (a *Area) getTableConfig(ctx context.Context, _ *mcp.CallToolRequest, in workspaceItemInput) (*mcp.CallToolResult, any, error) {
	if errs := in.validate(nil); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := workspaceOf(in.ItemID, in.Item)

	normWS, normItem, prefix, err := a.tableWarehousePrefix(ctx, ws, item)
	if err != nil {
		return response.Error(err), nil, nil
	}
	// warehouse is the same escaped workspace/item prefix as the path
	// segment below, interpolated as-is (upstream doesn't re-escape it).
	u := a.c.ep.table + "/iceberg/v1/config?warehouse=" + prefix
	raw, err := a.c.oneLake(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	cfg, err := parseTableJSON(raw, "table configuration")
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{
		"workspace": normWS, "item": normItem, "configuration": cfg, "rawResponse": string(raw),
	}), nil, nil
}

// listTableNamespaces ports TableNamespaceListCommand / ListTableNamespacesAsync.
func (a *Area) listTableNamespaces(ctx context.Context, _ *mcp.CallToolRequest, in workspaceItemInput) (*mcp.CallToolResult, any, error) {
	if errs := in.validate(nil); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := workspaceOf(in.ItemID, in.Item)

	normWS, normItem, prefix, err := a.tableWarehousePrefix(ctx, ws, item)
	if err != nil {
		return response.Error(err), nil, nil
	}
	u := a.c.ep.table + "/iceberg/v1/" + prefix + "/namespaces"
	raw, err := a.c.oneLake(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	namespaces, err := parseTableJSON(raw, "table namespace")
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{
		"workspace": normWS, "item": normItem, "namespaces": namespaces, "rawResponse": string(raw),
	}), nil, nil
}

type tableNamespaceGetInput struct {
	workspaceItemInput
	namespaceInput
}

// getTableNamespace ports TableNamespaceGetCommand / GetTableNamespaceAsync.
func (a *Area) getTableNamespace(ctx context.Context, _ *mcp.CallToolRequest, in tableNamespaceGetInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	errs = in.workspaceItemInput.validate(errs)
	errs = in.namespaceInput.validate(errs)
	if len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := workspaceOf(in.ItemID, in.Item)
	ns := strings.TrimSpace(cmpNonEmpty(in.Namespace, in.Schema))

	normWS, normItem, prefix, err := a.tableWarehousePrefix(ctx, ws, item)
	if err != nil {
		return response.Error(err), nil, nil
	}
	u := a.c.ep.table + "/iceberg/v1/" + prefix + "/namespaces/" + url.PathEscape(ns)
	raw, err := a.c.oneLake(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	def, err := parseTableJSON(raw, "table namespace")
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{
		"workspace": normWS, "item": normItem, "namespace": ns, "definition": def, "rawResponse": string(raw),
	}), nil, nil
}

type tableListInput struct {
	workspaceItemInput
	namespaceInput
}

// listTables ports TableListCommand / ListTablesAsync.
func (a *Area) listTables(ctx context.Context, _ *mcp.CallToolRequest, in tableListInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	errs = in.workspaceItemInput.validate(errs)
	errs = in.namespaceInput.validate(errs)
	if len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := workspaceOf(in.ItemID, in.Item)
	ns := strings.TrimSpace(cmpNonEmpty(in.Namespace, in.Schema))

	normWS, normItem, prefix, err := a.tableWarehousePrefix(ctx, ws, item)
	if err != nil {
		return response.Error(err), nil, nil
	}
	u := a.c.ep.table + "/iceberg/v1/" + prefix + "/namespaces/" + url.PathEscape(ns) + "/tables"
	raw, err := a.c.oneLake(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	tables, err := parseTableJSON(raw, "table list")
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{
		"workspace": normWS, "item": normItem, "namespace": ns, "tables": tables, "rawResponse": string(raw),
	}), nil, nil
}

type tableGetInput struct {
	workspaceItemInput
	namespaceInput
	Table string `json:"table" jsonschema:"The table name exposed by the OneLake table API."`
}

// getTable ports TableGetCommand / GetTableAsync.
func (a *Area) getTable(ctx context.Context, _ *mcp.CallToolRequest, in tableGetInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	errs = in.workspaceItemInput.validate(errs)
	errs = in.namespaceInput.validate(errs)
	if len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := workspaceOf(in.ItemID, in.Item)
	ns := strings.TrimSpace(cmpNonEmpty(in.Namespace, in.Schema))
	table := strings.TrimSpace(in.Table)

	normWS, normItem, prefix, err := a.tableWarehousePrefix(ctx, ws, item)
	if err != nil {
		return response.Error(err), nil, nil
	}
	u := a.c.ep.table + "/iceberg/v1/" + prefix + "/namespaces/" + url.PathEscape(ns) + "/tables/" + url.PathEscape(table)
	raw, err := a.c.oneLake(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	def, err := parseTableJSON(raw, "table")
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{
		"workspace": normWS, "item": normItem, "namespace": ns, "table": table, "definition": def, "rawResponse": string(raw),
	}), nil, nil
}

// cmpNonEmpty returns a, or b when a is blank, matching upstream's
// `!string.IsNullOrWhiteSpace(options.Namespace) ? options.Namespace : options.Schema!`.
func cmpNonEmpty(a, b string) string {
	if strings.TrimSpace(a) != "" {
		return a
	}
	return b
}

// tableWarehousePrefix ports GetWarehousePrefixAsync, the Table area's own
// workspace/item normalization. It differs from workspaceAndItem (used by
// every other OneLake tool group, ported from GetNormalizedIdentifiersAsync):
// the item identifier is resolved through the same cache/listing as
// resolveItem only when the workspace identifier itself parses as a GUID;
// otherwise it is used exactly as given (trimmed), unresolved. Upstream
// takes this shortcut because the table API's own warehouse prefix already
// disambiguates on the server side once the workspace is a GUID; ported
// here to match its behavior, not because it's obviously right.
func (a *Area) tableWarehousePrefix(ctx context.Context, ws, item string) (normWS, normItem, warehousePrefix string, err error) {
	normWS, err = normalizeWorkspace(ws)
	if err != nil {
		return "", "", "", err
	}
	normItem = strings.TrimSpace(item)
	if normItem == "" {
		return "", "", "", &argError{msg: "Item identifier is required. (Parameter 'itemIdentifier')"}
	}
	if isGUID(normWS) {
		if normItem, err = a.resolveItem(ctx, normWS, item); err != nil {
			return "", "", "", err
		}
	}
	warehousePrefix = url.PathEscape(strings.TrimRight(normWS, "/")) + "/" + url.PathEscape(strings.TrimLeft(normItem, "/"))
	return normWS, normItem, warehousePrefix, nil
}

// parseTableJSON ports the empty-body/JsonDocument.Parse step every Table
// service method shares. The bytes are kept as json.RawMessage rather than
// decoded, so the value round-trips exactly as JsonElement.Clone() does on
// the upstream side.
func parseTableJSON(raw []byte, what string) (json.RawMessage, error) {
	if strings.TrimSpace(string(raw)) == "" {
		return nil, &opError{msg: "Received empty " + what + " response."}
	}
	if !json.Valid(raw) {
		// Upstream's JsonDocument.Parse throws JsonException here, which
		// GetStatusCode's default switch doesn't special-case either
		// (falls to 500); not an argError/opError, so response.Error below
		// also falls through to response.Error's default mapping.
		return nil, fmt.Errorf("parse OneLake %s response: invalid JSON", what)
	}
	return json.RawMessage(raw), nil
}
