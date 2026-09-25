//go:build live

package onelake

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/microsoft/fabric-sdk-go/fabric"
	fabcore "github.com/microsoft/fabric-sdk-go/fabric/core"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
)

// TestLiveFiles creates a Lakehouse in FABMCP_E2E_WORKSPACE, exercises every
// onelake file tool against it and deletes it again:
//
//	FABMCP_E2E_WORKSPACE=<id> go test -tags live ./internal/tools/onelake/ -run LiveFiles -v
//
// The expected result shapes (JSON key sets) asserted below were captured by
// probing the live upstream .NET reference server (fabmcp) for the same
// calls against a throwaway Lakehouse in the same workspace, using an
// elicitation-accepting client to get past its destructive-operation
// confirmation gate -- see the parity harness (internal/parity) for the
// non-destructive/error-path calls compared automatically against that
// reference server.
func TestLiveFiles(t *testing.T) {
	ws := os.Getenv("FABMCP_E2E_WORKSPACE")
	if ws == "" {
		t.Skip("FABMCP_E2E_WORKSPACE not set")
	}
	cred, err := auth.NewCredential()
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create our own Lakehouse to exercise writes against; friendly
	// "Name.Lakehouse" identifiers aren't accepted by the OneLake blob
	// endpoint (upstream doesn't resolve them either -- see resolveItem in
	// items.go), so every call below addresses it by item-id.
	client, err := fabric.NewClient(cred, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	items := fabcore.NewClientFactoryWithClient(*client).NewItemsClient()
	name := fmt.Sprintf("fabmcp_files_%d", time.Now().Unix())
	typ := fabcore.ItemTypeLakehouse
	created, err := items.CreateItem(ctx, ws, fabcore.CreateItemRequest{DisplayName: &name, Type: &typ}, nil)
	if err != nil {
		t.Fatalf("create lakehouse: %v", err)
	}
	if created.ID == nil {
		t.Fatal("create lakehouse: no ID in response")
	}
	itemID := *created.ID
	t.Cleanup(func() {
		if _, err := items.DeleteItem(context.Background(), ws, itemID, nil); err != nil {
			t.Errorf("cleanup: delete lakehouse %s: %v", itemID, err)
		}
	})

	a := New(cred)
	cs := session(t, a)
	args := map[string]any{"workspace-id": ws, "item-id": itemID}
	withPath := func(extra map[string]any) map[string]any {
		m := make(map[string]any, len(args)+len(extra))
		for k, v := range args {
			m[k] = v
		}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}

	// A fresh Lakehouse has neither a Files nor a Tables folder yet, so
	// intelligent discovery (no path) finds nothing.
	env, isErr := call(t, cs, "onelake_list-files", withPath(nil))
	if isErr {
		t.Fatalf("list-files (empty): %s", jsonOf(env))
	}
	assertKeys(t, "list-files results", env["results"], "items", "rawResponse")
	if items := env["results"].(map[string]any)["items"].([]any); len(items) != 0 {
		t.Errorf("list-files (empty): items = %s, want []", jsonOf(items))
	}

	// create-directory
	env, isErr = call(t, cs, "onelake_create-directory", withPath(map[string]any{"directory-path": "Files/livetest"}))
	if isErr {
		t.Fatalf("create-directory: %s", jsonOf(env))
	}
	assertKeys(t, "create-directory results", env["results"], "workspaceId", "itemId", "directoryPath", "success", "message")
	results := env["results"].(map[string]any)
	if results["success"] != true || results["message"] != "Directory 'Files/livetest' created successfully" {
		t.Errorf("create-directory: %s", jsonOf(results))
	}

	// upload-file. content-type is set explicitly so the download below
	// exercises the inline text-decoding path (an untyped upload defaults to
	// application/octet-stream, which isTextContent correctly excludes).
	const content = "hello from the fabric-mcp-go live test"
	env, isErr = call(t, cs, "onelake_upload-file", withPath(map[string]any{
		"file-path": "Files/livetest/hello.txt", "content": content, "content-type": "text/plain",
	}))
	if isErr {
		t.Fatalf("upload-file: %s", jsonOf(env))
	}
	assertKeys(t, "upload-file results", env["results"],
		"workspaceId", "itemId", "blobPath", "contentLength", "contentType", "eTag", "lastModified",
		"requestId", "version", "requestServerEncrypted", "contentMd5", "contentCrc64", "encryptionScope",
		"encryptionKeySha256", "versionId", "clientRequestId", "rootActivityId", "message")
	results = env["results"].(map[string]any)
	// Like upstream's BlobPutCommand, the envelope reports 201 Created.
	if env["status"] != 201.0 {
		t.Errorf("upload-file: status = %v, want 201", env["status"])
	}
	if results["blobPath"] != "Files/livetest/hello.txt" || results["contentLength"] != float64(len(content)) {
		t.Errorf("upload-file: %s", jsonOf(results))
	}
	if results["message"] != "File uploaded successfully." {
		t.Errorf("upload-file message = %v", results["message"])
	}
	if results["eTag"] == nil {
		t.Errorf("upload-file: eTag missing")
	}

	// list-files (path-scoped) should now see the uploaded file.
	env, isErr = call(t, cs, "onelake_list-files", withPath(map[string]any{"path": "Files/livetest"}))
	if isErr {
		t.Fatalf("list-files (livetest): %s", jsonOf(env))
	}
	fileItems := env["results"].(map[string]any)["items"].([]any)
	if len(fileItems) != 1 {
		t.Fatalf("list-files (livetest): items = %s, want 1 entry", jsonOf(fileItems))
	}
	assertKeys(t, "list-files item", fileItems[0],
		"name", "path", "type", "size", "lastModified", "contentType", "etag", "permissions", "owner", "group",
		"isDirectory", "children")
	item := fileItems[0].(map[string]any)
	if item["name"] != "hello.txt" || item["size"] != float64(len(content)) || item["isDirectory"] != false {
		t.Errorf("list-files (livetest) item: %s", jsonOf(item))
	}

	// download-file
	env, isErr = call(t, cs, "onelake_download-file", withPath(map[string]any{"file-path": "Files/livetest/hello.txt"}))
	if isErr {
		t.Fatalf("download-file: %s", jsonOf(env))
	}
	assertKeys(t, "download-file results", env["results"], "blob", "message")
	blob := env["results"].(map[string]any)["blob"].(map[string]any)
	assertKeys(t, "download-file blob",
		env["results"].(map[string]any)["blob"],
		"workspaceId", "itemId", "path", "contentLength", "contentType", "charset", "contentEncoding",
		"contentLanguage", "contentDisposition", "contentMd5", "contentCrc64", "contentBase64", "contentText",
		"etag", "lastModified", "requestServerEncrypted", "encryptionScope", "encryptionKeySha256", "version",
		"versionId", "requestId", "clientRequestId", "rootActivityId", "contentFilePath", "inlineContentTruncated")
	if blob["contentText"] != content {
		t.Errorf("download-file: contentText = %v, want %q", blob["contentText"], content)
	}
	if want := base64.StdEncoding.EncodeToString([]byte(content)); blob["contentBase64"] != want {
		t.Errorf("download-file: contentBase64 = %v, want %q", blob["contentBase64"], want)
	}
	if env["results"].(map[string]any)["message"] != "File retrieved successfully." {
		t.Errorf("download-file message = %v", env["results"].(map[string]any)["message"])
	}

	// delete-file
	env, isErr = call(t, cs, "onelake_delete-file", withPath(map[string]any{"file-path": "Files/livetest/hello.txt"}))
	if isErr {
		t.Fatalf("delete-file: %s", jsonOf(env))
	}
	assertKeys(t, "delete-file results", env["results"], "filePath", "message")
	results = env["results"].(map[string]any)
	if results["filePath"] != "Files/livetest/hello.txt" || results["message"] != "File deleted successfully" {
		t.Errorf("delete-file: %s", jsonOf(results))
	}

	// delete-directory (now empty, non-recursive)
	env, isErr = call(t, cs, "onelake_delete-directory", withPath(map[string]any{"directory-path": "Files/livetest"}))
	if isErr {
		t.Fatalf("delete-directory: %s", jsonOf(env))
	}
	assertKeys(t, "delete-directory results", env["results"], "directoryPath", "message")
	results = env["results"].(map[string]any)
	if results["directoryPath"] != "Files/livetest" || results["message"] != "Directory deleted successfully" {
		t.Errorf("delete-directory: %s", jsonOf(results))
	}

	// Cleanup left the Lakehouse's Files folder empty again.
	env, isErr = call(t, cs, "onelake_list-files", withPath(map[string]any{"path": "Files"}))
	if isErr {
		t.Fatalf("list-files (post-cleanup): %s", jsonOf(env))
	}
	if got := env["results"].(map[string]any)["items"].([]any); len(got) != 0 {
		t.Errorf("list-files (post-cleanup): items = %s, want []", jsonOf(got))
	}
}

// assertKeys fails the test unless v is a JSON object with exactly the given
// keys (order-independent).
func assertKeys(t *testing.T, label string, v any, want ...string) {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("%s: not an object: %s", label, jsonOf(v))
	}
	var got []string
	for k := range m {
		got = append(got, k)
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("%s: keys = %v, want %v", label, got, want)
	}
}
