package onelake

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// fileSystemItem ports upstream's FileSystemItem model. OneLake writes
// nulls, so nullable fields are pointers rather than omitempty.
type fileSystemItem struct {
	Name         string           `json:"name"`
	Path         string           `json:"path"`
	Type         string           `json:"type"`
	Size         *int64           `json:"size"`
	LastModified *dotnetTime      `json:"lastModified"`
	ContentType  *string          `json:"contentType"`
	ETag         *string          `json:"etag"`
	Permissions  *string          `json:"permissions"`
	Owner        *string          `json:"owner"`
	Group        *string          `json:"group"`
	IsDirectory  bool             `json:"isDirectory"`
	Children     []fileSystemItem `json:"children"`
}

// blobGetResult ports upstream's BlobGetResult model (nested under "blob" in
// BlobGetCommandResult). OneLake writes nulls, so nullable fields are
// pointers rather than omitempty.
type blobGetResult struct {
	WorkspaceID            string            `json:"workspaceId"`
	ItemID                 string            `json:"itemId"`
	Path                   string            `json:"path"`
	ContentLength          *int64            `json:"contentLength"`
	ContentType            *string           `json:"contentType"`
	Charset                *string           `json:"charset"`
	ContentEncoding        *string           `json:"contentEncoding"`
	ContentLanguage        *string           `json:"contentLanguage"`
	ContentDisposition     *string           `json:"contentDisposition"`
	ContentMd5             *string           `json:"contentMd5"`
	ContentCrc64           *string           `json:"contentCrc64"`
	ContentBase64          *string           `json:"contentBase64"`
	ContentText            *string           `json:"contentText"`
	ETag                   *string           `json:"etag"`
	LastModified           *dotnetOffsetTime `json:"lastModified"`
	RequestServerEncrypted *bool             `json:"requestServerEncrypted"`
	EncryptionScope        *string           `json:"encryptionScope"`
	EncryptionKeySha256    *string           `json:"encryptionKeySha256"`
	Version                *string           `json:"version"`
	VersionID              *string           `json:"versionId"`
	RequestID              *string           `json:"requestId"`
	ClientRequestID        *string           `json:"clientRequestId"`
	RootActivityID         *string           `json:"rootActivityId"`
	ContentFilePath        *string           `json:"contentFilePath"`
	InlineContentTruncated bool              `json:"inlineContentTruncated"`
}

// blobPutResult ports the service-layer result PutBlobAsync returns, before
// BlobPutCommand wraps it (with the outer, auto-camelCased field names) into
// the tool's blobPutCommandResult. It is never serialized directly, so it
// carries no JSON tags.
type blobPutResult struct {
	WorkspaceID            string
	ItemID                 string
	Path                   string
	ContentLength          int64
	ContentType            string
	ETag                   *string
	LastModified           *dotnetOffsetTime
	RequestID              *string
	Version                *string
	RequestServerEncrypted *bool
	ContentMd5             *string
	ContentCrc64           *string
	EncryptionScope        *string
	EncryptionKeySha256    *string
	VersionID              *string
	ClientRequestID        *string
	RootActivityID         *string
}

// dotnetOffsetTime marshals like a .NET DateTimeOffset read straight off an
// HTTP response header (BlobGetResult/BlobPutResult LastModified): always
// UTC, the offset always shown as "+00:00" (.NET's DateTimeOffset "O" format
// never writes "Z"), fractional seconds trimmed when zero -- unlike
// dotnetTime (workspaces.go), which mimics DateTime.TryParse's implicit,
// Kind-Unspecified-so-local conversion instead.
type dotnetOffsetTime time.Time

func (t dotnetOffsetTime) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(time.Time(t).UTC().Format("2006-01-02T15:04:05.9999999-07:00"))), nil
}

// --- list-files service methods, ported from OneLakeService's ListPath* ---

