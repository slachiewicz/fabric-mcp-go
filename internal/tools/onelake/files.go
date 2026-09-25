package onelake

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// registerFiles ports upstream's non-hidden Fabric.Mcp.Tools.OneLake.Commands.File
// commands: PathListCommand, BlobGetCommand, BlobPutCommand, FileDeleteCommand,
// DirectoryCreateCommand and DirectoryDeleteCommand. BlobListCommand,
// BlobDeleteCommand, FileReadCommand and FileWriteCommand carry
// [HiddenCommand] upstream and are not exposed as MCP tools, so they aren't
// ported here.
func (a *Area) registerFiles(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "list-files",
		Description: "List files and directories in OneLake storage using a filesystem-style hierarchical view, similar to Azure Data Lake Storage Gen2.\n" +
			"Shows directory structure with paths, sizes, timestamps, and metadata. Use this to explore OneLake content in a filesystem format\n" +
			"rather than flat blob listing. Supports optional path filtering and recursive directory traversal.\n" +
			"\n" +
			"If no path is specified, intelligently discovers content by searching both Files and Tables folders automatically,\n" +
			"providing comprehensive visibility across all top-level OneLake folders.\n" +
			"\n" +
			"Use --format=raw to get the unprocessed OneLake DFS API response for debugging and analysis.",
		Annotations: readOnly("List OneLake Path Structure"),
	}, a.listFiles)

	server.AddTool(r, &mcp.Tool{
		Name: "download-file",
		Description: "Downloads a file from OneLake storage. Use this when the user needs to retrieve file content or metadata. " +
			"Returns base64 content, metadata, and text when applicable.",
		Annotations: readOnly("Download OneLake File"),
	}, a.downloadFile)

	server.AddTool(r, &mcp.Tool{
		Name: "upload-file",
		Description: "Uploads a file to OneLake storage from inline content or local file path. Use this when the user needs to store data in OneLake. " +
			"Supports overwrite control and content type specification.",
		Annotations: write("Upload OneLake File", true, false),
	}, a.uploadFile)

	server.AddTool(r, &mcp.Tool{
		Name: "delete-file",
		Description: "Deletes a file from OneLake storage. Use this when the user wants to remove a specific file. " +
			"Permanently removes the file at the specified path.",
		Annotations: write("Delete OneLake File", true, true),
	}, a.deleteFileTool)

	server.AddTool(r, &mcp.Tool{
		Name: "create-directory",
		Description: "Creates a directory in OneLake storage. Use this when the user needs to organize files or prepare folder structures. " +
			"Can create nested directory paths.",
		Annotations: write("Create OneLake Directory", false, true),
	}, a.createDirectoryTool)

	server.AddTool(r, &mcp.Tool{
		Name: "delete-directory",
		Description: "Deletes a directory from OneLake storage. Use this when the user wants to remove a folder. " +
			"Use recursive flag to delete non-empty directories.",
		Annotations: write("Delete OneLake Directory", true, true),
	}, a.deleteDirectoryTool)
}

// itemOf returns --item-id, else --item, as upstream's file commands do.
func itemOf(id, nameOrID string) string {
	if id != "" {
		return id
	}
	return nameOrID
}

// requireWorkspaceAndItem ports the ValidateOptions check every upstream
// file command shares: both a workspace and an item identifier are
// required, and multiple failures are joined the way .NET's ValidationResult
// joins them (one message per line).
func requireWorkspaceAndItem(ws, item string) *mcp.CallToolResult {
	var errs []string
	if strings.TrimSpace(ws) == "" {
		errs = append(errs, errWorkspaceRequired)
	}
	if strings.TrimSpace(item) == "" {
		errs = append(errs, errItemRequired)
	}
	if len(errs) == 0 {
		return nil
	}
	return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n"))
}

// --- list-files ---

type listFilesInput struct {
	WorkspaceID string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace   string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID      string `json:"item-id,omitempty" jsonschema:"The ID of the Fabric item."`
	Item        string `json:"item,omitempty" jsonschema:"The name or ID of the Fabric item. When using friendly names, MUST include the item type suffix (e.g., 'ItemName.Lakehouse', 'ItemName.Warehouse')."`
	Path        string `json:"path,omitempty" jsonschema:"The path to list in OneLake storage (optional, defaults to root)."`
	Recursive   bool   `json:"recursive,omitempty" jsonschema:"Whether to perform the operation recursively."`
	Format      string `json:"format,omitempty" jsonschema:"Output format for OneLake API responses. Use 'json' for parsed objects, 'xml' for raw XML API response, or 'raw' for unprocessed API response. Supported values: 'json' (default), 'xml', 'raw'."`
}

