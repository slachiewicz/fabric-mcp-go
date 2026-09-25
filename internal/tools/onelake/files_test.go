package onelake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// pathListJSON builds a minimal ADLS Gen2 Path List API response body, in
// the string-encoded shape the real API (and the upstream C# parser) uses.
func pathListJSON(entries ...pathListEntry) string {
	type wire struct {
		Name          string `json:"name"`
		IsDirectory   string `json:"isDirectory"`
		ContentLength string `json:"contentLength"`
		LastModified  string `json:"lastModified"`
		ETag          string `json:"etag"`
		Permissions   string `json:"permissions"`
		Owner         string `json:"owner"`
		Group         string `json:"group"`
	}
	var out []wire
	for _, e := range entries {
		out = append(out, wire{
			Name:          e.Name,
			IsDirectory:   strconv.FormatBool(asBool(e.IsDirectory)),
			ContentLength: strconv.FormatInt(asInt64(e.ContentLength), 10),
			LastModified:  e.LastModified,
			ETag:          e.ETag,
			Permissions:   e.Permissions,
			Owner:         e.Owner,
			Group:         e.Group,
		})
	}
	b, _ := json.Marshal(map[string]any{"paths": out})
	return string(b)
}

const httpDate = "Fri, 28 Aug 2026 21:27:14 GMT"

func TestListFiles(t *testing.T) {
	body := pathListJSON(
		pathListEntry{Name: "sales.Lakehouse/Files/b.txt", IsDirectory: "false", ContentLength: "10", LastModified: httpDate, ETag: "etag-b"},
		pathListEntry{Name: "sales.Lakehouse/Files/a.txt", IsDirectory: "false", ContentLength: "5", LastModified: httpDate, ETag: "etag-a"},
		pathListEntry{Name: "sales.Lakehouse/Files/sub", IsDirectory: "true", LastModified: httpDate, ETag: "etag-dir"},
	)
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/dfs/ws") || r.URL.Query().Get("resource") != "filesystem" {
			t.Errorf("unexpected request %s", r.URL)
		}
		dir := r.URL.Query().Get("directory")
		if dir != "sales.Lakehouse/Files/reports" {
			t.Errorf("directory = %q", dir)
		}
		_, _ = io.WriteString(w, body)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-files", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "path": "Files/reports",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	results := env["results"].(map[string]any)
	items := results["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3: %s", len(items), jsonOf(env))
	}
	// Directories sort first, then files alphabetically by name.
	names := []string{items[0].(map[string]any)["name"].(string), items[1].(map[string]any)["name"].(string), items[2].(map[string]any)["name"].(string)}
	if want := []string{"sub", "a.txt", "b.txt"}; names[0] != want[0] || names[1] != want[1] || names[2] != want[2] {
		t.Errorf("names = %v, want %v", names, want)
	}
	dirItem := items[0].(map[string]any)
	if dirItem["isDirectory"] != true || dirItem["size"] != nil || dirItem["contentType"] != "application/x-directory" {
		t.Errorf("directory item = %s", jsonOf(dirItem))
	}
	fileItem := items[1].(map[string]any)
	if fileItem["isDirectory"] != false || fileItem["size"] != 5.0 || fileItem["contentType"] != "application/octet-stream" {
		t.Errorf("file item = %s", jsonOf(fileItem))
	}
	if results["rawResponse"] != nil {
		t.Errorf("rawResponse = %v, want nil", results["rawResponse"])
	}
}

func TestListFilesIntelligentDiscoverySkipsErrors(t *testing.T) {
	filesBody := pathListJSON(pathListEntry{Name: "sales.Lakehouse/Files/a.txt", IsDirectory: "false", ContentLength: "1", LastModified: httpDate})
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		dir := r.URL.Query().Get("directory")
		switch dir {
		case "sales.Lakehouse/Files":
			_, _ = io.WriteString(w, filesBody)
		case "sales.Lakehouse/Tables":
			http.Error(w, "not found", http.StatusNotFound)
		default:
			t.Errorf("unexpected directory %q", dir)
		}
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-files", map[string]any{"workspace": "ws", "item": "sales.Lakehouse"})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	items := env["results"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "a.txt" {
		t.Errorf("items = %s", jsonOf(items))
	}
}

