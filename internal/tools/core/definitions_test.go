package core_test

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func realTempDir(t *testing.T) string {
	t.Helper()
	// macOS temp directories sit below the /var symlink, which upstream's
	// link check rejects.
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestCreateItemAccepted(t *testing.T) {
	items := "/v1/workspaces/" + ws1 + "/items"
	_, h := fake(t, map[string]reply{
		"POST " + items:                         {202, map[string]string{"x-ms-operation-id": op1, "Retry-After": "1"}, ""},
		"GET /v1/operations/" + op1:             {200, nil, `{"status":"Succeeded","percentComplete":100}`},
		"GET /v1/operations/" + op1 + "/result": {200, nil, `{"id":"` + item1 + `","displayName":"n","type":"Notebook","workspaceId":"` + ws1 + `"}`},
	})
	cs := session(t, staticCred{}, h)
	args := map[string]any{"workspace-id": ws1, "display-name": "n", "item-type": "Notebook"}

	env, isErr := call(t, cs, "core_create-item", args)
	expect(t, env, 202, "Success")
	if isErr || !sameJSON(env["results"], `{"operation":{"status":"Accepted","operationId":"`+op1+`","serverRetryAfterSeconds":1,"recommendedPollAfterSeconds":1}}`) {
		t.Errorf("results = %s (isError=%v)", jsonOf(env["results"]), isErr)
	}

	args["sync"] = true
	env, _ = call(t, cs, "core_create-item", args)
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"item":{"id":"`+item1+`","displayName":"n","type":"Notebook","workspaceId":"`+ws1+`"},`+
		`"operation":{"status":"Succeeded","operationId":"`+op1+`","lastState":{"status":"Succeeded","percentComplete":100}}}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
}

func TestCreateItemOperationFailed(t *testing.T) {
	_, h := fake(t, map[string]reply{
		"POST /v1/workspaces/" + ws1 + "/items": {202, map[string]string{"x-ms-operation-id": op1, "Retry-After": "1"}, ""},
		"GET /v1/operations/" + op1:             {200, nil, `{"status":"Failed","error":{"errorCode":"ItemDisplayNameAlreadyInUse"}}`},
	})
	cs := session(t, staticCred{}, h)
	env, isErr := call(t, cs, "core_create-item", map[string]any{"workspace-id": ws1, "display-name": "n", "item-type": "Notebook", "sync": true})
	const msg = "Fabric reports that the operation failed. Inspect the safe error code and request ID; the mutation was not resubmitted."
	expect(t, env, 502, msg)
	if !isErr || !sameJSON(env["results"], `{"operation":{"status":"Failed","operationId":"`+op1+`","lastState":{"status":"Failed","error":{"errorCode":"ItemDisplayNameAlreadyInUse"}},`+
		`"issue":{"code":"OperationFailed","message":"`+msg+`","httpStatus":502}}}`) {
		t.Errorf("results = %s (isError=%v)", jsonOf(env["results"]), isErr)
	}
}

func TestItemDefinition(t *testing.T) {
	item := "/v1/workspaces/" + ws1 + "/items/" + item1
	payload := base64.StdEncoding.EncodeToString([]byte("print(1)"))
	def := `{"definition":{"format":"ipynb","parts":[{"path":"notebook-content.py","payload":"` + payload + `","payloadType":"InlineBase64"},` +
		`{"path":".platform","payload":"","payloadType":"InlineBase64"}]}}`
	last, h := fake(t, map[string]reply{
		"POST " + item + "/getDefinition":       {200, nil, def},
		"GET /v1/operations/" + op1 + "/result": {200, nil, def},
		"POST " + item + "/updateDefinition":    {202, map[string]string{"x-ms-operation-id": op1}, ""},
	})
	cs := session(t, staticCred{}, h)

	env, _ := call(t, cs, "core_get-item-definition", map[string]any{"workspace-id": ws1, "item-id": item1, "format": "ipynb"})
	expect(t, env, 200, "Success")
	if q := last().query; q != "format=ipynb" {
		t.Errorf("query = %s", q)
	}
	res := env["results"].(map[string]any)
	if !sameJSON(res["definition"], def[len(`{"definition":`):len(def)-1]) {
		t.Errorf("definition = %s", jsonOf(res["definition"]))
	}

	out := filepath.Join(realTempDir(t), "export")
	env, _ = call(t, cs, "core_get-item-definition", map[string]any{"operation-id": op1, "output-directory": out})
	expect(t, env, 200, "Success")
	res = env["results"].(map[string]any)
	if res["outputDirectory"] != out || !sameJSON(res["operation"], `{"status":"Succeeded","operationId":"`+op1+`"}`) {
		t.Errorf("results = %s", jsonOf(res))
	}
	if b, err := os.ReadFile(filepath.Join(out, "notebook-content.py")); err != nil || string(b) != "print(1)" {
		t.Errorf("exported file = %q, %v", b, err)
	}
	if fi, err := os.Stat(filepath.Join(out, ".platform")); err != nil || fi.Size() != 0 {
		t.Errorf(".platform: %v", err)
	}
	// The destination must be new.
	env, _ = call(t, cs, "core_get-item-definition", map[string]any{"operation-id": op1, "output-directory": out})
	expect(t, env, 400, "Invalid definition export destination. Select a new absolute local directory below an existing parent, "+
		"using portable paths without links, UNC/device paths, or an existing destination. No files were saved."+help)

	env, _ = call(t, cs, "core_get-item-definition", map[string]any{"operation-id": op1, "item-id": item1})
	expect(t, env, 400, "--operation-id reads a completed definition result once and cannot be combined with --workspace-id, --item-id, --format, or --sync true.")

	// Saved files go up as InlineBase64 parts.
	dir := realTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "nb.py"), []byte("x = 1"), 0o600); err != nil {
		t.Fatal(err)
	}
	env, _ = call(t, cs, "core_update-item-definition", map[string]any{
		"workspace-id": ws1, "item-id": item1, "definition-directory": dir,
		"definition-files": `[{"partPath":"notebook-content.py","sourcePath":"nb.py"}]`, "update-metadata": true,
	})
	expect(t, env, 202, "Success")
	if !sameJSON(env["results"], `{"operation":{"status":"Accepted","operationId":"`+op1+`","recommendedPollAfterSeconds":3}}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	s := last()
	if s.query != "updateMetadata=true" || !sameJSON(s.body, `{"definition":{"parts":[{"path":"notebook-content.py","payload":"`+
		base64.StdEncoding.EncodeToString([]byte("x = 1"))+`","payloadType":"InlineBase64"}]}}`) {
		t.Errorf("request = %s %s", s.query, jsonOf(s.body))
	}

	env, _ = call(t, cs, "core_update-item-definition", map[string]any{"workspace-id": ws1, "item-id": item1})
	expect(t, env, 400, "Provide a complete replacement definition using exactly one inline or saved-file source.")
}