// listPath ports ListPathAsync's single-directory branch.
func (a *Area) listPath(ctx context.Context, ws, item, p string, recursive bool) ([]fileSystemItem, error) {
	if err := validatePathForTraversal(p, "path"); err != nil {
		return nil, err
	}
	ws, item, err := a.workspaceAndItem(ctx, ws, item)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(p) == "" {
		return a.listPathIntelligent(ctx, ws, item, recursive)
	}
	dir := resolveDirectoryPath(p)
	u := buildAdlsListPathURL(a.c.ep, ws, item, dir, recursive)
	body, err := a.getDataPlane(ctx, u)
	if err != nil {
		return nil, err
	}
	items, err := parsePathListResponse(body)
	if err != nil {
		return nil, err
	}
	sortFileSystemItems(items)
	return items, nil
}

// listPathIntelligent ports ListPathIntelligentAsync: search both the Files
// and Tables top-level folders, skipping either on any error (typically 404
// when the folder doesn't exist yet).
func (a *Area) listPathIntelligent(ctx context.Context, ws, item string, recursive bool) ([]fileSystemItem, error) {
	ws, item, err := a.workspaceAndItem(ctx, ws, item)
	if err != nil {
		return nil, err
	}
	all := []fileSystemItem{}
	for _, folder := range [...]string{"Files", "Tables"} {
		u := buildAdlsListPathURL(a.c.ep, ws, item, folder, recursive)
		body, err := a.getDataPlane(ctx, u)
		if err != nil {
			continue
		}
		items, err := parsePathListResponse(body)
		if err != nil {
			continue
		}
		all = append(all, items...)
	}
	sortFileSystemItems(all)
	return all, nil
}