func TestListFilesRawFormat(t *testing.T) {
	body := `{"paths":[{"name":"sales.Lakehouse/Files/a.txt"}]}`
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, body)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-files", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "path": "Files", "format": "raw",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	results := env["results"].(map[string]any)
	if results["items"] != nil {
		t.Errorf("items = %v, want nil", results["items"])
	}
	if results["rawResponse"] != body {
		t.Errorf("rawResponse = %q, want %q", results["rawResponse"], body)
	}
}

func TestListFilesValidation(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-files", nil)
	want := "Workspace identifier is required. Provide --workspace or --workspace-id.\nItem identifier is required. Provide --item or --item-id."
	if !isErr || env["message"] != want || env["status"] != 400.0 {
		t.Errorf("missing both: %s", jsonOf(env))
	}

	env, isErr = call(t, cs, "onelake_list-files", map[string]any{"workspace": "ws"})
	if !isErr || env["message"] != errItemRequired {
		t.Errorf("missing item: %s", jsonOf(env))
	}
}

func TestListFilesPathTraversal(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_list-files", map[string]any{"workspace": "ws", "item": "sales.Lakehouse", "path": "../etc"})
	if !isErr {
		t.Fatalf("expected error: %s", jsonOf(env))
	}
	if !strings.Contains(env["message"].(string), "directory traversal") {
		t.Errorf("message = %q", env["message"])
	}
}

func TestDownloadFileInline(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/Files/a.txt") {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("ETag", `"0xetag"`)
		w.Header().Set("Last-Modified", httpDate)
		w.Header().Set("x-ms-request-id", "req-1")
		_, _ = io.WriteString(w, "hello world")
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_download-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/a.txt",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	results := env["results"].(map[string]any)
	blob := results["blob"].(map[string]any)
	if blob["contentText"] != "hello world" {
		t.Errorf("contentText = %v", blob["contentText"])
	}
	if blob["contentBase64"] != "aGVsbG8gd29ybGQ=" {
		t.Errorf("contentBase64 = %v", blob["contentBase64"])
	}
	if blob["etag"] != `"0xetag"` {
		t.Errorf("etag = %v", blob["etag"])
	}
	if blob["lastModified"] != "2026-08-28T21:27:14+00:00" {
		t.Errorf("lastModified = %v", blob["lastModified"])
	}
	if blob["contentFilePath"] != nil {
		t.Errorf("contentFilePath = %v, want nil", blob["contentFilePath"])
	}
	if results["message"] != "File retrieved successfully." {
		t.Errorf("message = %v", results["message"])
	}
}

func TestDownloadFileTruncated(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.Itoa(inlineContentLimitBytes+1))
		w.WriteHeader(http.StatusOK)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_download-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/big.bin",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	results := env["results"].(map[string]any)
	blob := results["blob"].(map[string]any)
	if blob["contentBase64"] != nil || blob["contentText"] != nil {
		t.Errorf("content should not be inlined: %s", jsonOf(blob))
	}
	if blob["inlineContentTruncated"] != true {
		t.Errorf("inlineContentTruncated = %v", blob["inlineContentTruncated"])
	}
	if !strings.Contains(results["message"].(string), "exceeds the inline limit") {
		t.Errorf("message = %v", results["message"])
	}
}

func TestDownloadFileToLocalPath(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "file content")
	})
	cs := session(t, a)

	dest := filepath.Join(t.TempDir(), "sub", "out.txt")
	env, isErr := call(t, cs, "onelake_download-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/a.txt", "download-file-path": dest,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	b, err := os.ReadFile(dest)
	if err != nil || string(b) != "file content" {
		t.Fatalf("downloaded file: %v %q", err, b)
	}
	results := env["results"].(map[string]any)
	blob := results["blob"].(map[string]any)
	if blob["contentFilePath"] != dest {
		t.Errorf("contentFilePath = %v, want %q", blob["contentFilePath"], dest)
	}
	if blob["contentBase64"] != nil {
		t.Errorf("contentBase64 should be nil when writing to a local file: %v", blob["contentBase64"])
	}
	if want := "File downloaded to local file '" + dest + "'."; results["message"] != want {
		t.Errorf("message = %q, want %q", results["message"], want)
	}
}

