package onelake

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// registerShortcutCreates ports the eight ShortcutCreate*Command classes,
// one per target type. Every one of them builds an OneLakeShortcut around a
// single populated ShortcutTarget field and POSTs it through
// IOneLakeService.CreateShortcutAsync, so they share their HTTP, response
// and error handling through the single createShortcut helper below; each
// registration here supplies only what upstream's own Options classes vary:
// the tool's exact name/title/description/parameters and the closure that
// builds its target's ShortcutTarget variant.
func (a *Area) registerShortcutCreates(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "create-shortcut-onelake",
		Description: "Create a shortcut pointing to another OneLake location. Specify the target\n" +
			"workspace, item, and optional path within the target item. Requires\n" +
			"OneLake.ReadWrite.All.",
		Annotations: write("Create OneLake Shortcut (OneLake Target)", false, true),
	}, a.createShortcutOneLake)

	server.AddTool(r, &mcp.Tool{
		Name: "create-shortcut-adls-gen2",
		Description: "Create a shortcut pointing to an Azure Data Lake Storage Gen2 location.\n" +
			"Requires a connection ID for authentication and a target URL. Requires\n" +
			"OneLake.ReadWrite.All.",
		Annotations: write("Create OneLake Shortcut (ADLS Gen2 Target)", false, true),
	}, a.createShortcutAdlsGen2)

	server.AddTool(r, &mcp.Tool{
		Name: "create-shortcut-amazon-s3",
		Description: "Create a shortcut pointing to an Amazon S3 location. Requires a connection\n" +
			"ID for authentication and a target URL. Requires OneLake.ReadWrite.All.",
		Annotations: write("Create OneLake Shortcut (Amazon S3 Target)", false, true),
	}, a.createShortcutAmazonS3)

	server.AddTool(r, &mcp.Tool{
		Name: "create-shortcut-azure-blob",
		Description: "Create a shortcut pointing to an Azure Blob Storage location. Requires a\n" +
			"connection ID for authentication and a target URL. Requires\n" +
			"OneLake.ReadWrite.All.",
		Annotations: write("Create OneLake Shortcut (Azure Blob Storage Target)", false, true),
	}, a.createShortcutAzureBlob)

	server.AddTool(r, &mcp.Tool{
		Name: "create-shortcut-gcs",
		Description: "Create a shortcut pointing to a Google Cloud Storage location. Requires a\n" +
			"connection ID for authentication and a target URL. Requires\n" +
			"OneLake.ReadWrite.All.",
		Annotations: write("Create OneLake Shortcut (Google Cloud Storage Target)", false, true),
	}, a.createShortcutGcs)

	server.AddTool(r, &mcp.Tool{
		Name: "create-shortcut-s3-compatible",
		Description: "Create a shortcut pointing to an S3-compatible storage location. Requires\n" +
			"a connection ID, target URL, and bucket name. Requires OneLake.ReadWrite.All.",
		Annotations: write("Create OneLake Shortcut (S3 Compatible Target)", false, true),
	}, a.createShortcutS3Compatible)

	server.AddTool(r, &mcp.Tool{
		Name: "create-shortcut-dataverse",
		Description: "Create a shortcut pointing to a Dataverse environment. Requires the\n" +
			"environment domain, connection ID, and Delta Lake folder. Requires\n" +
			"OneLake.ReadWrite.All.",
		Annotations: write("Create OneLake Shortcut (Dataverse Target)", false, true),
	}, a.createShortcutDataverse)

	server.AddTool(r, &mcp.Tool{
		Name: "create-shortcut-onedrive-sharepoint",
		Description: "Create a shortcut pointing to a OneDrive or SharePoint Online location.\n" +
			"Requires a connection ID and target URL. Optionally updates the Fabric\n" +
			"item sensitivity label from the source. Requires OneLake.ReadWrite.All.",
		Annotations: write("Create OneLake Shortcut (OneDrive/SharePoint Target)", false, true),
	}, a.createShortcutOneDriveSharePoint)
}

// shortcutCreateCommandResult mirrors every ShortcutCreate*Command's own
// Result record, e.g. ShortcutCreateOneLakeCommand.ShortcutCreateOneLakeCommandResult:
// all eight are single-field records wrapping the created OneLakeShortcut,
// which JSON-serialize identically (as {"shortcut": {...}}).
type shortcutCreateCommandResult struct {
	Shortcut shortcut `json:"shortcut"`
}