// listPathRaw ports ListPathRawAsync: the unprocessed OneLake DFS API
// response body(ies), for --format=raw.
func (a *Area) listPathRaw(ctx context.Context, ws, item, p string, recursive bool) (string, error) {
	if err := validatePathForTraversal(p, "path"); err != nil {
		return "", err
	}
	ws, item, err := a.workspaceAndItem(ctx, ws, item)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(p) == "" {
		var parts []string
		for _, folder := range [...]string{"Files", "Tables"} {
			u := buildAdlsListPathURL(a.c.ep, ws, item, folder, recursive)
			body, err := a.getDataPlane(ctx, u)
			if err != nil {
				parts = append(parts, "/* Error accessing folder "+folder+": "+err.Error()+" */")
				continue
			}
			parts = append(parts, "/* Response for folder: "+folder+" */\n"+string(body))
		}
		return strings.Join(parts, "\n\n"), nil
	}
	dir := resolveDirectoryPath(p)
	u := buildAdlsListPathURL(a.c.ep, ws, item, dir, recursive)
	body, err := a.getDataPlane(ctx, u)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// sortFileSystemItems ports the `OrderBy(f => f.Type == "directory" ? 0 : 1)
// .ThenBy(f => f.Name)` upstream applies after every listing: directories
// first, then files, each group alphabetical by name.
func sortFileSystemItems(items []fileSystemItem) {
	sort.SliceStable(items, func(i, j int) bool {
		ri, rj := rank(items[i]), rank(items[j])
		if ri != rj {
			return ri < rj
		}
		return items[i].Name < items[j].Name
	})
}

func rank(it fileSystemItem) int {
	if it.Type == "directory" {
		return 0
	}
	return 1
}

// buildAdlsListPathURL ports BuildAdlsListPathUrl: the directory is a query
// parameter of the ADLS Gen2 Path List API, not part of the URL path; in
// OneLake, filesystem = workspaceId and directory = itemId(/path).
func buildAdlsListPathURL(ep endpoints, workspaceID, itemID, directory string, recursive bool) string {
	dirPath := itemID
	if directory != "" {
		dirPath = itemID + "/" + strings.TrimLeft(directory, "/")
	}
	u := ep.dfs + "/" + workspaceID + "?resource=filesystem"
	u += "&directory=" + url.QueryEscape(dirPath)
	u += "&recursive=" + strconv.FormatBool(recursive)
	return u
}

// getDataPlane GETs u on the data plane and returns the response body.
func (a *Area) getDataPlane(ctx context.Context, u string) ([]byte, error) {
	resp, err := a.c.dataPlane(ctx, http.MethodGet, u, nil, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

// pathListEntry is the shape of one element of the ADLS Gen2 Path List API's
// "paths" array. isDirectory and contentLength arrive as either JSON strings
// or JSON literals depending on the API, so they're decoded loosely.
type pathListEntry struct {
	Name          string `json:"name"`
	IsDirectory   any    `json:"isDirectory"`
	ContentLength any    `json:"contentLength"`
	LastModified  string `json:"lastModified"`
	ETag          string `json:"etag"`
	Permissions   string `json:"permissions"`
	Owner         string `json:"owner"`
	Group         string `json:"group"`
}

// parsePathListResponse ports ParsePathListResponse.
func parsePathListResponse(body []byte) ([]fileSystemItem, error) {
	var doc struct {
		Paths []pathListEntry `json:"paths"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, &opError{msg: "Failed to parse OneLake path listing response: " + err.Error()}
	}
	items := make([]fileSystemItem, 0, len(doc.Paths))
	for _, p := range doc.Paths {
		if strings.TrimSpace(p.Name) == "" {
			continue
		}
		isDir := asBool(p.IsDirectory)
		typ := "file"
		contentType := "application/octet-stream"
		var size *int64
		if isDir {
			typ = "directory"
			contentType = "application/x-directory"
		} else {
			n := asInt64(p.ContentLength)
			size = &n
		}
		items = append(items, fileSystemItem{
			Name:         path.Base(p.Name),
			Path:         p.Name,
			Type:         typ,
			Size:         size,
			LastModified: parseHTTPTime(p.LastModified),
			ContentType:  &contentType,
			ETag:         nonEmptyPtr(p.ETag),
			Permissions:  nonEmptyPtr(p.Permissions),
			Owner:        nonEmptyPtr(p.Owner),
			Group:        nonEmptyPtr(p.Group),
			IsDirectory:  isDir,
		})
	}
	return items, nil
}

func asBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	}
	return false
}

func asInt64(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// --- download-file / upload-file / delete-file / create-directory /
// delete-directory service methods, ported from OneLakeService ---

// getBlob ports GetBlobAsync/DownloadBlobAsync against the blob endpoint.
// When downloadPath is non-empty, the content is streamed straight to that
// local file and no inline base64/text is produced -- matching
// BlobGetCommand, which sets IncludeInlineContent = downloadPath is null.
func (a *Area) getBlob(ctx context.Context, ws, item, blobPath, downloadPath string) (blobGetResult, error) {
	if err := validatePathForTraversal(blobPath, "blobPath"); err != nil {
		return blobGetResult{}, err
	}
	ws, item, err := a.workspaceAndItem(ctx, ws, item)
	if err != nil {
		return blobGetResult{}, err
	}
	u := a.c.ep.blob + "/" + ws + "/" + item + "/" + strings.TrimLeft(blobPath, "/")
	resp, err := a.c.dataPlane(ctx, http.MethodGet, u, nil, nil)
	if err != nil {
		return blobGetResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	h := resp.Header
	result := blobGetResult{WorkspaceID: ws, ItemID: item, Path: blobPath}
	result.ContentType, result.Charset = contentTypeAndCharset(h)
	result.ContentEncoding = headerPtr(h, "Content-Encoding")
	result.ContentLanguage = headerPtr(h, "Content-Language")
	result.ContentDisposition = headerPtr(h, "Content-Disposition")
	result.ContentMd5 = headerPtr(h, "Content-MD5")
	result.ContentCrc64 = headerPtr(h, "x-ms-content-crc64")
	result.ContentLength = headerInt64Ptr(h, "Content-Length")
	result.ETag = headerPtr(h, "ETag")
	result.LastModified = headerOffsetTimePtr(h, "Last-Modified")
	result.RequestServerEncrypted = headerBoolPtr(h, "x-ms-request-server-encrypted")
	result.EncryptionScope = headerPtr(h, "x-ms-encryption-scope")
	result.EncryptionKeySha256 = headerPtr(h, "x-ms-encryption-key-sha256")
	result.Version = headerPtr(h, "x-ms-version")
	result.VersionID = headerPtr(h, "x-ms-version-id")
	result.RequestID = headerPtr(h, "x-ms-request-id")
	result.ClientRequestID = headerPtr(h, "x-ms-client-request-id")
	result.RootActivityID = headerPtr(h, "x-ms-root-activity-id")

	switch {
	case downloadPath != "":
		if err := os.MkdirAll(filepath.Dir(downloadPath), 0o755); err != nil {
			return blobGetResult{}, err
		}
		f, err := os.OpenFile(downloadPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return blobGetResult{}, err
		}
		_, copyErr := io.Copy(f, resp.Body)
		closeErr := f.Close()
		if copyErr != nil {
			return blobGetResult{}, copyErr
		}
		if closeErr != nil {
			return blobGetResult{}, closeErr
		}
		result.ContentFilePath = &downloadPath
	case result.ContentLength != nil && *result.ContentLength > inlineContentLimitBytes:
		result.InlineContentTruncated = true
	default:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return blobGetResult{}, err
		}
		b64 := base64.StdEncoding.EncodeToString(body)
		result.ContentBase64 = &b64
		hasBinaryEncoding := result.ContentEncoding != nil && !strings.EqualFold(*result.ContentEncoding, "identity")
		if !hasBinaryEncoding && isTextContent(result.ContentType) {
			text := decodeText(body, result.Charset)
			result.ContentText = &text
		}
	}
	return result, nil
}

// putBlob ports PutBlobAsync against the blob endpoint.
func (a *Area) putBlob(ctx context.Context, ws, item, blobPath string, content []byte, contentType string, overwrite bool) (blobPutResult, error) {
	if err := validatePathForTraversal(blobPath, "blobPath"); err != nil {
		return blobPutResult{}, err
	}
	ws, item, err := a.workspaceAndItem(ctx, ws, item)
	if err != nil {
		return blobPutResult{}, err
	}
	ct := strings.TrimSpace(contentType)
	if ct == "" {
		ct = "application/octet-stream"
	}
	header := http.Header{}
	header.Set("x-ms-blob-type", "BlockBlob")
	header.Set("Content-Type", ct)
	if !overwrite {
		header.Set("If-None-Match", "*")
	}
	u := a.c.ep.blob + "/" + ws + "/" + item + "/" + strings.TrimLeft(blobPath, "/")
	resp, err := a.c.dataPlane(ctx, http.MethodPut, u, bytes.NewReader(content), header)
	if err != nil {
		return blobPutResult{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	h := resp.Header
	return blobPutResult{
		WorkspaceID:            ws,
		ItemID:                 item,
		Path:                   blobPath,
		ContentLength:          int64(len(content)),
		ContentType:            ct,
		ETag:                   headerPtr(h, "ETag"),
		LastModified:           headerOffsetTimePtr(h, "Last-Modified"),
		RequestID:              headerPtr(h, "x-ms-request-id"),
		Version:                headerPtr(h, "x-ms-version"),
		RequestServerEncrypted: headerBoolPtr(h, "x-ms-request-server-encrypted"),
		ContentMd5:             headerPtr(h, "Content-MD5"),
		ContentCrc64:           headerPtr(h, "x-ms-content-crc64"),
		EncryptionScope:        headerPtr(h, "x-ms-encryption-scope"),
		EncryptionKeySha256:    headerPtr(h, "x-ms-encryption-key-sha256"),
		VersionID:              headerPtr(h, "x-ms-version-id"),
		ClientRequestID:        headerPtr(h, "x-ms-client-request-id"),
		RootActivityID:         headerPtr(h, "x-ms-root-activity-id"),
	}, nil
}

// deleteFile ports DeleteFileAsync against the "api" (ADLS-flavoured) data
// plane endpoint.
func (a *Area) deleteFile(ctx context.Context, ws, item, filePath string) error {
	if err := validatePathForTraversal(filePath, "filePath"); err != nil {
		return err
	}
	ws, item, err := a.workspaceAndItem(ctx, ws, item)
	if err != nil {
		return err
	}
	u := a.c.ep.api + "/" + ws + "/" + item + "/" + strings.TrimLeft(filePath, "/")
	resp, err := a.c.dataPlane(ctx, http.MethodDelete, u, nil, nil)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// deleteDirectory ports DeleteDirectoryAsync against the "api" endpoint,
// with the x-ms-delete-type: directory header Blob Storage requires for a
// directory delete.
func (a *Area) deleteDirectory(ctx context.Context, ws, item, directoryPath string, recursive bool) error {
	if err := validatePathForTraversal(directoryPath, "directoryPath"); err != nil {
		return err
	}
	ws, item, err := a.workspaceAndItem(ctx, ws, item)
	if err != nil {
		return err
	}
	u := a.c.ep.api + "/" + ws + "/" + item + "/" + strings.TrimLeft(directoryPath, "/")
	if recursive {
		u += "?recursive=true"
	}
	header := http.Header{}
	header.Set("x-ms-delete-type", "directory")
	resp, err := a.c.dataPlane(ctx, http.MethodDelete, u, nil, header)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// createDirectory ports CreateDirectoryAsync against the DFS endpoint.
func (a *Area) createDirectory(ctx context.Context, ws, item, directoryPath string) error {
	if err := validatePathForTraversal(directoryPath, "directoryPath"); err != nil {
		return err
	}
	ws, item, err := a.workspaceAndItem(ctx, ws, item)
	if err != nil {
		return err
	}
	u := a.c.ep.dfs + "/" + ws + "/" + item + "/" + strings.TrimLeft(directoryPath, "/") + "?resource=directory"
	header := http.Header{}
	header.Set("x-ms-resource", "directory")
	resp, err := a.c.dataPlane(ctx, http.MethodPut, u, nil, header)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// --- header / content-type helpers ---

func headerPtr(h http.Header, name string) *string {
	if v := h.Get(name); v != "" {
		return &v
	}
	return nil
}

func headerInt64Ptr(h http.Header, name string) *int64 {
	v := h.Get(name)
	if v == "" {
		return nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return nil
	}
	return &n
}

func headerBoolPtr(h http.Header, name string) *bool {
	v := h.Get(name)
	if v == "" {
		return nil
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return nil
	}
	return &b
}

func headerOffsetTimePtr(h http.Header, name string) *dotnetOffsetTime {
	v := h.Get(name)
	if v == "" {
		return nil
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return nil
	}
	d := dotnetOffsetTime(t)
	return &d
}

// contentTypeAndCharset returns the raw Content-Type header (matching
// upstream's contentTypeHeader?.ToString()) and, when present, its charset
// parameter.
func contentTypeAndCharset(h http.Header) (*string, *string) {
	ct := h.Get("Content-Type")
	if ct == "" {
		return nil, nil
	}
	var charset *string
	if _, params, err := mime.ParseMediaType(ct); err == nil {
		if cs, ok := params["charset"]; ok {
			charset = &cs
		}
	}
	return &ct, charset
}

// isTextContent ports IsTextContent.
func isTextContent(contentType *string) bool {
	if contentType == nil || strings.TrimSpace(*contentType) == "" {
		return false
	}
	ct := strings.ToLower(*contentType)
	if strings.HasPrefix(ct, "text/") {
		return true
	}
	for _, kw := range [...]string{"json", "xml", "yaml", "csv", "html", "javascript"} {
		if strings.Contains(ct, kw) {
			return true
		}
	}
	return false
}

// decodeText ports GetTextEncoding + Encoding.GetString for the charsets
// likely to appear on OneLake content: UTF-8 (the default, like upstream's
// fallback) and the single-byte Latin-1/ASCII family, decoded byte-for-rune.
func decodeText(body []byte, charset *string) string {
	if charset != nil {
		switch strings.ToLower(strings.TrimSpace(*charset)) {
		case "us-ascii", "ascii", "iso-8859-1", "latin1":
			runes := make([]rune, len(body))
			for i, b := range body {
				runes[i] = rune(b)
			}
			return string(runes)
		}
	}
	return string(body)
}