func TestDownloadFileNotFound(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "The specified blob does not exist.", http.StatusNotFound)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_download-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/missing.txt",
	})
	if !isErr || env["status"] != 404.0 {
		t.Fatalf("not found: %s", jsonOf(env))
	}
	if !strings.HasPrefix(env["message"].(string), "Service unavailable or network connectivity issues. Details:") {
		t.Errorf("message = %q", env["message"])
	}
}

func TestDownloadFileValidation(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_download-file", map[string]any{"workspace": "ws", "file-path": "Files/a.txt"})
	if !isErr || env["message"] != errItemRequired {
		t.Errorf("missing item: %s", jsonOf(env))
	}
}

func TestUploadFile(t *testing.T) {
	var gotOverwrite, gotBlobType, gotContentType string
	var gotBody []byte
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/Files/a.txt") || r.Method != http.MethodPut {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		gotOverwrite = r.Header.Get("If-None-Match")
		gotBlobType = r.Header.Get("x-ms-blob-type")
		gotContentType = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("ETag", `"0xnew"`)
		w.Header().Set("x-ms-request-id", "put-1")
		w.WriteHeader(http.StatusCreated)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_upload-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/a.txt", "content": "hello",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if gotBlobType != "BlockBlob" || gotOverwrite != "*" || gotContentType != "application/octet-stream" {
		t.Errorf("headers: blobType=%q overwrite=%q contentType=%q", gotBlobType, gotOverwrite, gotContentType)
	}
	if string(gotBody) != "hello" {
		t.Errorf("body = %q", gotBody)
	}
	results := env["results"].(map[string]any)
	if results["blobPath"] != "Files/a.txt" || results["contentLength"] != 5.0 || results["eTag"] != `"0xnew"` {
		t.Errorf("results = %s", jsonOf(results))
	}
	if results["message"] != "File uploaded successfully." {
		t.Errorf("message = %v", results["message"])
	}

	// overwrite=true drops If-None-Match and changes the message.
	env, isErr = call(t, cs, "onelake_upload-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/a.txt", "content": "hello", "overwrite": true,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if gotOverwrite != "" {
		t.Errorf("If-None-Match = %q, want empty when overwrite=true", gotOverwrite)
	}
	if env["results"].(map[string]any)["message"] != "File uploaded successfully (overwritten)." {
		t.Errorf("message = %v", env["results"].(map[string]any)["message"])
	}
}

func TestUploadFileLocalFilePath(t *testing.T) {
	var gotBody []byte
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusCreated)
	})
	cs := session(t, a)

	src := filepath.Join(t.TempDir(), "src.txt")
	if err := os.WriteFile(src, []byte("from disk"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, isErr := call(t, cs, "onelake_upload-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/a.txt", "local-file-path": src,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if string(gotBody) != "from disk" {
		t.Errorf("body = %q", gotBody)
	}
}

func TestUploadFileNoContent(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_upload-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/a.txt",
	})
	if !isErr || !strings.Contains(env["message"].(string), "Either --content or --local-file-path must be specified") {
		t.Errorf("no content: %s", jsonOf(env))
	}
}

func TestUploadFileLocalFileNotFound(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_upload-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/a.txt",
		"local-file-path": filepath.Join(t.TempDir(), "missing.txt"),
	})
	if !isErr || !strings.Contains(env["message"].(string), "Local file not found") {
		t.Errorf("missing local file: %s", jsonOf(env))
	}
}