// pathListResult ports PathListCommand.PathListResult. OneLake writes nulls,
// so neither field is omitempty.
type pathListResult struct {
	Items       []fileSystemItem `json:"items"`
	RawResponse *string          `json:"rawResponse"`
}

// listFiles ports PathListCommand, which keeps the default error mapping.
func (a *Area) listFiles(ctx context.Context, _ *mcp.CallToolRequest, in listFilesInput) (*mcp.CallToolResult, any, error) {
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := itemOf(in.ItemID, in.Item)
	if r := requireWorkspaceAndItem(ws, item); r != nil {
		return r, nil, nil
	}

	if strings.EqualFold(in.Format, "raw") {
		raw, err := a.listPathRaw(ctx, ws, item, in.Path, in.Recursive)
		if err != nil {
			return response.Error(err), nil, nil
		}
		return response.Success(pathListResult{RawResponse: &raw}), nil, nil
	}

	var (
		items []fileSystemItem
		err   error
	)
	if strings.TrimSpace(in.Path) == "" {
		items, err = a.listPathIntelligent(ctx, ws, item, in.Recursive)
	} else {
		items, err = a.listPath(ctx, ws, item, in.Path, in.Recursive)
	}
	if err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(pathListResult{Items: items}), nil, nil
}

// --- download-file ---

type downloadFileInput struct {
	WorkspaceID      string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace        string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID           string `json:"item-id,omitempty" jsonschema:"The ID of the Fabric item."`
	Item             string `json:"item,omitempty" jsonschema:"The name or ID of the Fabric item. When using friendly names, MUST include the item type suffix (e.g., 'ItemName.Lakehouse', 'ItemName.Warehouse')."`
	FilePath         string `json:"file-path" jsonschema:"The path to the file in OneLake."`
	DownloadFilePath string `json:"download-file-path,omitempty" jsonschema:"Local path to save the downloaded content when running locally."`
}

// blobGetCommandResult ports BlobGetCommand.BlobGetCommandResult, which
// nests the blob details under "blob".
type blobGetCommandResult struct {
	Blob    blobGetResult `json:"blob"`
	Message string        `json:"message"`
}

// inlineContentLimitBytes mirrors BlobGetCommand.InlineContentLimitBytes.
const inlineContentLimitBytes = 1 * 1024 * 1024

// downloadFile ports BlobGetCommand, which keeps the default error mapping.
// This server has only the stdio transport (see cmd/fabmcp), so
// --download-file-path is always usable; upstream's stdio-only guard for it
// has no non-stdio transport to reject here.
func (a *Area) downloadFile(ctx context.Context, _ *mcp.CallToolRequest, in downloadFileInput) (*mcp.CallToolResult, any, error) {
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := itemOf(in.ItemID, in.Item)
	if r := requireWorkspaceAndItem(ws, item); r != nil {
		return r, nil, nil
	}

	downloadPath := in.DownloadFilePath
	if downloadPath != "" && !filepath.IsAbs(downloadPath) {
		abs, err := filepath.Abs(downloadPath)
		if err != nil {
			return response.Error(err), nil, nil
		}
		downloadPath = abs
	}

	blob, err := a.getBlob(ctx, ws, item, in.FilePath, downloadPath)
	if err != nil {
		return response.Error(err), nil, nil
	}

	var message string
	switch {
	case downloadPath != "":
		path := downloadPath
		if blob.ContentFilePath != nil {
			path = *blob.ContentFilePath
		}
		message = "File downloaded to local file '" + path + "'."
	case blob.InlineContentTruncated:
		message = "File metadata retrieved. Content exceeds the inline limit of 1,048,576 bytes; provide --download-file-path when running locally to save the content."
	default:
		message = "File retrieved successfully."
	}

	return response.Success(blobGetCommandResult{Blob: blob, Message: message}), nil, nil
}

// --- upload-file ---

type uploadFileInput struct {
	WorkspaceID   string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace     string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID        string `json:"item-id,omitempty" jsonschema:"The ID of the Fabric item."`
	Item          string `json:"item,omitempty" jsonschema:"The name or ID of the Fabric item. When using friendly names, MUST include the item type suffix (e.g., 'ItemName.Lakehouse', 'ItemName.Warehouse')."`
	FilePath      string `json:"file-path" jsonschema:"The path to the file in OneLake."`
	Content       string `json:"content,omitempty" jsonschema:"The content to write to the file."`
	LocalFilePath string `json:"local-file-path,omitempty" jsonschema:"The path to a local file to upload."`
	Overwrite     bool   `json:"overwrite,omitempty" jsonschema:"Whether to overwrite existing files."`
	ContentType   string `json:"content-type,omitempty" jsonschema:"MIME content type to set on the uploaded file (e.g., 'application/json'). Defaults to 'application/octet-stream'."`
}