// createShortcut is the shared table-driven core of the eight
// create-shortcut-* commands: build the request body around target, POST it
// (with the conflict policy query parameter when one was given), and wrap
// whatever the Fabric API returns — or, if it answered with an empty body,
// the shortcut as sent — the way every ShortcutCreate*Command's own result
// record does. It ports IOneLakeService.CreateShortcutAsync plus each
// command's ExecuteAsync body.
func (a *Area) createShortcut(ctx context.Context, ws, item, path, name, conflictPolicy string, target *shortcutTarget) (*mcp.CallToolResult, any, error) {
	u := a.c.ep.fabric + "/workspaces/" + ws + "/items/" + item + "/shortcuts"
	if conflictPolicy != "" {
		u += "?shortcutConflictPolicy=" + url.QueryEscape(conflictPolicy)
	}
	sc := shortcut{Path: path, Name: name, Target: target}
	b, err := a.c.fabric(ctx, http.MethodPost, u, sc)
	if err != nil {
		return response.Error(err), nil, nil
	}
	result := sc
	if len(b) > 0 {
		if err := json.Unmarshal(b, &result); err != nil {
			return response.Error(&opError{msg: "Failed to parse shortcut response: " + err.Error()}), nil, nil
		}
	}
	return response.Success(shortcutCreateCommandResult{Shortcut: result}), nil, nil
}

// --- create-shortcut-onelake ---

type createShortcutOneLakeInput struct {
	WorkspaceID            string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID                 string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath           string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName           string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
	ShortcutConflictPolicy string `json:"shortcut-conflict-policy,omitempty" jsonschema:"Action when a shortcut with the same name and path already exists. Default: Abort."`
	TargetWorkspaceID      string `json:"target-workspace-id" jsonschema:"The workspace ID (GUID) of the target OneLake item."`
	TargetItemID           string `json:"target-item-id" jsonschema:"The item ID (GUID) of the target OneLake item."`
	TargetPath             string `json:"target-path" jsonschema:"The path within the target item (e.g. 'Files/data')."`
	TargetConnectionID     string `json:"target-connection-id,omitempty" jsonschema:"The connection ID (GUID) for authenticating to the target."`
}

func (a *Area) createShortcutOneLake(ctx context.Context, _ *mcp.CallToolRequest, in createShortcutOneLakeInput) (*mcp.CallToolResult, any, error) {
	return a.createShortcut(ctx, in.WorkspaceID, in.ItemID, in.ShortcutPath, in.ShortcutName, in.ShortcutConflictPolicy, &shortcutTarget{
		OneLake: &oneLakeShortcutTarget{
			WorkspaceID:  nonEmptyPtr(in.TargetWorkspaceID),
			ItemID:       nonEmptyPtr(in.TargetItemID),
			Path:         nonEmptyPtr(in.TargetPath),
			ConnectionID: nonEmptyPtr(in.TargetConnectionID),
		},
	})
}

// --- create-shortcut-adls-gen2 ---

type createShortcutAdlsGen2Input struct {
	WorkspaceID            string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID                 string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath           string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName           string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
	ShortcutConflictPolicy string `json:"shortcut-conflict-policy,omitempty" jsonschema:"Action when a shortcut with the same name and path already exists. Default: Abort."`
	TargetLocation         string `json:"target-location" jsonschema:"The target storage URL (e.g. 'https://myaccount.dfs.core.windows.net/container')."`
	TargetSubpath          string `json:"target-subpath" jsonschema:"The subpath within the target storage location."`
	TargetConnectionID     string `json:"target-connection-id" jsonschema:"The connection ID (GUID) for authenticating to the target."`
}

func (a *Area) createShortcutAdlsGen2(ctx context.Context, _ *mcp.CallToolRequest, in createShortcutAdlsGen2Input) (*mcp.CallToolResult, any, error) {
	return a.createShortcut(ctx, in.WorkspaceID, in.ItemID, in.ShortcutPath, in.ShortcutName, in.ShortcutConflictPolicy, &shortcutTarget{
		AdlsGen2: &adlsGen2ShortcutTarget{
			Location:     nonEmptyPtr(in.TargetLocation),
			Subpath:      nonEmptyPtr(in.TargetSubpath),
			ConnectionID: nonEmptyPtr(in.TargetConnectionID),
		},
	})
}

// --- create-shortcut-amazon-s3 ---

type createShortcutAmazonS3Input struct {
	WorkspaceID            string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID                 string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath           string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName           string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
	ShortcutConflictPolicy string `json:"shortcut-conflict-policy,omitempty" jsonschema:"Action when a shortcut with the same name and path already exists. Default: Abort."`
	TargetLocation         string `json:"target-location" jsonschema:"The target storage URL (e.g. 'https://myaccount.dfs.core.windows.net/container')."`
	TargetSubpath          string `json:"target-subpath,omitempty" jsonschema:"The subpath within the target storage location."`
	TargetConnectionID     string `json:"target-connection-id" jsonschema:"The connection ID (GUID) for authenticating to the target."`
}