func TestDeleteFile(t *testing.T) {
	var gotMethod, gotPath string
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod, gotPath = r.Method, r.URL.Path
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_delete-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/a.txt",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if gotMethod != http.MethodDelete || !strings.HasSuffix(gotPath, "/Files/a.txt") {
		t.Errorf("request = %s %s", gotMethod, gotPath)
	}
	results := env["results"].(map[string]any)
	if results["filePath"] != "Files/a.txt" || results["message"] != "File deleted successfully" {
		t.Errorf("results = %s", jsonOf(results))
	}
}

func TestDeleteFileNotFound(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "The specified path does not exist.", http.StatusNotFound)
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_delete-file", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "file-path": "Files/missing.txt",
	})
	if !isErr || env["status"] != 404.0 {
		t.Errorf("not found: %s", jsonOf(env))
	}
}

func TestCreateDirectory(t *testing.T) {
	var gotXMSResource string
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut || r.URL.Query().Get("resource") != "directory" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL)
		}
		gotXMSResource = r.Header.Get("x-ms-resource")
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_create-directory", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "directory-path": "Files/reports",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if gotXMSResource != "directory" {
		t.Errorf("x-ms-resource = %q", gotXMSResource)
	}
	results := env["results"].(map[string]any)
	want := map[string]any{
		"workspaceId": "ws", "itemId": "sales.Lakehouse", "directoryPath": "Files/reports",
		"success": true, "message": "Directory 'Files/reports' created successfully",
	}
	for k, v := range want {
		if results[k] != v {
			t.Errorf("results[%q] = %v, want %v", k, results[k], v)
		}
	}
}

func TestCreateDirectoryPathTraversal(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_create-directory", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "directory-path": "../etc",
	})
	if !isErr || env["status"] != 400.0 {
		t.Fatalf("path traversal: %s", jsonOf(env))
	}
	want := "Invalid argument: Path cannot contain directory traversal sequences. (Parameter 'directoryPath')"
	if !strings.HasPrefix(env["message"].(string), want) {
		t.Errorf("message = %q, want prefix %q", env["message"], want)
	}
}

func TestDeleteDirectory(t *testing.T) {
	var gotDeleteType, gotQuery string
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) {
		gotDeleteType = r.Header.Get("x-ms-delete-type")
		gotQuery = r.URL.RawQuery
	})
	cs := session(t, a)

	env, isErr := call(t, cs, "onelake_delete-directory", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "directory-path": "Files/reports", "recursive": true,
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if gotDeleteType != "directory" {
		t.Errorf("x-ms-delete-type = %q", gotDeleteType)
	}
	if q, _ := url.ParseQuery(gotQuery); q.Get("recursive") != "true" {
		t.Errorf("query = %q", gotQuery)
	}
	results := env["results"].(map[string]any)
	if results["message"] != "Directory and all contents deleted successfully" {
		t.Errorf("message = %v", results["message"])
	}

	env, isErr = call(t, cs, "onelake_delete-directory", map[string]any{
		"workspace": "ws", "item": "sales.Lakehouse", "directory-path": "Files/reports",
	})
	if isErr {
		t.Fatalf("unexpected error: %s", jsonOf(env))
	}
	if env["results"].(map[string]any)["message"] != "Directory deleted successfully" {
		t.Errorf("message = %v", env["results"].(map[string]any)["message"])
	}
}

func TestDeleteDirectoryValidation(t *testing.T) {
	a := fakeArea(t, func(w http.ResponseWriter, r *http.Request) { t.Error("no request expected") })
	cs := session(t, a)

	// directory-path is a required schema property (like file-path elsewhere);
	// it must be present so the mcp SDK's own schema validation doesn't
	// intercept the call before our workspace/item check ever runs.
	env, isErr := call(t, cs, "onelake_delete-directory", map[string]any{"directory-path": "Files/x"})
	want := "Workspace identifier is required. Provide --workspace or --workspace-id.\nItem identifier is required. Provide --item or --item-id."
	if !isErr || env["message"] != want {
		t.Errorf("missing both: %s", jsonOf(env))
	}
}
