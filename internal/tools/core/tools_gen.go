package core

// Code generated from internal/parity/testdata/ref-tools.json; descriptions
// and annotations must match upstream verbatim. Edit by regenerating.

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

type listWorkspacesInput struct {
	ContinuationToken                *string `json:"continuation-token,omitempty" jsonschema:"The continuation token returned by the preceding page. Pass it unchanged and repeat the same roles and endpoint preference to retrieve the next page. Omit for the first page."`
	PreferWorkspaceSpecificEndpoints *bool   `json:"prefer-workspace-specific-endpoints,omitempty" jsonschema:"Whether to request workspace-specific API endpoints in the returned metadata. Omit to use the API default. This does not change the endpoint used to list workspaces."`
	Roles                            *string `json:"roles,omitempty" jsonschema:"Filter by the calling principal's workspace roles: Admin, Member, Contributor, or Viewer, separated by commas. Role names are case-insensitive. Omit to list workspaces without a role filter."`
}

func listWorkspacesTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "list-workspaces",
		Description: "Lists one page of Microsoft Fabric workspaces accessible to the calling principal using the Core REST management API.\n" +
			"Use this to discover workspace IDs and inspect workspace management metadata, optionally filtering by workspace role:\n" +
			"Admin, Member, Contributor, or Viewer. Returns IDs, display names, types, available descriptions, capacity/domain details,\n" +
			"tags, and continuation information. Optionally request workspace-specific API endpoints.\n" +
			"Pass the continuation token with the same filters to get another page.\n" +
			"Unlike onelake_list-workspaces, this is management metadata, not a OneLake storage/data-plane listing.\n" +
			"It does not search catalog items, read item data or definitions, or modify resources.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Fabric Workspaces", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type getWorkspaceInput struct {
	WorkspaceID                      string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace to retrieve. Must be a nonempty UUID."`
	PreferWorkspaceSpecificEndpoints *bool  `json:"prefer-workspace-specific-endpoints,omitempty" jsonschema:"Whether to request workspace-specific API and OneLake endpoints. Omit to preserve the service default. Returned endpoints are metadata only and are not contacted by this tool."`
}

func getWorkspaceTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-workspace",
		Description: "Gets metadata for one existing Microsoft Fabric workspace using its required workspace UUID.\n" +
			"Use this to inspect a known workspace's ID, display name, type, description, capacity, domain,\n" +
			"workspace identity identifiers, and applied tags when available.\n" +
			"Optionally request workspace-specific API and OneLake endpoints; returned endpoints are metadata only.\n" +
			"Does not list workspaces or items, read item data or definitions, modify resources, or poll capacity assignments.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Workspace", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type createWorkspaceInput struct {
	DisplayName string  `json:"display-name" jsonschema:"The display name of the new Microsoft Fabric workspace. Required, not empty or whitespace, and at most 256 characters. Only unused names are allowed; 'Admin monitoring' is reserved."`
	CapacityID  *string `json:"capacity-id,omitempty" jsonschema:"The nonempty UUID of an existing capacity to assign during workspace creation. Requires capacity contributor or admin permission. Does not create a capacity or perform a separate assignment operation."`
	Description *string `json:"description,omitempty" jsonschema:"The workspace description, at most 4000 characters. Omit to use the service default."`
	DomainID    *string `json:"domain-id,omitempty" jsonschema:"The nonempty UUID of an existing domain to assign during workspace creation. Requires permission to assign the workspace to that domain. Does not create or look up a domain."`
}

func createWorkspaceTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "create-workspace",
		Description: "Creates a new Microsoft Fabric workspace with a required display name and optional description.\n" +
			"Optionally assign an existing capacity and domain by UUID in the same creation request.\n" +
			"Returns the created workspace's management metadata and the Location header when supplied.\n" +
			"Requires Fabric workspace-creation permission, capacity contributor/admin access when assigning a capacity,\n" +
			"and domain assignment permission when assigning a domain; delegated callers need Workspace.ReadWrite.All.\n" +
			"Does not provision capacities or domains, resolve names, create items, or poll capacity assignment.\n" +
			"Creation is not idempotent and is never automatically retried. If the outcome is uncertain,\n" +
			"check whether the workspace was created before making another creation request.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Fabric Workspace", ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type updateWorkspaceInput struct {
	WorkspaceID string  `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; workspace names are not accepted."`
	Description *string `json:"description,omitempty" jsonschema:"The new workspace description, up to 4000 characters. An empty string clears the description; omit to leave unchanged."`
	DisplayName *string `json:"display-name,omitempty" jsonschema:"The new workspace display name, up to 256 characters. Must not be empty or whitespace-only and must be unique within the tenant. Admin monitoring is reserved. Omit to leave unchanged."`
}

func updateWorkspaceTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "update-workspace",
		Description: "Updates or renames an existing Microsoft Fabric workspace using its required workspace UUID.\n" +
			"Supply a new display name, a new description, or both. Omitted properties remain unchanged;\n" +
			"an empty description clears it. Display names are limited to 256 characters and descriptions\n" +
			"to 4000 characters. Workspace names must be unique within the tenant; Admin monitoring is reserved.\n" +
			"Returns the workspace ID, display name, type, and description when available.\n" +
			"Requires the workspace Admin role and, for delegated callers, Workspace.ReadWrite.All.\n" +
			"Does not change capacity, domain, identity, permissions, tags, endpoints, items, or data.\n" +
			"Does not resolve names, fetch additional metadata, poll, or retry automatically.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Fabric Workspace", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type deleteWorkspaceInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace to delete. Must be a nonempty UUID. Deleting this workspace also deletes the items under it."`
}

func deleteWorkspaceTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "delete-workspace",
		Description: "Deletes one Microsoft Fabric workspace AND the items under it. This is a destructive operation.\n" +
			"Requires the explicit workspace UUID; workspace names and automatic target selection are not supported.\n" +
			"The caller must have the Admin workspace role; delegated callers also need Workspace.ReadWrite.All.\n" +
			"Returns the targeted workspace ID and a deletion acknowledgement only after Fabric reports successful completion.\n" +
			"Does not enumerate or individually delete items, retry automatically, or poll.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Delete Fabric Workspace", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type assignWorkspaceToCapacityInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the existing Microsoft Fabric workspace, as a nonempty GUID."`
	CapacityID  string `json:"capacity-id" jsonschema:"The ID of the target capacity, as a nonempty GUID."`
}

func assignWorkspaceToCapacityTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "assign-workspace-to-capacity",
		Description: "Submit a request to assign an existing Microsoft Fabric workspace to a target capacity.\n" +
			"Requires workspace-id and capacity-id as nonempty GUIDs, workspace Admin and capacity\n" +
			"Contributor or Admin permissions, and Capacity.ReadWrite.All and Workspace.ReadWrite.All scopes.\n" +
			"Returns 202 Accepted with the requested workspace and capacity IDs, accepted=true, and\n" +
			"state=Pending. Acceptance does not confirm completion. Does not poll, retry, create workspaces,\n" +
			"or provision or resize capacities. Inspect the workspace separately to check assignment progress.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Assign Fabric Workspace to Capacity", ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type listCapacitiesInput struct {
	ContinuationToken *string `json:"continuation-token,omitempty" jsonschema:"The continuation token returned by a previous capacity listing. Pass it unchanged to retrieve the next page, or omit it for the first page."`
}

func listCapacitiesTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "list-capacities",
		Description: "Lists one page of Microsoft Fabric capacities where the calling principal is an administrator or contributor.\n" +
			"Use this to discover accessible capacity IDs, display names, SKUs, regions, and states.\n" +
			"Returns capacity metadata and available continuationToken and continuationUri values.\n" +
			"To request another page, pass the returned continuation token unchanged; omit it for the first page.\n" +
			"Supports users, service principals, and managed identities. Delegated calls require Capacity.Read.All or Capacity.ReadWrite.All.\n" +
			"This is the Fabric Core API, not an Azure subscription inventory or a capacity utilization report.\n" +
			"Does not automatically retrieve additional pages, follow returned URIs, or modify capacities.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Fabric Capacities", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type getCapacityInput struct {
	CapacityID string `json:"capacity-id" jsonschema:"The ID of the Microsoft Fabric capacity to retrieve. Must be a nonempty UUID."`
}

func getCapacityTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-capacity",
		Description: "Gets metadata for one existing Microsoft Fabric capacity using its required capacity UUID.\n" +
			"Use this to inspect a known capacity's ID, display name, SKU, region, and state.\n" +
			"Requires Administrator or Contributor permission on the capacity.\n" +
			"Does not list capacities, read billing or item data, modify resources, assign workspaces,\n" +
			"or poll long-running operations.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Capacity", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type listItemsInput struct {
	WorkspaceID       string  `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID."`
	ContinuationToken *string `json:"continuation-token,omitempty" jsonschema:"The continuation token returned by a previous page, copied unchanged. Keep the same workspace, type, root folder, and recursive options. Omit for the first page."`
	Recursive         *bool   `json:"recursive,omitempty" jsonschema:"Include items in nested folders. Defaults to true; false lists only direct items in the workspace root or specified root folder."`
	RootFolderID      *string `json:"root-folder-id,omitempty" jsonschema:"The root folder ID to list within. Must be a nonempty UUID. Omit to use the workspace root."`
	Type              *string `json:"type,omitempty" jsonschema:"Filter by Fabric item type, such as Lakehouse, Notebook, or Report. Omit to include all types."`
}

func listItemsTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "list-items",
		Description: "Lists one page of Microsoft Fabric item metadata in a known workspace, optionally filtered by item type or root folder.\n" +
			"Requires a workspace UUID. Use this for a workspace or folder inventory of Lakehouses, Notebooks, Reports, and other Fabric items.\n" +
			"Includes nested folders by default; set recursive to false for direct items only.\n" +
			"Returns item IDs, names, types, workspace and folder IDs, other inventory metadata, and available continuation information.\n" +
			"To retrieve another page, pass the returned continuation token with the same filters.\n" +
			"This is the Fabric Core metadata API, not OneLake file/blob listing or cross-workspace catalog search.\n" +
			"Does not read item data, definitions, or default identities, and does not modify resources.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Fabric Items", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type getItemInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID      string `json:"item-id" jsonschema:"The ID of the Microsoft Fabric item to retrieve. Must be a nonempty UUID."`
}

func getItemTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-item",
		Description: "Gets metadata for one existing Microsoft Fabric item, such as a Lakehouse, Notebook, or Report.\n" +
			"Use this to inspect an item when its workspace ID and item ID are known. Both IDs are required UUIDs.\n" +
			"Returns the item ID, display name, description when available, type, and workspace ID.\n" +
			"Does not return item data, definitions, or workload-specific properties, and does not modify the item.\n" +
			"Use search-catalog to discover items when their IDs are unknown.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Item", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type updateItemInput struct {
	WorkspaceID string  `json:"workspace-id" jsonschema:"The nonempty UUID of the Microsoft Fabric workspace containing the item."`
	ItemID      string  `json:"item-id" jsonschema:"The nonempty UUID of the Microsoft Fabric item to update."`
	Description *string `json:"description,omitempty" jsonschema:"The new item description, at most 256 characters. An empty string clears the description; omit to keep it unchanged."`
	DisplayName *string `json:"display-name,omitempty" jsonschema:"The new item display name. Must follow the naming rules for the item's type. Omit to keep the current name."`
}

func updateItemTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "update-item",
		Description: "Updates an existing Fabric item's display name or description and returns its metadata.\n" +
			"Requires workspace-id and item-id UUIDs and at least one of display-name or description.\n" +
			"Omitted properties remain unchanged; an empty description clears it. Descriptions allow at most 256 characters;\n" +
			"display-name rules depend on item type. Does not change item definitions, data, permissions, tags, or identity.\n" +
			"Requires read and write permission on the item. Delegated callers need Item.ReadWrite.All or the corresponding\n" +
			"item-specific scope; service-principal and managed-identity support depends on item type.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Fabric Item", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type moveItemInput struct {
	WorkspaceID    string  `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	ItemID         string  `json:"item-id" jsonschema:"The existing item to move. Must be a nonempty UUID. Fabric moves child items with their parent; a child cannot be moved on its own."`
	TargetFolderID *string `json:"target-folder-id,omitempty" jsonschema:"The existing destination folder UUID in the same workspace. Omit or pass null to move to the workspace root. No destination is created or looked up."`
}

func moveItemTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "move-item",
		Description: "Moves one existing Microsoft Fabric item within the same workspace using explicit workspace and item UUIDs.\n" +
			"Supply target-folder-id for an existing destination folder, or omit it to move to the workspace root.\n" +
			"Fabric moves child items with their parent; a child cannot be moved on its own (CannotMoveChildOnly).\n" +
			"Makes exactly one POST with only optional targetFolderId. Does not discover or move a parent automatically,\n" +
			"look up names, create folders, move across workspaces, copy data, rename, or operate on OneLake or local paths.\n" +
			"Does not retry automatically, poll, or perform verification requests. This is not a long-running operation.\n" +
			"Only HTTP 200 confirms movement. Returns the complete metadata collection in service order, including returned\n" +
			"child items; it may be empty and optional metadata may be absent. Does not infer unrelated dependencies.\n" +
			"Requires Contributor or higher workspace access; delegated callers require Workspace.ReadWrite.All.\n" +
			"Users, service principals, and managed identities use the existing configured authentication.\n" +
			"After an uncertain send, inspect the item separately before another move; never retry blindly.\n" +
			"If movement is confirmed but metadata is unavailable, do not resubmit it.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Move Fabric Item", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type deleteItemInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID      string `json:"item-id" jsonschema:"The ID of the Microsoft Fabric item to delete. Must be a nonempty UUID."`
	HardDelete  *bool  `json:"hard-delete,omitempty" jsonschema:"Explicitly pass true to permanently delete the item; this cannot be recovered and requires workspace Admin. Omit or pass false to use Fabric's default deletion behavior, which soft-deletes only supported item types and does not guarantee recovery. When supplied, this option requires an explicit true or false value."`
}

func deleteItemTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "delete-item",
		Description: "Deletes one Microsoft Fabric item using its required workspace UUID and item UUID.\n" +
			"Omit hard-delete or explicitly pass false to use Fabric's default deletion behavior:\n" +
			"soft deletion only for supported item types, without a guarantee of recovery.\n" +
			"Explicitly pass hard-delete true to permanently delete the item; permanent deletion cannot be recovered.\n" +
			"Soft deletion requires item write permission, and permanent deletion requires workspace Admin.\n" +
			"Delegated callers need Item.ReadWrite.All or the item-specific write scope.\n" +
			"Returns only the requested workspace ID, item ID, and hard-delete mode after a successful response.\n" +
			"Does not resolve names, infer item type, retry, or fall back to permanent deletion.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Delete Fabric Item", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type listFoldersInput struct {
	WorkspaceID       string  `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	ContinuationToken *string `json:"continuation-token,omitempty" jsonschema:"The unchanged continuation token from a previous page. Keep the same workspace, root folder, and recursive options. Omit for the first page. Must be valid Unicode, not blank, without control characters and at most 8,192 UTF-8 bytes. Do not supply a continuation URL."`
	Recursive         *bool   `json:"recursive,omitempty" jsonschema:"Include nested logical workspace folders. Omission uses the API default of true; false lists only direct children. This never performs client-side folder walking."`
	RootFolderID      *string `json:"root-folder-id,omitempty" jsonschema:"An existing root folder ID in the workspace. Must be a nonempty UUID. Omit to use the workspace root."`
}

func listFoldersTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "list-folders",
		Description: "Lists one complete page of logical Microsoft Fabric workspace folders for an explicit workspace UUID.\n" +
			"Optionally select a root folder UUID and recursive true or false. The API includes nested folders by default.\n" +
			"Returns folder IDs, display names, workspace IDs, parent folder IDs when available, and any continuation token\n" +
			"and Retry-After guidance. Parents may be outside the current page; no paths are reconstructed.\n" +
			"Call again with the unchanged token and the same workspace, root folder, and recursive options for another page.\n" +
			"Makes exactly one GET, without folder walking, lookups, enrichment, automatic pagination, retries, or polling.\n" +
			"This is workspace organization metadata, not OneLake directories, local files, or item contents.\n" +
			"Requires workspace Viewer or higher access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Fabric Folders", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type getFolderInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace containing the folder. Must be a nonempty UUID; names are not resolved."`
	FolderID    string `json:"folder-id" jsonschema:"The ID of the logical workspace folder to retrieve. Must be a nonempty UUID, not a folder name, OneLake directory, or local path."`
}

func getFolderTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-folder",
		Description: "Gets metadata for one logical Microsoft Fabric workspace folder using explicit workspace and folder UUIDs.\n" +
			"Returns its ID, display name, workspace ID, and parent folder ID when available, plus Retry-After guidance.\n" +
			"Makes exactly one GET without listing folders, looking up names or ancestors, enumerating children or items,\n" +
			"reconstructing paths, enriching metadata, retrying, polling, or modifying resources.\n" +
			"These are logical workspace folders, not OneLake directories, local paths, or item contents.\n" +
			"Requires workspace Viewer or higher access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Folder", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type createFolderInput struct {
	WorkspaceID    string  `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	DisplayName    string  `json:"display-name" jsonschema:"The name of one logical workspace folder, not a filesystem path. Must meet Fabric folder naming rules: at most 255 characters, no leading/trailing spaces, control codes, prohibited characters, or reserved names. The value is not trimmed or sanitized."`
	ParentFolderID *string `json:"parent-folder-id,omitempty" jsonschema:"An existing parent folder ID in the workspace. Must be a nonempty UUID. Omit or supply null to create at the workspace root; no parent lookup or creation is performed."`
}

func createFolderTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "create-folder",
		Description: "Creates one logical Microsoft Fabric workspace folder for an explicit workspace UUID and display name.\n" +
			"Optionally supply an existing parent-folder-id UUID; omission or null creates at the workspace root.\n" +
			"Names must meet Fabric folder naming rules and are never trimmed or sanitized.\n" +
			"This is workspace organization, not a OneLake directory or local filesystem path.\n" +
			"Makes exactly one POST, without parent/name lookup, duplicate checks, recursive creation, get-or-create,\n" +
			"automatic retry, follow-up reads, or operation polling. A duplicate-name error remains a failure.\n" +
			"HTTP 201 confirms creation and returns folder ID, display name, workspace ID, and parent ID when available.\n" +
			"Requires Contributor or higher workspace access; delegated callers require Workspace.ReadWrite.All.\n" +
			"Users, service principals, and managed identities use the existing configured authentication.\n" +
			"After an uncertain send, verify the workspace separately before any new creation; never resubmit blindly.\n" +
			"If creation is confirmed but metadata is unavailable, do not repeat the creation request.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Fabric Folder", ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type updateFolderInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	FolderID    string `json:"folder-id" jsonschema:"The existing logical workspace folder ID. Must be a nonempty UUID, not a name or filesystem path."`
	DisplayName string `json:"display-name" jsonschema:"The required replacement folder display name. Must meet Fabric folder naming rules: 1 to 255 characters, no leading/trailing spaces, control codes, prohibited characters, or reserved names. The value is not trimmed or sanitized."`
}

func updateFolderTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "update-folder",
		Description: "Renames one existing logical Microsoft Fabric workspace folder using explicit workspace and folder UUIDs.\n" +
			"Requires display-name, following Fabric folder naming rules; the value is never trimmed or sanitized.\n" +
			"Makes exactly one PATCH with only displayName. Does not move the folder, modify items or permissions,\n" +
			"create OneLake or local directories, resolve names, check duplicates, fetch or merge existing metadata,\n" +
			"retry automatically, or poll. A duplicate-name error remains a failure.\n" +
			"HTTP 200 confirms the update and returns the folder's metadata snapshot, including its parent ID when available.\n" +
			"Requires Contributor or higher workspace access; delegated callers require Workspace.ReadWrite.All.\n" +
			"Users, service principals, and managed identities use the existing configured authentication.\n" +
			"After an uncertain send, inspect the folder separately before another update; never retry blindly.\n" +
			"If the update is confirmed but metadata is unavailable, do not resubmit it.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Fabric Folder", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type moveFolderInput struct {
	WorkspaceID    string  `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	FolderID       string  `json:"folder-id" jsonschema:"The existing logical workspace folder to move. Must be a nonempty UUID, not a name or filesystem path."`
	TargetFolderID *string `json:"target-folder-id,omitempty" jsonschema:"The existing destination folder UUID in the same workspace. Omit or pass null to move to the workspace root. Cannot be the folder being moved; Fabric validates descendant, depth, and name conflicts."`
}

func moveFolderTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "move-folder",
		Description: "Moves one existing logical Microsoft Fabric folder within the same workspace using explicit workspace and folder UUIDs.\n" +
			"Supply target-folder-id for an existing destination folder, or omit it to move to the workspace root.\n" +
			"Fabric moves the folder hierarchy, including its contained folders and items, server-side.\n" +
			"Makes exactly one POST with only optional targetFolderId, without enumerating descendants or moving items individually.\n" +
			"Does not rename, move across workspaces, create destinations, resolve names or paths, inspect ancestors, check conflicts,\n" +
			"modify OneLake or local directories, retry automatically, or poll. Fabric enforces hierarchy, depth, and name constraints.\n" +
			"Only HTTP 200 confirms movement and returns the folder's metadata snapshot, including its parent ID when available.\n" +
			"Requires Contributor or higher workspace access; delegated callers require Workspace.ReadWrite.All.\n" +
			"Users, service principals, and managed identities use the existing configured authentication.\n" +
			"After an uncertain send, inspect the folder separately before another move; never retry blindly.\n" +
			"If movement is confirmed but metadata is unavailable, do not resubmit it.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Move Fabric Folder", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type deleteFolderInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace containing the folder. Must be a nonempty UUID; names are not resolved."`
	FolderID    string `json:"folder-id" jsonschema:"The ID of the empty logical workspace folder to delete. Must be a nonempty UUID, not a folder name, OneLake directory, or local path."`
}

func deleteFolderTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "delete-folder",
		Description: "Deletes one empty logical Microsoft Fabric workspace folder using explicit workspace and folder UUIDs.\n" +
			"Requires normal destructive-operation consent. Fabric rejects folders containing any items or nested folders\n" +
			"with FolderNotEmpty; soft-deleted items can also prevent deletion. Never deletes or moves folder contents.\n" +
			"Makes exactly one DELETE without a request body, query flags, Get/List or emptiness preflight, name/path lookup,\n" +
			"descendant enumeration, automatic emptying, retry, or follow-up verification.\n" +
			"Only HTTP 200 confirms deletion and returns deletionStatus Deleted with the requested workspace and folder IDs,\n" +
			"not a folder snapshot. This is not an LRO: never follows Location, polls, or retrieves operation results.\n" +
			"These are workspace organizational folders, not OneLake directories or local paths.\n" +
			"Requires Contributor or higher workspace access; delegated callers require Workspace.ReadWrite.All.\n" +
			"Users, service principals, and managed identities use the existing configured authentication.\n" +
			"No recursive, force, or hard-delete option exists. The API does not document folder recovery or a retention period.\n" +
			"A 404 or FolderNotFound remains a failure, even after an earlier deletion.\n" +
			"Canceling the MCP request does not undo deletion. After an uncertain send, inspect the folder separately\n" +
			"with Get or List Folders before considering another deletion; never retry blindly.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Delete Fabric Folder", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type getOperationStateInput struct {
	OperationID string `json:"operation-id" jsonschema:"The nonempty operation UUID returned by the initiating Fabric API. URLs and resource names are not accepted."`
}

func getOperationStateTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-operation-state",
		Description: "Reads the state of one Fabric long-running operation by operation-id UUID in a single request.\n" +
			"Returns the status, optional progress/timestamps, safe failure code/request ID, and polling guidance.\n" +
			"A successful state read can report a Failed operation; unknown future statuses are not completion.\n" +
			"After Succeeded, use get-operation-result only if the initiating API produces a result.\n" +
			"Requires the same identity, resource permissions, and delegated scopes as the initiating API.\n" +
			"Does not poll, retry, cancel operations, follow response URLs, or resubmit the original mutation.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Operation State", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type getOperationResultInput struct {
	OperationID string `json:"operation-id" jsonschema:"The nonempty operation UUID returned by the initiating Fabric API. URLs and resource names are not accepted."`
}

func getOperationResultTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-operation-result",
		Description: "Reads the result of one completed Fabric long-running operation by operation-id UUID in a single request.\n" +
			"Returns a JSON envelope with operationId, hasBody, and value; JSON null differs from an empty body.\n" +
			"Only JSON and empty results are supported, up to 1 MiB (1,048,576 bytes). Binary results are unsupported.\n" +
			"Use get-operation-state first to confirm Succeeded. Not every initiating API produces a result;\n" +
			"a missing result or HTTP 404 does not establish successful no-result completion.\n" +
			"Requires the same identity, resource permissions, and delegated scopes as the initiating API.\n" +
			"Does not poll, retry, follow response URLs, download binary data, or resubmit the original mutation.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Operation Result", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type createItemInput struct {
	DisplayName              string   `json:"display-name" jsonschema:"The display name for the item, following the naming rules for its item type."`
	ItemType                 string   `json:"item-type" jsonschema:"The type of the Fabric item (e.g., Lakehouse, Notebook, etc.)."`
	CreationPayload          *string  `json:"creation-payload,omitempty" jsonschema:"Item-specific creation properties as a JSON object string, up to 1 MiB (1048576 UTF-8 bytes) and 32 nesting levels. Mutually exclusive with every inline or saved-file definition option. Fabric validates supported properties for the item type. Not a path or a complete create-request body."`
	Definition               *string  `json:"definition,omitempty" jsonschema:"Inline definition JSON as a string: {\"parts\":[{\"path\":\"logical/path\",\"payload\":\"base64\",\"payloadType\":\"InlineBase64\"}],\"format\":\"optional format\"}. Supported in local and HTTP modes. Mutually exclusive with every definition-directory/files/format option. Supply only the definition envelope, not a file path or an API request body."`
	DefinitionDirectory      *string  `json:"definition-directory,omitempty" jsonschema:"Existing absolute local item directory containing saved definition files. Local execution only; unavailable over HTTP. Requires definition-files; never scans the folder automatically. Mutually exclusive with inline definition."`
	DefinitionFiles          *string  `json:"definition-files,omitempty" jsonschema:"Explicit file selection as a JSON array string: [{\"partPath\":\"artifact.content.ipynb\",\"sourcePath\":\"sales.ipynb\"}]. Paths are relative to definition-directory; sourcePath defaults to partPath. Logical part paths use '/'. No wildcards or automatic enumeration. Local execution only."`
	DefinitionFormat         *string  `json:"definition-format,omitempty" jsonschema:"Optional Fabric definition format for saved-file input only, for example ipynb, FabricGitSource, TMDL, TMSL, PBIR, or PBIR-Legacy. Requires definition-directory and definition-files. For inline input, put format inside definition JSON. Format and content are forwarded unchanged after generic validation; Fabric validates format support and item-specific layouts."`
	Description              *string  `json:"description,omitempty" jsonschema:"The description for the item, at most 256 characters."`
	EarlyPoll                *bool    `json:"early-poll,omitempty" jsonschema:"Allow one early status probe after at most 3 seconds, even if the initial 202 Retry-After is longer. Defaults to true; false honors the initial hint. Later polls and 429/503 retries honor validated hints."`
	FolderID                 *string  `json:"folder-id,omitempty" jsonschema:"Optional nonempty UUID of an existing Fabric folder in the workspace. Passed directly without looking up or creating folders. Omit to use the workspace root."`
	MaxWaitSeconds           *float64 `json:"max-wait-seconds,omitempty" jsonschema:"Finite positive local wait budget in seconds after an accepted response when sync is true. Defaults to 120; the server rejects values above 600 (10 minutes), without clamping. Fractional seconds are allowed. Expiry returns a resumable receipt and does not cancel Fabric's operation."`
	Options                  *string  `json:"options,omitempty" jsonschema:"The API's top-level item-specific options as a JSON object string, up to 1 MiB (1048576 UTF-8 bytes) and 32 nesting levels. Requires an inline or saved-file definition source. Consult the item's Update Definition API for supported options. These are not LRO wait settings; use sync, max-wait-seconds, and early-poll for waiting."`
	SensitivityLabelSettings *string  `json:"sensitivity-label-settings,omitempty" jsonschema:"Optional label settings as a JSON object string, up to 4096 UTF-8 bytes: {\"labelId\":\"nonempty UUID\",\"sensitivityLabelApplyStrategy\":\"ApplyOrFail\"}. When supplied, labelId is required and must be a nonempty UUID; the strategy is optional and stays omitted when not supplied. A supplied strategy must be ApplyOrFail or Ignore. Fabric defaults an omitted strategy to ApplyOrFail. Ignore explicitly permits creation without the label if it cannot be applied; the tool never downgrades policy or retries without it."`
	Sync                     *bool    `json:"sync,omitempty" jsonschema:"Wait asynchronously for accepted creation to complete. Defaults to false, returning an operation receipt without polling."`
	Workspace                *string  `json:"workspace,omitempty" jsonschema:"Backward-compatible alias for the workspace UUID, used when workspace-id is omitted or blank. Workspace names are not supported."`
	WorkspaceID              *string  `json:"workspace-id,omitempty" jsonschema:"The nonempty UUID of the Microsoft Fabric workspace. Workspace names are not resolved. Takes precedence over workspace unless omitted, empty, or whitespace."`
}

func createItemTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "create-item",
		Description: "Creates a Fabric item using a nonempty workspace UUID, display-name, and item-type.\n" +
			"Supply workspace-id or the backward-compatible workspace UUID alias; workspace names are not resolved.\n" +
			"Optional folder-id is an existing folder UUID, passed without lookup or folder creation.\n" +
			"Supply bounded item-specific creation-payload JSON or a definition, never both.\n" +
			"Optionally supply inline definition JSON, or in local execution a definition-directory and explicit\n" +
			"definition-files mapping with optional definition-format. Never combine these sources.\n" +
			"File input reads saved, explicitly selected files only; HTTP callers must supply inline content.\n" +
			"Definitions support InlineBase64 parts, portable paths, and bounded byte/count budgets.\n" +
			"Definition format, safe part names, and content are forwarded unchanged after generic validation.\n" +
			"Fabric validates item-specific layouts, required files, supported formats, content semantics, and dependencies.\n" +
			"Top-level options JSON requires a definition and contains item-specific settings, not LRO wait settings.\n" +
			"Optional sensitivity-label-settings JSON requires a nonempty labelId UUID and accepts optional sensitivityLabelApplyStrategy.\n" +
			"Omitted label settings or strategy stay omitted; Fabric defaults the strategy to ApplyOrFail.\n" +
			"Ignore is explicit opt-in to proceed without the label if it cannot be applied; no policy downgrade or retry is performed.\n" +
			"Sends one creation request. A 201 response returns item metadata; 202 returns a resumable operation receipt.\n" +
			"With sync=true, asynchronously wait up to max-wait-seconds (default 120, server maximum 600 seconds)\n" +
			"after acceptance and retrieve the result if available. Both modes immediately return synchronous completion without polling.\n" +
			"early-poll=true permits one probe after at most 3 seconds instead of the initial 202 Retry-After;\n" +
			"false honors that hint. Later polls and 429/503 retries honor validated hints.\n" +
			"Wait expiry returns last-known state, not a failed mutation; client cancellation can stop waiting sooner.\n" +
			"Description is limited to 256 characters; naming rules depend on item type.\n" +
			"Requires Contributor or higher workspace access and, for delegated callers, Item.ReadWrite.All or\n" +
			"the item-specific ReadWrite.All scope. Non-Power BI items require a supported Fabric capacity and\n" +
			"tenant/capacity settings that enable Fabric item creation; Power BI items require the appropriate license.\n" +
			"Service-principal and managed-identity support depends on item type.\n" +
			"Does not create dependencies, cancel Fabric operations, or resubmit creation.\n" +
			"Only JSON/empty operation responses up to 1 MiB are supported; created item metadata is required.\n" +
			"Acceptance is not completion. Resume using get-operation-state and get-operation-result, not another create.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Fabric Item", ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type searchCatalogInput struct {
	ContinuationToken *string `json:"continuation-token,omitempty" jsonschema:"The unchanged token returned by the previous page. Carries the original search, filter, and page size; may be sent alone and must not accompany search or filter."`
	Filter            *string `json:"filter,omitempty" jsonschema:"The filter for the search. Supports filtering by type of entries. Supported operators: eq (Equals), ne (Not Equals), or (Logical OR), () (Parentheses for grouping). Example: \"Type eq 'Report' or Type eq 'Lakehouse'\". Supported item types include Report, Lakehouse, Notebook, Warehouse, SemanticModel, KQLDatabase, and DataPipeline; for the full list see the Catalog Search API reference at https://learn.microsoft.com/rest/api/fabric/core/catalog/search. Omit when using continuation-token."`
	PageSize          *int    `json:"page-size,omitempty" jsonschema:"The page size that needs to be returned. Must be between 1 and 1000."`
	Search            *string `json:"search,omitempty" jsonschema:"The text query for the search. Supports searching across display name, description and workspace name of the catalog entry. Omit when using continuation-token."`
}

func searchCatalogTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "search-catalog",
		Description: "Searches one page of Microsoft Fabric OneLake catalog metadata across workspaces, limited to entries\n" +
			"the calling principal can access. Find Fabric items by name, description, or workspace name, optionally\n" +
			"filtering by item type. Returns typed entries with available workspace hierarchy and a continuation token.\n" +
			"For the next page, send the returned continuation-token without search or filter: the token already\n" +
			"carries the original search, filter, and page size. An explicitly supplied page-size must be 1 through 1000.\n" +
			"Does not fetch all pages, read item data, or retry automatically. Delegated callers need Catalog.Read.All.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Search Catalog", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type getItemDefinitionInput struct {
	EarlyPoll       *bool    `json:"early-poll,omitempty" jsonschema:"Allow one initial status probe after at most 3 seconds, even when the initial Retry-After is longer. Defaults to true. False honors the initial hint; subsequent polling and 429/503 backoff honor validated hints."`
	Format          *string  `json:"format,omitempty" jsonschema:"Optional definition format, such as ipynb or FabricGitSource. Omit to use the item's default. Must not be empty or whitespace, must be at most 128 characters, and contain no control characters. Passed as one encoded query value; Fabric validates support. Cannot be combined with operation-id."`
	ItemID          *string  `json:"item-id,omitempty" jsonschema:"The nonempty UUID of the Fabric item whose definition is requested. Required with workspace-id for an initial request; cannot be combined with operation-id. Names are not resolved."`
	MaxWaitSeconds  *float64 `json:"max-wait-seconds,omitempty" jsonschema:"Finite positive local wait budget after acceptance when sync=true. Defaults to 120 seconds; maximum 600 seconds. Fractional values are allowed. Expiry returns the operation ID without saving files or canceling the Fabric operation."`
	OperationID     *string  `json:"operation-id,omitempty" jsonschema:"Nonempty UUID from an earlier Get Item Definition receipt. After get-operation-state reports Succeeded, retrieve this definition result once with one GET and no POST or polling. The result does not identify its item or prove its provenance. Cannot be combined with workspace-id, item-id, format, or sync=true."`
	OutputDirectory *string  `json:"output-directory,omitempty" jsonschema:"Optional new absolute local directory for the completed definition's decoded files. Its parent must exist. Never overwrites or merges a destination. Local execution only; unavailable over HTTP. Omit to return only inline definition content without writing files."`
	Sync            *bool    `json:"sync,omitempty" jsonschema:"Wait for accepted definition retrieval, poll its state, and retrieve its result within max-wait-seconds. Defaults to false, returning an operation receipt without polling. Cannot be combined with operation-id."`
	WorkspaceID     *string  `json:"workspace-id,omitempty" jsonschema:"The nonempty UUID of the Fabric workspace containing the item. Required with item-id for an initial request; cannot be combined with operation-id. Names are not resolved."`
}

func getItemDefinitionTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-item-definition",
		Description: "Gets a Fabric item's typed definition using explicit nonempty workspace-id and item-id UUIDs.\n" +
			"Optional format is sent to Fabric without a metadata/name lookup. Sends one POST to getDefinition.\n" +
			"A 200 response returns definition.parts (path, payload, payloadType) and any returned format.\n" +
			"A 202 response is a resumable operation receipt, not a definition or a saved directory.\n" +
			"With sync=true, poll and retrieve the result within max-wait-seconds (default 120, maximum 600);\n" +
			"early-poll permits one probe after at most 3 seconds, then validated Retry-After hints are honored.\n" +
			"Wait expiry or caller cancellation stops local waiting, not Fabric's operation; no background polling remains.\n" +
			"Resume with get-operation-state, then when Succeeded use this tool with operation-id alone\n" +
			"to retrieve that result once with one GET and no POST or polling.\n" +
			"Do not combine operation-id with workspace-id, item-id, format, or sync=true.\n" +
			"The operation result does not contain item identity or prove which item it came from.\n" +
			"Inline retrieval works locally and over HTTP and never creates unsolicited files.\n" +
			"Optional output-directory exports completed parts using the existing definition file service.\n" +
			"This requires local execution and a new absolute directory below an existing parent; it is unavailable over HTTP.\n" +
			"Export preserves binary bytes, encodings, relative paths, empty files, and .platform, without merging or overwriting.\n" +
			"Export failures are errors, never partial success or silent inline fallback.\n" +
			"Because optional export writes files, this tool is excluded by read-only server filtering in every transport.\n" +
			"Supports JSON responses up to 64 MiB, InlineBase64 parts, and the host's generic path/byte/count limits\n" +
			"(defaults: 1024 parts, 16 MiB decoded per part, 32 MiB decoded total). Oversized content is rejected, not truncated.\n" +
			"No per-part downloads, schema downloads, item-specific layout validation, dependency lookups, or item updates.\n" +
			"Requires read AND write item permissions and Item.ReadWrite.All or item-specific ReadWrite.All delegated scope.\n" +
			"Service-principal and managed-identity support depends on item type. Sensitivity labels are not in definitions;\n" +
			"encrypted labels require the caller's applicable usage rights. Treat returned definition content as sensitive.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Item Definition", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
		Meta: mcp.Meta{server.SecretHint: true},
	}
}