func (a *Area) createShortcutAmazonS3(ctx context.Context, _ *mcp.CallToolRequest, in createShortcutAmazonS3Input) (*mcp.CallToolResult, any, error) {
	return a.createShortcut(ctx, in.WorkspaceID, in.ItemID, in.ShortcutPath, in.ShortcutName, in.ShortcutConflictPolicy, &shortcutTarget{
		AmazonS3: &amazonS3ShortcutTarget{
			Location:     nonEmptyPtr(in.TargetLocation),
			Subpath:      nonEmptyPtr(in.TargetSubpath),
			ConnectionID: nonEmptyPtr(in.TargetConnectionID),
		},
	})
}

// --- create-shortcut-azure-blob ---

type createShortcutAzureBlobInput struct {
	WorkspaceID            string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID                 string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath           string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName           string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
	ShortcutConflictPolicy string `json:"shortcut-conflict-policy,omitempty" jsonschema:"Action when a shortcut with the same name and path already exists. Default: Abort."`
	TargetLocation         string `json:"target-location" jsonschema:"The target storage URL (e.g. 'https://myaccount.dfs.core.windows.net/container')."`
	TargetSubpath          string `json:"target-subpath,omitempty" jsonschema:"The subpath within the target storage location."`
	TargetConnectionID     string `json:"target-connection-id" jsonschema:"The connection ID (GUID) for authenticating to the target."`
}

func (a *Area) createShortcutAzureBlob(ctx context.Context, _ *mcp.CallToolRequest, in createShortcutAzureBlobInput) (*mcp.CallToolResult, any, error) {
	return a.createShortcut(ctx, in.WorkspaceID, in.ItemID, in.ShortcutPath, in.ShortcutName, in.ShortcutConflictPolicy, &shortcutTarget{
		AzureBlobStorage: &azureBlobStorageShortcutTarget{
			Location:     nonEmptyPtr(in.TargetLocation),
			Subpath:      nonEmptyPtr(in.TargetSubpath),
			ConnectionID: nonEmptyPtr(in.TargetConnectionID),
		},
	})
}

// --- create-shortcut-gcs ---

type createShortcutGcsInput struct {
	WorkspaceID            string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID                 string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath           string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName           string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
	ShortcutConflictPolicy string `json:"shortcut-conflict-policy,omitempty" jsonschema:"Action when a shortcut with the same name and path already exists. Default: Abort."`
	TargetLocation         string `json:"target-location" jsonschema:"The target storage URL (e.g. 'https://myaccount.dfs.core.windows.net/container')."`
	TargetSubpath          string `json:"target-subpath,omitempty" jsonschema:"The subpath within the target storage location."`
	TargetConnectionID     string `json:"target-connection-id" jsonschema:"The connection ID (GUID) for authenticating to the target."`
}

func (a *Area) createShortcutGcs(ctx context.Context, _ *mcp.CallToolRequest, in createShortcutGcsInput) (*mcp.CallToolResult, any, error) {
	return a.createShortcut(ctx, in.WorkspaceID, in.ItemID, in.ShortcutPath, in.ShortcutName, in.ShortcutConflictPolicy, &shortcutTarget{
		GoogleCloudStorage: &googleCloudStorageShortcutTarget{
			Location:     nonEmptyPtr(in.TargetLocation),
			Subpath:      nonEmptyPtr(in.TargetSubpath),
			ConnectionID: nonEmptyPtr(in.TargetConnectionID),
		},
	})
}

// --- create-shortcut-s3-compatible ---

type createShortcutS3CompatibleInput struct {
	WorkspaceID            string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID                 string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath           string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName           string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
	ShortcutConflictPolicy string `json:"shortcut-conflict-policy,omitempty" jsonschema:"Action when a shortcut with the same name and path already exists. Default: Abort."`
	TargetLocation         string `json:"target-location" jsonschema:"The target storage URL (e.g. 'https://myaccount.dfs.core.windows.net/container')."`
	TargetSubpath          string `json:"target-subpath,omitempty" jsonschema:"The subpath within the target storage location."`
	TargetConnectionID     string `json:"target-connection-id" jsonschema:"The connection ID (GUID) for authenticating to the target."`
	TargetBucket           string `json:"target-bucket" jsonschema:"The bucket name for S3-compatible targets."`
}

