package onelake

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// registerShortcuts ports Fabric.Mcp.Tools.OneLake's Shortcut command group:
// list, get, delete, reset-cache, and (in shortcuts_create.go) the eight
// create-shortcut-* commands, one per target type.
func (a *Area) registerShortcuts(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "list-shortcuts",
		Description: "List shortcuts defined within an item, recursing through subfolders.\n" +
			"Returns each shortcut's path and target. Requires OneLake.Read.All.",
		Annotations: readOnly("List OneLake Shortcuts"),
	}, a.listShortcuts)

	server.AddTool(r, &mcp.Tool{
		Name: "get-shortcut",
		Description: "Get the properties of a single shortcut (name, path, target,\n" +
			"configuration). Requires OneLake.Read.All.",
		Annotations: readOnly("Get OneLake Shortcut"),
	}, a.getShortcut)

	server.AddTool(r, &mcp.Tool{
		Name: "delete-shortcut",
		Description: "Delete a single shortcut from an item. Destructive but the destination\n" +
			"data is preserved — only the shortcut reference is removed. Requires\n" +
			"OneLake.ReadWrite.All.",
		Annotations: write("Delete OneLake Shortcut", true, true),
	}, a.deleteShortcut)

	server.AddTool(r, &mcp.Tool{
		Name: "reset-shortcut-cache",
		Description: "Drop cached shortcut reads for a workspace, forcing the next read to\n" +
			"re-resolve from the destination. Use sparingly — primarily for debugging\n" +
			"stale-cache issues. Requires OneLake.ReadWrite.All.",
		Annotations: write("Reset OneLake Shortcut Cache", false, true),
	}, a.resetShortcutCache)

	a.registerShortcutCreates(r)
}

// --- models ---
//
// These mirror Fabric.Mcp.Tools.OneLake.Models.ShortcutModels exactly.
// OneLake's JSON context writes nulls (no global ignore-nulls option), so no
// field here is omitempty except the two upstream itself marks
// JsonIgnore(WhenWritingNull): shortcutTarget.Type, a GET-only discriminator
// that must never be sent on a create request, and
// oneLakeShortcutTarget.ConnectionID.

// shortcut mirrors upstream's OneLakeShortcut.
type shortcut struct {
	Path   string          `json:"path"`
	Name   string          `json:"name"`
	Target *shortcutTarget `json:"target"`
}

// shortcutTarget mirrors upstream's ShortcutTarget: exactly one of the nine
// target fields is populated at a time, and the other eight are sent (and
// returned) as explicit JSON nulls.
type shortcutTarget struct {
	Type               *string                           `json:"type,omitempty"`
	OneLake            *oneLakeShortcutTarget            `json:"oneLake"`
	AdlsGen2           *adlsGen2ShortcutTarget           `json:"adlsGen2"`
	AmazonS3           *amazonS3ShortcutTarget           `json:"amazonS3"`
	GoogleCloudStorage *googleCloudStorageShortcutTarget `json:"googleCloudStorage"`
	Dataverse          *dataverseShortcutTarget          `json:"dataverse"`
	S3Compatible       *s3CompatibleShortcutTarget       `json:"s3Compatible"`
	ExternalDataShare  *externalDataShareShortcutTarget  `json:"externalDataShare"`
	AzureBlobStorage   *azureBlobStorageShortcutTarget   `json:"azureBlobStorage"`
	OneDriveSharePoint *oneDriveSharePointShortcutTarget `json:"oneDriveSharePoint"`
}

type oneLakeShortcutTarget struct {
	WorkspaceID  *string `json:"workspaceId"`
	ItemID       *string `json:"itemId"`
	Path         *string `json:"path"`
	ConnectionID *string `json:"connectionId,omitempty"`
}

type adlsGen2ShortcutTarget struct {
	Location     *string `json:"location"`
	Subpath      *string `json:"subpath"`
	ConnectionID *string `json:"connectionId"`
}

type amazonS3ShortcutTarget struct {
	Location     *string `json:"location"`
	Subpath      *string `json:"subpath"`
	ConnectionID *string `json:"connectionId"`
}

type googleCloudStorageShortcutTarget struct {
	Location     *string `json:"location"`
	Subpath      *string `json:"subpath"`
	ConnectionID *string `json:"connectionId"`
}

type dataverseShortcutTarget struct {
	EnvironmentDomain *string `json:"environmentDomain"`
	DeltaLakeFolder   *string `json:"deltaLakeFolder"`
	ConnectionID      *string `json:"connectionId"`
}

type s3CompatibleShortcutTarget struct {
	Location     *string `json:"location"`
	Subpath      *string `json:"subpath"`
	ConnectionID *string `json:"connectionId"`
	Bucket       *string `json:"bucket"`
}

type externalDataShareShortcutTarget struct {
	ConnectionID *string `json:"connectionId"`
}

type azureBlobStorageShortcutTarget struct {
	Location     *string `json:"location"`
	Subpath      *string `json:"subpath"`
	ConnectionID *string `json:"connectionId"`
}

type oneDriveSharePointShortcutTarget struct {
	Location                    *string `json:"location"`
	Subpath                     *string `json:"subpath"`
	ConnectionID                *string `json:"connectionId"`
	UpdateFabricItemSensitivity *bool   `json:"updateFabricItemSensitivity"`
}

// shortcutListResponse mirrors upstream's ShortcutListResponse, the raw
// Fabric API list-shortcuts response body.
type shortcutListResponse struct {
	Value             []shortcut `json:"value"`
	ContinuationToken *string    `json:"continuationToken"`
	ContinuationURI   *string    `json:"continuationUri"`
}

// --- list-shortcuts ---