// blobPutCommandResult ports BlobPutCommand.BlobPutCommandResult: a flat
// record, unlike BlobGetCommandResult which nests a "blob" object.
type blobPutCommandResult struct {
	WorkspaceID            string            `json:"workspaceId"`
	ItemID                 string            `json:"itemId"`
	BlobPath               string            `json:"blobPath"`
	ContentLength          int64             `json:"contentLength"`
	ContentType            string            `json:"contentType"`
	ETag                   *string           `json:"eTag"`
	LastModified           *dotnetOffsetTime `json:"lastModified"`
	RequestID              *string           `json:"requestId"`
	Version                *string           `json:"version"`
	RequestServerEncrypted *bool             `json:"requestServerEncrypted"`
	ContentMd5             *string           `json:"contentMd5"`
	ContentCrc64           *string           `json:"contentCrc64"`
	EncryptionScope        *string           `json:"encryptionScope"`
	EncryptionKeySha256    *string           `json:"encryptionKeySha256"`
	VersionID              *string           `json:"versionId"`
	ClientRequestID        *string           `json:"clientRequestId"`
	RootActivityID         *string           `json:"rootActivityId"`
	Message                string            `json:"message"`
}

// uploadFile ports BlobPutCommand, which keeps the default error mapping.
func (a *Area) uploadFile(ctx context.Context, _ *mcp.CallToolRequest, in uploadFileInput) (*mcp.CallToolResult, any, error) {
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := itemOf(in.ItemID, in.Item)
	if r := requireWorkspaceAndItem(ws, item); r != nil {
		return r, nil, nil
	}

	content, err := resolveUploadContent(in.LocalFilePath, in.Content)
	if err != nil {
		return response.Error(err), nil, nil
	}

	blob, err := a.putBlob(ctx, ws, item, in.FilePath, content, in.ContentType, in.Overwrite)
	if err != nil {
		return response.Error(err), nil, nil
	}

	message := "File uploaded successfully."
	if in.Overwrite {
		message = "File uploaded successfully (overwritten)."
	}

	return response.SuccessStatus(http.StatusCreated, blobPutCommandResult{
		WorkspaceID:            blob.WorkspaceID,
		ItemID:                 blob.ItemID,
		BlobPath:               blob.Path,
		ContentLength:          blob.ContentLength,
		ContentType:            blob.ContentType,
		ETag:                   blob.ETag,
		LastModified:           blob.LastModified,
		RequestID:              blob.RequestID,
		Version:                blob.Version,
		RequestServerEncrypted: blob.RequestServerEncrypted,
		ContentMd5:             blob.ContentMd5,
		ContentCrc64:           blob.ContentCrc64,
		EncryptionScope:        blob.EncryptionScope,
		EncryptionKeySha256:    blob.EncryptionKeySha256,
		VersionID:              blob.VersionID,
		ClientRequestID:        blob.ClientRequestID,
		RootActivityID:         blob.RootActivityID,
		Message:                message,
	}), nil, nil
}

// resolveUploadContent ports BlobPutCommand.ResolveContentStream.
func resolveUploadContent(localFilePath, content string) ([]byte, error) {
	if localFilePath != "" {
		b, err := os.ReadFile(localFilePath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, &opError{msg: "Local file not found: " + localFilePath}
			}
			return nil, err
		}
		return b, nil
	}
	if content != "" {
		return []byte(content), nil
	}
	return nil, &argError{msg: "Either --content or --local-file-path must be specified when uploading a blob."}
}

// --- delete-file ---

type deleteFileInput struct {
	WorkspaceID string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace   string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID      string `json:"item-id,omitempty" jsonschema:"The ID of the Fabric item."`
	Item        string `json:"item,omitempty" jsonschema:"The name or ID of the Fabric item. When using friendly names, MUST include the item type suffix (e.g., 'ItemName.Lakehouse', 'ItemName.Warehouse')."`
	FilePath    string `json:"file-path" jsonschema:"The path to the file in OneLake."`
}

// fileDeleteCommandResult ports FileDeleteCommand.FileDeleteCommandResult.
type fileDeleteCommandResult struct {
	FilePath string `json:"filePath"`
	Message  string `json:"message"`
}