type updateItemDefinitionInput struct {
	WorkspaceID         string   `json:"workspace-id" jsonschema:"The nonempty UUID of the existing Fabric workspace. Workspace names are not resolved."`
	ItemID              string   `json:"item-id" jsonschema:"The nonempty UUID of the existing Fabric item whose entire definition will be replaced. Item names are not resolved."`
	Definition          *string  `json:"definition,omitempty" jsonschema:"Inline definition JSON as a string: {\"parts\":[{\"path\":\"logical/path\",\"payload\":\"base64\",\"payloadType\":\"InlineBase64\"}],\"format\":\"optional format\"}. Supported in local and HTTP modes. Mutually exclusive with every definition-directory/files/format option. Supply only the definition envelope, not a file path or an API request body."`
	DefinitionDirectory *string  `json:"definition-directory,omitempty" jsonschema:"Existing absolute local item directory containing saved definition files. Local execution only; unavailable over HTTP. Requires definition-files; never scans the folder automatically. Mutually exclusive with inline definition."`
	DefinitionFiles     *string  `json:"definition-files,omitempty" jsonschema:"Explicit file selection as a JSON array string: [{\"partPath\":\"artifact.content.ipynb\",\"sourcePath\":\"sales.ipynb\"}]. Paths are relative to definition-directory; sourcePath defaults to partPath. Logical part paths use '/'. No wildcards or automatic enumeration. Local execution only."`
	DefinitionFormat    *string  `json:"definition-format,omitempty" jsonschema:"Optional Fabric definition format for saved-file input only, for example ipynb, FabricGitSource, TMDL, TMSL, PBIR, or PBIR-Legacy. Requires definition-directory and definition-files. For inline input, put format inside definition JSON. Format and content are forwarded unchanged after generic validation; Fabric validates format support and item-specific layouts."`
	EarlyPoll           *bool    `json:"early-poll,omitempty" jsonschema:"Allow one early status probe after at most 3 seconds, even if the initial 202 Retry-After is longer. Defaults to true; false honors the initial hint. Later polls and 429/503 retries honor validated hints."`
	MaxWaitSeconds      *float64 `json:"max-wait-seconds,omitempty" jsonschema:"Finite positive local wait budget in seconds after acceptance when sync is true. Defaults to 120; maximum 600 (10 minutes). Fractional seconds are allowed. Expiry returns a resumable receipt and does not cancel Fabric's operation."`
	Options             *string  `json:"options,omitempty" jsonschema:"The API's top-level item-specific options as a JSON object string, up to 1 MiB (1048576 UTF-8 bytes) and 32 nesting levels, with no duplicate properties. Consult the actual item type's Update Definition API for supported options. Not LRO wait settings; use sync, max-wait-seconds, and early-poll for waiting."`
	Sync                *bool    `json:"sync,omitempty" jsonschema:"Wait asynchronously for accepted definition replacement to complete. Defaults to false, returning an operation receipt without polling. This is a no-result API: only operation state is polled, never /result."`
	UpdateMetadata      *bool    `json:"update-metadata,omitempty" jsonschema:"Explicit true or false. Only true requests metadata updates from .platform when that part is supplied. Omit to leave the API query parameter omitted; false is sent explicitly. The tool never adds, removes, or rewrites .platform and this API never changes the item's sensitivity label."`
}

func updateItemDefinitionTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "update-item-definition",
		Description: "Replaces the entire definition of an existing Fabric item using known nonempty workspace-id and item-id UUIDs.\n" +
			"Requires exactly one complete definition source: inline definition JSON, or an existing local\n" +
			"definition-directory with an explicit definition-files mapping and optional definition-format.\n" +
			"Saved-file input reads only selected saved files on the hosting machine; HTTP callers must use inline content.\n" +
			"This is not a changed-files patch: it never gets the old definition, merges parts, or resolves names.\n" +
			"Shared validation checks InlineBase64 payloads, portable paths, and bounded byte/count budgets.\n" +
			"Definition format, safe part layout, and payload bytes pass through unchanged, without item-type or format-specific rules.\n" +
			"Fabric validates required parts, supported formats, item-type requirements, content, dependencies, and item-specific options.\n" +
			"Optional top-level options is bounded JSON for the item's Update Definition API, not LRO wait settings.\n" +
			"update-metadata must be explicitly true to request metadata updates from a supplied .platform part.\n" +
			"Omission leaves that query parameter omitted. .platform is never added, removed, or rewritten.\n" +
			"Updating a definition does not affect the sensitivity label; no label, folder, or creation properties are accepted.\n" +
			"Sends one POST. A synchronous 200 returns a Succeeded receipt with no item/result body.\n" +
			"A 202 returns a resumable operation receipt. With sync=true, asynchronously poll state for up to\n" +
			"max-wait-seconds (default 120, maximum 600); confirmed Succeeded completes without a /result or Get Item call.\n" +
			"early-poll=true permits one probe after at most 3 seconds instead of the initial Retry-After.\n" +
			"Later polls and 429/503 read retries honor validated hints. Wait expiry preserves last-known state.\n" +
			"Client cancellation stops only the local wait, not the Fabric operation.\n" +
			"Resume pending updates with get-operation-state, never by resubmitting the update or requesting a result.\n" +
			"Requires read and write item permissions and, for delegated callers, Item.ReadWrite.All or the\n" +
			"item-specific ReadWrite.All scope. Service-principal and managed-identity support depends on item type.\n" +
			"Does not perform preflight reads, download schemas, create dependencies, or follow arbitrary URLs.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Fabric Item Definition", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type listWorkspaceRoleAssignmentsInput struct {
	WorkspaceID       string  `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	ContinuationToken *string `json:"continuation-token,omitempty" jsonschema:"The unchanged continuation token from a previous role-assignment page for the same workspace. Omit for the first page. Must be valid Unicode, not blank, without control characters and at most 8,192 UTF-8 bytes. Do not supply a continuation URL."`
}

func listWorkspaceRoleAssignmentsTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "list-workspace-role-assignments",
		Description: "Lists one complete page of Microsoft Fabric workspace role assignments for an explicit workspace UUID.\n" +
			"Returns assignment IDs, roles, principal IDs and types, available display names and principal details,\n" +
			"and any continuation token and Retry-After guidance. Preserves future role and principal type strings.\n" +
			"Call again with the unchanged token and the same workspace for another page, even after an empty page.\n" +
			"Makes exactly one GET, without directory or Graph lookups, group-membership expansion, identity enrichment,\n" +
			"per-row requests, automatic pagination, retries, or polling. Does not calculate effective permissions or change roles.\n" +
			"Requires workspace Member or higher access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All.\n" +
			"Supports users, service principals, and managed identities through the configured Fabric authentication.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Fabric Workspace Role Assignments", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type getWorkspaceRoleAssignmentInput struct {
	WorkspaceID               string `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	WorkspaceRoleAssignmentID string `json:"workspace-role-assignment-id" jsonschema:"The workspace role assignment ID. Must be a nonempty UUID. This identifies the assignment, not its principal; no lookup or ID translation is performed."`
}

func getWorkspaceRoleAssignmentTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-workspace-role-assignment",
		Description: "Gets one Microsoft Fabric workspace role assignment using explicit workspace and assignment UUIDs.\n" +
			"Returns the assignment ID, role, principal ID and type, available display name and principal details,\n" +
			"and Retry-After guidance. Preserves future role and principal type strings. Assignment and principal IDs are independent.\n" +
			"Makes exactly one GET, without listing assignments, translating principal IDs, directory or Graph lookups,\n" +
			"group-membership expansion, identity enrichment, retries, or polling. Does not calculate effective permissions or change roles.\n" +
			"Requires workspace Member or higher access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All.\n" +
			"Supports users, service principals, and managed identities through the configured Fabric authentication.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Workspace Role Assignment", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type addWorkspaceRoleAssignmentInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	Principal   string `json:"principal" jsonschema:"Explicit principal JSON object with a nonempty id UUID and type: User, Group, ServicePrincipal, ServicePrincipalProfile, or EntireTenant. Supply the principal ID for that type: an Entra object ID for a user, group or service principal, or the profile ID for a service principal profile; not an application/client ID, email, name, or assignment ID. EntireTenant explicitly targets all tenant users and still requires its principal UUID. Optional writable groupDetails.groupType and servicePrincipalProfileDetails.parentPrincipal are supported for their respective types. Read-only displayName, userDetails and servicePrincipalDetails are rejected. Extensible type strings are forwarded unchanged; no identity is inferred or looked up."`
	Role        string `json:"role" jsonschema:"Explicit workspace role: Admin, Member, Contributor, or Viewer. No default, trimming, upgrade, or fallback. Extensible API role strings are sent unchanged. Members can grant Member or lower; granting Admin requires an Admin caller. Fabric enforces authorization."`
}

func addWorkspaceRoleAssignmentTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "add-workspace-role-assignment",
		Description: "Adds one Microsoft Fabric workspace role assignment using explicit workspace-id, principal JSON, and role.\n" +
			"The principal requires an id UUID and type; no name, email, application ID, group expansion, or identity lookup is performed.\n" +
			"User, Group, ServicePrincipal, ServicePrincipalProfile, and EntireTenant are documented principal types.\n" +
			"EntireTenant explicitly grants access to all tenant users. Admin, Member, Contributor, and Viewer are documented roles.\n" +
			"Extensible type and role strings are forwarded unchanged; no default, upgrade, broadening, or permission fallback is chosen.\n" +
			"Requires workspace Member or higher access; delegated calls require Workspace.ReadWrite.All. Members can grant Member or lower;\n" +
			"granting Admin requires Admin access. Fabric enforces permissions and its 1,000 users/groups per workspace limit.\n" +
			"Users, service principals, and managed identities use the configured Fabric authentication.\n" +
			"Makes exactly one POST, without preflight List/Get, Graph calls, get-or-create, conflict updates, retries, or polling.\n" +
			"Only HTTP 201 confirms creation. Returns the new assignment and available principal metadata; assignment and principal IDs are independent.\n" +
			"After an uncertain send inspect assignments separately, never retry blindly. If creation is confirmed but metadata is unavailable,\n" +
			"do not resubmit the grant. This tool is an additive permission mutation, not a read or a way to update an existing assignment.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Add Fabric Workspace Role Assignment", ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type updateWorkspaceRoleAssignmentInput struct {
	WorkspaceID               string `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	WorkspaceRoleAssignmentID string `json:"workspace-role-assignment-id" jsonschema:"The existing workspace role assignment ID. Must be a nonempty UUID, independent of the principal ID; no lookup or ID translation is performed."`
	Role                      string `json:"role" jsonschema:"The explicit replacement workspace role: Admin, Member, Contributor, or Viewer. Future nonempty role strings are passed unchanged for Fabric to validate. No default, trimming, upgrade, or fallback is selected."`
}

func updateWorkspaceRoleAssignmentTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "update-workspace-role-assignment",
		Description: "Updates one existing Microsoft Fabric workspace role assignment using explicit workspace-id, workspace-role-assignment-id, and role.\n" +
			"Assignment and principal IDs are independent; supply the existing assignment UUID, not a principal name or an ID to translate.\n" +
			"Admin, Member, Contributor, and Viewer are documented roles. Future nonempty role strings are forwarded unchanged.\n" +
			"No default, trimming, upgrade, broadening, or permission fallback is selected. This can increase or reduce existing workspace access.\n" +
			"Requires workspace Admin access; delegated calls require Workspace.ReadWrite.All. The last admin's assignment cannot be changed.\n" +
			"Users, service principals, and managed identities use the configured Fabric authentication; Fabric enforces permissions.\n" +
			"Makes exactly one PATCH with only role. No principal payload, preflight List/Get, directory/Graph calls, membership expansion,\n" +
			"enrichment, create/upsert fallback, automatic conflict update, retry, polling, or follow-up verification is performed.\n" +
			"Only HTTP 200 confirms update, returning matching assignment and role metadata with available principal details.\n" +
			"After an uncertain send inspect the assignment separately; never retry blindly. If update is confirmed but metadata is unavailable,\n" +
			"do not resubmit it. This is a permission-changing mutation, not a read or a way to create an assignment.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Fabric Workspace Role Assignment", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type deleteWorkspaceRoleAssignmentInput struct {
	WorkspaceID               string `json:"workspace-id" jsonschema:"The Microsoft Fabric workspace ID. Must be a nonempty UUID; names are not resolved."`
	WorkspaceRoleAssignmentID string `json:"workspace-role-assignment-id" jsonschema:"The existing workspace role assignment ID to delete. Must be a nonempty UUID, independent of the principal ID; no lookup or ID translation is performed."`
}

func deleteWorkspaceRoleAssignmentTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "delete-workspace-role-assignment",
		Description: "Deletes one existing Microsoft Fabric workspace role assignment using explicit workspace-id and workspace-role-assignment-id.\n" +
			"Assignment and principal IDs are independent; supply the existing assignment UUID, not a principal name or an ID to translate.\n" +
			"Removes only that assignment, not the principal, group membership, workspace, or other assignments.\n" +
			"Does not establish that the principal has no remaining effective access.\n" +
			"Requires workspace Admin access; delegated calls require Workspace.ReadWrite.All. The last admin's assignment cannot be deleted.\n" +
			"Users, service principals, and managed identities use the configured Fabric authentication; Fabric enforces permissions.\n" +
			"Uses normal destructive-operation confirmation. Makes exactly one DELETE without a request body or query flags.\n" +
			"No List/Get preflight, directory/Graph calls, name resolution, identity translation, membership expansion,\n" +
			"cascade, bulk removal, fallback, automatic retry, or follow-up verification is performed.\n" +
			"Only HTTP 200 confirms deletion and returns deletionStatus Deleted with the requested workspace and assignment IDs,\n" +
			"not an assignment snapshot or an effective-access assessment. A 404 remains a failure, even after an earlier deletion.\n" +
			"This is not a documented LRO: never follows Location, polls, or retrieves operation results.\n" +
			"Canceling the MCP request does not undo deletion. After an uncertain send inspect the assignment separately;\n" +
			"never retry blindly or resubmit confirmed deletion. Retry-After timing is not permission to replay the deletion.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Delete Fabric Workspace Role Assignment", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}