func (a *Area) createShortcutS3Compatible(ctx context.Context, _ *mcp.CallToolRequest, in createShortcutS3CompatibleInput) (*mcp.CallToolResult, any, error) {
	return a.createShortcut(ctx, in.WorkspaceID, in.ItemID, in.ShortcutPath, in.ShortcutName, in.ShortcutConflictPolicy, &shortcutTarget{
		S3Compatible: &s3CompatibleShortcutTarget{
			Location:     nonEmptyPtr(in.TargetLocation),
			Subpath:      nonEmptyPtr(in.TargetSubpath),
			ConnectionID: nonEmptyPtr(in.TargetConnectionID),
			Bucket:       nonEmptyPtr(in.TargetBucket),
		},
	})
}

// --- create-shortcut-dataverse ---

type createShortcutDataverseInput struct {
	WorkspaceID             string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID                  string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath            string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName            string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
	ShortcutConflictPolicy  string `json:"shortcut-conflict-policy,omitempty" jsonschema:"Action when a shortcut with the same name and path already exists. Default: Abort."`
	TargetEnvironmentDomain string `json:"target-environment-domain" jsonschema:"The Dataverse environment domain URI (e.g. 'https://orgname.crm.dynamics.com')."`
	TargetConnectionID      string `json:"target-connection-id" jsonschema:"The connection ID (GUID) for authenticating to the target."`
	TargetDeltalakeFolder   string `json:"target-deltalake-folder" jsonschema:"The Delta Lake folder path in Dataverse."`
	// TargetTableName is accepted, matching upstream's schema, but never
	// used to build the request: upstream's own ShortcutCreateDataverseOptions
	// carries the identical unused option ("TODO (alzimmer): Option isn't
	// used, command probably needs to be updated.").
	TargetTableName string `json:"target-table-name,omitempty" jsonschema:"The Dataverse table name."`
}

func (a *Area) createShortcutDataverse(ctx context.Context, _ *mcp.CallToolRequest, in createShortcutDataverseInput) (*mcp.CallToolResult, any, error) {
	return a.createShortcut(ctx, in.WorkspaceID, in.ItemID, in.ShortcutPath, in.ShortcutName, in.ShortcutConflictPolicy, &shortcutTarget{
		Dataverse: &dataverseShortcutTarget{
			EnvironmentDomain: nonEmptyPtr(in.TargetEnvironmentDomain),
			DeltaLakeFolder:   nonEmptyPtr(in.TargetDeltalakeFolder),
			ConnectionID:      nonEmptyPtr(in.TargetConnectionID),
		},
	})
}

// --- create-shortcut-onedrive-sharepoint ---

type createShortcutOneDriveSharePointInput struct {
	WorkspaceID                       string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace."`
	ItemID                            string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ShortcutPath                      string `json:"shortcut-path" jsonschema:"The path of the shortcut within the item."`
	ShortcutName                      string `json:"shortcut-name" jsonschema:"The name of the shortcut."`
	ShortcutConflictPolicy            string `json:"shortcut-conflict-policy,omitempty" jsonschema:"Action when a shortcut with the same name and path already exists. Default: Abort."`
	TargetLocation                    string `json:"target-location" jsonschema:"The target storage URL (e.g. 'https://myaccount.dfs.core.windows.net/container')."`
	TargetSubpath                     string `json:"target-subpath,omitempty" jsonschema:"The subpath within the target storage location."`
	TargetConnectionID                string `json:"target-connection-id" jsonschema:"The connection ID (GUID) for authenticating to the target."`
	TargetUpdateFabricItemSensitivity bool   `json:"target-update-fabric-item-sensitivity,omitempty" jsonschema:"Whether to update Fabric item sensitivity from OneDrive/SharePoint. Default: false."`
}

func (a *Area) createShortcutOneDriveSharePoint(ctx context.Context, _ *mcp.CallToolRequest, in createShortcutOneDriveSharePointInput) (*mcp.CallToolResult, any, error) {
	// Upstream maps a false (default/unset) option to a null target field,
	// not to false: ShortcutCreateOneDriveSharePointCommand builds
	// `options.TargetUpdateFabricItemSensitivity ? true : null`.
	var sensitivity *bool
	if in.TargetUpdateFabricItemSensitivity {
		sensitivity = boolPtr(true)
	}
	return a.createShortcut(ctx, in.WorkspaceID, in.ItemID, in.ShortcutPath, in.ShortcutName, in.ShortcutConflictPolicy, &shortcutTarget{
		OneDriveSharePoint: &oneDriveSharePointShortcutTarget{
			Location:                    nonEmptyPtr(in.TargetLocation),
			Subpath:                     nonEmptyPtr(in.TargetSubpath),
			ConnectionID:                nonEmptyPtr(in.TargetConnectionID),
			UpdateFabricItemSensitivity: sensitivity,
		},
	})
}