type listShortcutsInput struct {
	WorkspaceID       string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID            string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ParentPath        string `json:"parent-path,omitempty" jsonschema:"The parent path under which to list shortcuts."`
	ContinuationToken string `json:"continuation-token,omitempty" jsonschema:"Token for retrieving the next page of results."`
	IncludeManaged    bool   `json:"include-managed,omitempty" jsonschema:"Include DW-managed shortcuts in the results. Default: false (managed shortcuts are hidden to avoid overwhelming output)."`
}

// shortcutListCommandResult mirrors ShortcutListCommand.ShortcutListCommandResult.
type shortcutListCommandResult struct {
	Shortcuts         []shortcut `json:"shortcuts"`
	ContinuationToken *string    `json:"continuationToken"`
	ContinuationURI   *string    `json:"continuationUri"`
}

// listShortcuts ports ShortcutListCommand, which keeps the default error
// mapping (it doesn't override GetErrorMessage/GetStatusCode).
func (a *Area) listShortcuts(ctx context.Context, _ *mcp.CallToolRequest, in listShortcutsInput) (*mcp.CallToolResult, any, error) {
	u := a.c.ep.fabric + "/workspaces/" + in.WorkspaceID + "/items/" + in.ItemID + "/shortcuts"
	var q []string
	if in.ParentPath != "" {
		q = append(q, "parentPath="+url.QueryEscape(in.ParentPath))
	}
	if in.ContinuationToken != "" {
		q = append(q, "continuationToken="+url.QueryEscape(in.ContinuationToken))
	}
	if len(q) > 0 {
		u += "?" + strings.Join(q, "&")
	}
	b, err := a.c.fabric(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	var list shortcutListResponse
	if len(b) > 0 {
		if err := json.Unmarshal(b, &list); err != nil {
			return response.Error(&opError{msg: "Failed to parse shortcuts list response: " + err.Error()}), nil, nil
		}
	}
	shortcuts := list.Value
	if !in.IncludeManaged {
		shortcuts = filterManagedShortcuts(shortcuts)
	}
	if shortcuts == nil {
		shortcuts = []shortcut{}
	}
	return response.Success(shortcutListCommandResult{
		Shortcuts: shortcuts, ContinuationToken: list.ContinuationToken, ContinuationURI: list.ContinuationURI,
	}), nil, nil
}

// filterManagedShortcuts drops DW-managed shortcuts, ported from
// ShortcutListCommand.IsManagedShortcut: DW-managed shortcuts are created
// internally by Warehouse/SQL endpoints and can number in the hundreds of
// thousands, drowning user-visible shortcuts. They typically reside under
// well-known managed paths ("Tables/dbo.*") with OneLake-internal targets.
func filterManagedShortcuts(in []shortcut) []shortcut {
	out := make([]shortcut, 0, len(in))
	for _, s := range in {
		if !isManagedShortcut(s) {
			out = append(out, s)
		}
	}
	return out
}

func isManagedShortcut(s shortcut) bool {
	if s.Target == nil || s.Target.OneLake == nil {
		return false
	}
	return strings.HasPrefix(strings.ToLower(s.Path), "tables/")
}

// --- get-shortcut / delete-shortcut ---

type shortcutPathInput struct {
	WorkspaceID  string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID       string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
}

func (in shortcutPathInput) url(base string) string {
	return base + "/workspaces/" + in.WorkspaceID + "/items/" + in.ItemID + "/shortcuts/" +
		url.PathEscape(in.ShortcutPath) + "/" + url.PathEscape(in.ShortcutName)
}

type shortcutGetCommandResult struct {
	Shortcut shortcut `json:"shortcut"`
}

// getShortcut ports ShortcutGetCommand.
func (a *Area) getShortcut(ctx context.Context, _ *mcp.CallToolRequest, in shortcutPathInput) (*mcp.CallToolResult, any, error) {
	b, err := a.c.fabric(ctx, http.MethodGet, in.url(a.c.ep.fabric), nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	var sc shortcut
	if len(b) > 0 {
		if err := json.Unmarshal(b, &sc); err != nil {
			return response.Error(&opError{msg: "Failed to parse shortcut response: " + err.Error()}), nil, nil
		}
	}
	return response.Success(shortcutGetCommandResult{Shortcut: sc}), nil, nil
}

type shortcutDeleteCommandResult struct {
	ShortcutPath string `json:"shortcutPath"`
	ShortcutName string `json:"shortcutName"`
	Message      string `json:"message"`
}

// deleteShortcut ports ShortcutDeleteCommand.
func (a *Area) deleteShortcut(ctx context.Context, _ *mcp.CallToolRequest, in shortcutPathInput) (*mcp.CallToolResult, any, error) {
	if _, err := a.c.fabric(ctx, http.MethodDelete, in.url(a.c.ep.fabric), nil); err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(shortcutDeleteCommandResult{
		ShortcutPath: in.ShortcutPath, ShortcutName: in.ShortcutName, Message: "Shortcut deleted successfully.",
	}), nil, nil
}

// --- reset-shortcut-cache ---

type resetShortcutCacheInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
}

type shortcutResetCacheCommandResult struct {
	Message string `json:"message"`
}

// resetShortcutCache ports ShortcutResetCacheCommand.
func (a *Area) resetShortcutCache(ctx context.Context, _ *mcp.CallToolRequest, in resetShortcutCacheInput) (*mcp.CallToolResult, any, error) {
	u := a.c.ep.fabric + "/workspaces/" + in.WorkspaceID + "/onelake/resetShortcutCache"
	if _, err := a.c.fabric(ctx, http.MethodPost, u, nil); err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(shortcutResetCacheCommandResult{Message: "Shortcut cache reset successfully."}), nil, nil
}
