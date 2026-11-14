// Package core ports the upstream Fabric.Mcp.Tools.Core project's two
// areas: core (catalog search and workspace, capacity, folder, item,
// definition and operation management) and jobs (see NewJobs).
package core

import (
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/microsoft/fabric-sdk-go/fabric"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// Area is the "core" tool namespace.
type Area struct {
	pipeline   runtime.Pipeline
	base       string // Fabric REST base URL, ending in /v1
	localFiles bool   // definition files may be read and written locally
}

// Option configures an Area.
type Option func(*Area)

// WithLocalFiles sets whether definition tools may read and write local
// files. Upstream allows it only on the stdio transport; the default is
// true.
func WithLocalFiles(enabled bool) Option { return func(a *Area) { a.localFiles = enabled } }

// New returns the core area backed by client. It calls the Fabric REST API
// through client's pipeline rather than the SDK's typed clients, which
// retry and poll where upstream sends each request once.
func New(client *fabric.Client, opts ...Option) *Area {
	a := &Area{
		pipeline:   client.Internal.Pipeline(),
		base:       strings.TrimSuffix(client.Endpoint, "/") + "/v1",
		localFiles: true,
	}
	for _, o := range opts {
		o(a)
	}
	return a
}

// Name implements server.Area.
func (*Area) Name() string { return "core" }

// Description implements server.Describer, porting FabricCoreSetup's
// CommandGroup description verbatim for namespace mode's "core" proxy tool.
func (*Area) Description() string {
	return "Microsoft Fabric Core Operations - Inspect capacities, discover, create, and update workspaces, and search, create, and manage Fabric items.\n" +
		"Use this tool when you need to:\n" +
		"- Get metadata for a known Fabric capacity\n" +
		"- List accessible Fabric capacities and their metadata\n" +
		"- List logical workspace folders and their parent relationships by explicit workspace ID\n" +
		"- Create one logical workspace folder at the root or under an explicitly identified parent\n" +
		"- Delete one explicitly identified empty logical workspace folder, without changing its contents\n" +
		"- Get metadata for one logical workspace folder by explicit workspace and folder IDs\n" +
		"- Move one logical folder and its hierarchy within the same workspace, without enumerating descendants\n" +
		"- Rename one logical workspace folder by explicit workspace and folder IDs\n" +
		"- Search the OneLake catalog to discover Fabric items across workspaces\n" +
		"- Get metadata for an existing Fabric workspace by ID\n" +
		"- Get metadata for one existing Fabric item by workspace ID and item ID\n" +
		"- Retrieve an item's definition inline or explicitly export completed parts to a new local directory\n" +
		"- List accessible workspaces and their management metadata, optionally filtered by the caller's workspace roles\n" +
		"- Get one workspace role assignment by explicit workspace and assignment IDs, without identity lookups\n" +
		"- Add one workspace role assignment for an explicit principal and role, without lookups, defaults, or conflict updates\n" +
		"- Delete one explicitly identified workspace role assignment, without removing the principal or assessing effective access\n" +
		"- Update one existing workspace role assignment by workspace and assignment IDs with an explicit replacement role\n" +
		"- List one page of workspace role assignments and returned principal metadata without directory lookups or group expansion\n" +
		"- List item metadata within a known workspace or folder, optionally filtered by type\n" +
		"- Move one item and its child items within the same workspace, returning the complete moved-item collection\n" +
		"- Create new Fabric items, optionally from inline definitions or explicitly selected local definition files\n" +
		"- Replace a known item's complete definition from inline content or explicitly selected saved files\n" +
		"- Track accepted long-running operations and read their JSON or empty results\n" +
		"- Create Fabric workspaces, optionally assigning an existing capacity and domain\n" +
		"- Rename a known workspace or update or clear its description\n" +
		"- Delete a known Fabric item, with permanent deletion only by explicit opt-in\n" +
		"- Delete an explicitly identified workspace and the items under it\n" +
		"- Manage core Fabric workspace items\n" +
		"- Submit workspace capacity assignments without waiting for completion\n" +
		"This tool provides core operations for working with Fabric resources."
}

// Title implements server.Describer. FabricCoreSetup's CommandGroup gets no
// title, so upstream falls back to the namespace name ("core"); returning ""
// here does the same via server.describe.
func (*Area) Title() string { return "" }

func boolPtr(b bool) *bool { return &b }

// Register implements server.Area, in FabricCoreSetup's order.
func (a *Area) Register(r *server.Registrar) {
	server.AddTool(r, getCapacityTool(), a.getCapacity)
	server.AddTool(r, listCapacitiesTool(), a.listCapacities)
	server.AddTool(r, createFolderTool(), a.createFolder)
	server.AddTool(r, deleteFolderTool(), a.deleteFolder)
	server.AddTool(r, getFolderTool(), a.getFolder)
	server.AddTool(r, listFoldersTool(), a.listFolders)
	server.AddTool(r, moveFolderTool(), a.moveFolder)
	server.AddTool(r, updateFolderTool(), a.updateFolder)
	server.AddTool(r, createItemTool(), a.createItem)
	server.AddTool(r, updateItemDefinitionTool(), a.updateItemDefinition)
	server.AddTool(r, deleteItemTool(), a.deleteItem)
	server.AddTool(r, getItemDefinitionTool(), a.getItemDefinition)
	server.AddTool(r, getItemTool(), a.getItem)
	server.AddTool(r, listItemsTool(), a.listItems)
	server.AddTool(r, moveItemTool(), a.moveItem)
	server.AddTool(r, updateItemTool(), a.updateItem)
	server.AddTool(r, getOperationStateTool(), a.getOperationStateTool)
	server.AddTool(r, getOperationResultTool(), a.getOperationResultTool)
	server.AddTool(r, searchCatalogTool(), a.searchCatalog)
	server.AddTool(r, assignWorkspaceToCapacityTool(), a.assignWorkspaceToCapacity)
	server.AddTool(r, createWorkspaceTool(), a.createWorkspace)
	server.AddTool(r, deleteWorkspaceTool(), a.deleteWorkspace)
	server.AddTool(r, getWorkspaceTool(), a.getWorkspace)
	server.AddTool(r, listWorkspacesTool(), a.listWorkspaces)
	server.AddTool(r, updateWorkspaceTool(), a.updateWorkspace)
}

// bindFallback holds the GetValidationErrorMessage fallback of commands
// that override it; the rest use FabricCoreCommand's.
var bindFallback = map[string]string{
	"core_search-catalog": "Invalid catalog search request. Check option names and values; page-size must be an integer from 1 through 1000.",
	"core_create-item":    "Invalid item creation request. Provide a nonempty workspace UUID, display name, and item type; check option names and values.",
	"core_create-folder":  "Invalid Fabric folder creation. Provide a workspace UUID, a valid folder display name, and optionally a parent folder UUID. The request was not sent.",
	"core_update-folder":  "Invalid Fabric folder update. Provide workspace and folder UUIDs and a valid replacement display name. The request was not sent.",
	"core_move-folder":    "Invalid Fabric folder move. Provide workspace and folder UUIDs, and an optional different target folder UUID. Omit the target to use the workspace root. The request was not sent.",
	"core_delete-folder":  "Invalid Fabric folder deletion. Provide explicit workspace and folder UUIDs.",
	"core_update-item-definition": "Invalid definition update request. Provide nonempty workspace and item UUIDs, exactly one complete definition source, " +
		"and valid options; --update-metadata requires an explicit true or false.",
	"core_move-item": "Invalid Fabric item move. Provide workspace and item UUIDs, and an optional target folder UUID. Omit the target to use the workspace root. The request was not sent.",
}

// BindMessage implements server.BindMessager, porting
// FabricCoreCommand.HandleException's validation message: the missing
// options, else the command's fallback guidance.
func (*Area) BindMessage(tool string, missing []string, msg string) string {
	if tool == "core_assign-workspace-to-capacity" {
		return msg // an AuthenticatedCommand, with the base handler
	}
	if len(missing) > 0 {
		msg = "Missing Required options: " + strings.Join(missing, ", ")
	} else if fb, ok := bindFallback[tool]; ok {
		msg = fb
	} else {
		msg = "Invalid Fabric Core request. Check option names and values; Boolean options must be true or false."
	}
	if tool == "core_delete-folder" {
		msg += " No DELETE request was sent."
	}
	return msg
}