// deleteFileTool ports FileDeleteCommand, which keeps the default error
// mapping. Named with the Tool suffix to avoid colliding with the deleteFile
// service method below (see listItemsTool in workspaces.go for the same
// convention).
func (a *Area) deleteFileTool(ctx context.Context, _ *mcp.CallToolRequest, in deleteFileInput) (*mcp.CallToolResult, any, error) {
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := itemOf(in.ItemID, in.Item)
	if r := requireWorkspaceAndItem(ws, item); r != nil {
		return r, nil, nil
	}
	if err := a.deleteFile(ctx, ws, item, in.FilePath); err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(fileDeleteCommandResult{FilePath: in.FilePath, Message: "File deleted successfully"}), nil, nil
}

// --- create-directory ---

type createDirectoryInput struct {
	WorkspaceID   string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace     string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID        string `json:"item-id,omitempty" jsonschema:"The ID of the Fabric item."`
	Item          string `json:"item,omitempty" jsonschema:"The name or ID of the Fabric item. When using friendly names, MUST include the item type suffix (e.g., 'ItemName.Lakehouse', 'ItemName.Warehouse')."`
	DirectoryPath string `json:"directory-path" jsonschema:"The path to the directory in OneLake."`
}

// directoryCreateCommandResult ports DirectoryCreateCommand.DirectoryCreateCommandResult.
type directoryCreateCommandResult struct {
	WorkspaceID   string `json:"workspaceId"`
	ItemID        string `json:"itemId"`
	DirectoryPath string `json:"directoryPath"`
	Success       bool   `json:"success"`
	Message       string `json:"message"`
}

// createDirectoryTool ports DirectoryCreateCommand, the one file command that
// overrides GetErrorMessage/GetStatusCode with OneLakeCommandValidators, so
// it uses errorResult rather than response.Error.
func (a *Area) createDirectoryTool(ctx context.Context, _ *mcp.CallToolRequest, in createDirectoryInput) (*mcp.CallToolResult, any, error) {
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := itemOf(in.ItemID, in.Item)
	if r := requireWorkspaceAndItem(ws, item); r != nil {
		return r, nil, nil
	}
	if err := a.createDirectory(ctx, ws, item, in.DirectoryPath); err != nil {
		return errorResult(err), nil, nil
	}
	// Upstream reports back the raw --workspace/--item option values here,
	// not the normalized/resolved identifiers (DirectoryCreateCommand builds
	// this result from options.WorkspaceId ?? options.Workspace, not from
	// the service's normalized identifiers) -- ws and item already are those
	// raw values, resolution happens only inside a.createDirectory.
	result := directoryCreateCommandResult{
		WorkspaceID:   ws,
		ItemID:        item,
		DirectoryPath: in.DirectoryPath,
		Success:       true,
		Message:       "Directory '" + in.DirectoryPath + "' created successfully",
	}
	return response.Success(result), nil, nil
}

// --- delete-directory ---

type deleteDirectoryInput struct {
	WorkspaceID   string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace     string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID        string `json:"item-id,omitempty" jsonschema:"The ID of the Fabric item."`
	Item          string `json:"item,omitempty" jsonschema:"The name or ID of the Fabric item. When using friendly names, MUST include the item type suffix (e.g., 'ItemName.Lakehouse', 'ItemName.Warehouse')."`
	DirectoryPath string `json:"directory-path" jsonschema:"The path to the directory in OneLake."`
	Recursive     bool   `json:"recursive,omitempty" jsonschema:"Whether to perform the operation recursively."`
}

// directoryDeleteCommandResult ports DirectoryDeleteCommand.DirectoryDeleteCommandResult.
type directoryDeleteCommandResult struct {
	DirectoryPath string `json:"directoryPath"`
	Message       string `json:"message"`
}

// deleteDirectoryTool ports DirectoryDeleteCommand, which keeps the default
// error mapping.
func (a *Area) deleteDirectoryTool(ctx context.Context, _ *mcp.CallToolRequest, in deleteDirectoryInput) (*mcp.CallToolResult, any, error) {
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	item := itemOf(in.ItemID, in.Item)
	if r := requireWorkspaceAndItem(ws, item); r != nil {
		return r, nil, nil
	}
	if err := a.deleteDirectory(ctx, ws, item, in.DirectoryPath, in.Recursive); err != nil {
		return response.Error(err), nil, nil
	}
	message := "Directory deleted successfully"
	if in.Recursive {
		message = "Directory and all contents deleted successfully"
	}
	return response.Success(directoryDeleteCommandResult{DirectoryPath: in.DirectoryPath, Message: message}), nil, nil
}
