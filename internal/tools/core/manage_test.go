package core_test

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
)

const (
	ws1   = "11111111-1111-1111-1111-111111111111"
	item1 = "22222222-2222-2222-2222-222222222222"
	fold1 = "33333333-3333-3333-3333-333333333333"
	fold2 = "44444444-4444-4444-4444-444444444444"
	cap1  = "55555555-5555-5555-5555-555555555555"
	op1   = "66666666-6666-6666-6666-666666666666"
	help  = ". To mitigate this issue, please refer to the troubleshooting guidelines here at https://aka.ms/azmcp/troubleshooting."
)

// reply is one canned Fabric API response.
type reply struct {
	status int
	header map[string]string
	body   string
}

// seen is the last request the fake received.
type seen struct {
	method, path, query string
	body                map[string]any
}

// fake serves canned replies keyed by "METHOD /path" and records requests.
func fake(t *testing.T, replies map[string]reply) (func(args ...any) *seen, http.HandlerFunc) {
	t.Helper()
	var last seen
	var count int
	h := func(w http.ResponseWriter, r *http.Request) {
		count++
		last = seen{method: r.Method, path: r.URL.Path, query: r.URL.RawQuery}
		if b, _ := io.ReadAll(r.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &last.body)
		}
		rep, ok := replies[r.Method+" "+r.URL.Path]
		if !ok {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusTeapot)
			return
		}
		if rep.body != "" {
			w.Header().Set("Content-Type", "application/json")
		}
		for k, v := range rep.header {
			w.Header().Set(k, v)
		}
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}
	return func(...any) *seen {
		if count == 0 {
			return nil
		}
		return &last
	}, h
}

func expect(t *testing.T, env map[string]any, status int, message string) {
	t.Helper()
	if got := int(env["status"].(float64)); got != status {
		t.Errorf("status = %d, want %d (message %q)", got, status, env["message"])
	}
	if message != "" && env["message"] != message {
		t.Errorf("message =\n\t%q\nwant\n\t%q", env["message"], message)
	}
}

func TestWorkspaces(t *testing.T) {
	last, h := fake(t, map[string]reply{
		"GET /v1/workspaces":                               {200, nil, `{"value":[{"id":"` + ws1 + `","displayName":"W","type":"Workspace","extra":1,"tags":[]}],"continuationToken":"a b","continuationUri":"u"}`},
		"GET /v1/workspaces/" + ws1:                        {429, map[string]string{"Retry-After": "30"}, ""},
		"POST /v1/workspaces":                              {201, map[string]string{"Location": "https://x/v1/workspaces/" + ws1}, `{"id":"` + ws1 + `","displayName":"New"}`},
		"PATCH /v1/workspaces/" + ws1:                      {409, map[string]string{"Retry-After": "5"}, `{"errorCode":"X"}`},
		"DELETE /v1/workspaces/" + ws1:                     {200, nil, ""},
		"POST /v1/workspaces/" + ws1 + "/assignToCapacity": {403, nil, ""},
		"GET /v1/capacities":                               {200, nil, `{"value":[{"id":"` + cap1 + `","displayName":"C","sku":"F2","region":"West","state":"Active"}]}`},
		"GET /v1/capacities/" + cap1:                       {200, nil, `{"id":"` + cap1 + `","displayName":"C","sku":"F2","region":"West","state":""}`},
	})
	cs := session(t, staticCred{}, h)

	env, _ := call(t, cs, "core_list-workspaces", map[string]any{"roles": "viewer, ADMIN,Viewer", "continuation-token": "x y%2F", "prefer-workspace-specific-endpoints": true})
	expect(t, env, 200, "Success")
	if q := last().query; q != "roles=Viewer%2CAdmin&continuationToken=x%20y%2F&preferWorkspaceSpecificEndpoints=true" {
		t.Errorf("query = %s", q)
	}
	if !sameJSON(env["results"], `{"workspaces":[{"id":"`+ws1+`","displayName":"W","type":"Workspace","tags":[]}],"continuationToken":"a b","continuationUri":"u"}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "core_list-workspaces", map[string]any{"roles": "Admin,,Viewer", "continuation-token": " "})
	expect(t, env, 400, "--roles must be a comma-separated list of Admin, Member, Contributor, or Viewer without empty entries.\n--continuation-token must not be empty or whitespace. Omit it to request the first page.")

	env, _ = call(t, cs, "core_get-workspace", map[string]any{"workspace-id": ws1})
	expect(t, env, 429, "Fabric throttled the request. Wait at least 30 seconds before retrying"+help)
	if _, ok := env["results"]; ok {
		t.Error("error envelope has results")
	}
	env, _ = call(t, cs, "core_get-workspace", map[string]any{"workspace-id": "nope"})
	expect(t, env, 400, "--workspace-id must be a nonempty UUID.")

	env, _ = call(t, cs, "core_create-workspace", map[string]any{"display-name": "New", "capacity-id": strings.ToUpper(cap1)})
	expect(t, env, 201, "Success")
	if b := last().body; b["capacityId"] != cap1 || b["displayName"] != "New" || len(b) != 2 {
		t.Errorf("body = %v", b)
	}
	if !sameJSON(env["results"], `{"workspace":{"id":"`+ws1+`","displayName":"New"},"location":"https://x/v1/workspaces/`+ws1+`"}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "core_create-workspace", map[string]any{"display-name": " admin monitoring "})
	expect(t, env, 400, "--display-name cannot use the reserved workspace name 'Admin monitoring'.")

	env, _ = call(t, cs, "core_update-workspace", map[string]any{"workspace-id": ws1, "description": ""})
	expect(t, env, 409, "The update conflicts with the workspace state or an existing workspace name. Workspace names must be unique within the tenant"+help)
	if b := last().body; len(b) != 1 || b["description"] != "" {
		t.Errorf("body = %v", b)
	}
	env, _ = call(t, cs, "core_update-workspace", map[string]any{"workspace-id": ws1})
	expect(t, env, 400, "Provide at least one update: --display-name or --description. An empty description clears it.")

	env, _ = call(t, cs, "core_delete-workspace", map[string]any{"workspace-id": ws1})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"deleted":true,"workspaceId":"`+ws1+`"}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}

	env, _ = call(t, cs, "core_assign-workspace-to-capacity", map[string]any{"workspace-id": ws1, "capacity-id": cap1})
	const denied = "Access denied. Workspace Admin and capacity Contributor or Admin permissions are required, along with Capacity.ReadWrite.All and Workspace.ReadWrite.All scopes."
	expect(t, env, 403, denied+help)
	if !sameJSON(env["results"], `{"message":"`+denied+`","type":"WorkspaceCapacityAssignmentException"}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}

	env, _ = call(t, cs, "core_list-capacities", nil)
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"capacities":[{"id":"`+cap1+`","displayName":"C","sku":"F2","region":"West","state":"Active"}]}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "core_get-capacity", map[string]any{"capacity-id": cap1})
	expect(t, env, 502, "Fabric returned an invalid capacity metadata response"+help)
}

func TestAssignWorkspaceAccepted(t *testing.T) {
	last, h := fake(t, map[string]reply{"POST /v1/workspaces/" + ws1 + "/assignToCapacity": {202, nil, ""}})
	cs := session(t, staticCred{}, h)
	env, _ := call(t, cs, "core_assign-workspace-to-capacity", map[string]any{"workspace-id": ws1, "capacity-id": cap1})
	expect(t, env, 202, "Request accepted. Capacity assignment is pending; completion has not been verified.")
	if b := last().body; b["capacityId"] != cap1 {
		t.Errorf("body = %v", b)
	}
	if !sameJSON(env["results"], `{"accepted":true,"capacityId":"`+cap1+`","state":"Pending","workspaceId":"`+ws1+`"}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
}

func TestItems(t *testing.T) {
	base := "/v1/workspaces/" + ws1 + "/items"
	last, h := fake(t, map[string]reply{
		"GET " + base:                          {200, nil, `{"value":[{"id":"` + item1 + `","displayName":"I","type":"Notebook","workspaceId":"` + ws1 + `","folderId":"` + fold1 + `"}]}`},
		"GET " + base + "/" + item1:            {200, nil, `{"id":"` + item1 + `","displayName":"I","type":"Notebook","workspaceId":"` + ws2() + `"}`},
		"PATCH " + base + "/" + item1:          {200, nil, `{"id":"` + strings.ToUpper(item1) + `","displayName":"J","type":"Notebook","workspaceId":"` + ws1 + `"}`},
		"DELETE " + base + "/" + item1:         {429, nil, ""},
		"POST " + base + "/" + item1 + "/move": {404, map[string]string{"Retry-After": "7"}, `{"errorCode":"ItemNotFound","message":"secret"}`},
	})
	cs := session(t, staticCred{}, h)

	env, _ := call(t, cs, "core_list-items", map[string]any{"workspace-id": ws1, "type": "Data Pipeline", "root-folder-id": fold1})
	expect(t, env, 200, "Success")
	if q := last().query; q != "type=Data%20Pipeline&recursive=true&rootFolderId="+fold1 {
		t.Errorf("query = %s", q)
	}
	if !sameJSON(env["results"], `{"items":[{"id":"`+item1+`","displayName":"I","type":"Notebook","workspaceId":"`+ws1+`","folderId":"`+fold1+`"}]}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}

	env, _ = call(t, cs, "core_get-item", map[string]any{"workspace-id": ws1, "item-id": item1})
	expect(t, env, 502, "Fabric returned an invalid item metadata response"+help)

	env, _ = call(t, cs, "core_update-item", map[string]any{"workspace-id": ws1, "item-id": item1, "display-name": "J"})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"item":{"id":"`+strings.ToUpper(item1)+`","displayName":"J","type":"Notebook","workspaceId":"`+ws1+`"}}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "core_update-item", map[string]any{"workspace-id": ws1, "item-id": item1})
	expect(t, env, 400, "Provide at least one of --display-name or --description.")

	env, _ = call(t, cs, "core_delete-item", map[string]any{"workspace-id": ws1, "item-id": item1, "hard-delete": false})
	expect(t, env, 429, "Fabric throttled the deletion request. Wait before retrying; the request was not automatically retried"+help)
	if q := last().query; q != "hardDelete=false" {
		t.Errorf("query = %s", q)
	}

	env, _ = call(t, cs, "core_move-item", map[string]any{"workspace-id": ws1, "item-id": item1})
	expect(t, env, 404, "The Fabric workspace, item, or destination folder was not found, or the caller does not have access. "+
		"No automatic retry was attempted. Fabric error code: ItemNotFound. Wait at least 7 seconds before another request"+help)
	if b := last().body; len(b) != 0 {
		t.Errorf("body = %v", b)
	}
}

func ws2() string { return "77777777-7777-7777-7777-777777777777" }

func TestMoveItem(t *testing.T) {
	_, h := fake(t, map[string]reply{
		"POST /v1/workspaces/" + ws1 + "/items/" + item1 + "/move": {200, nil, `{"value":[{"type":"Notebook","id":"` + item1 + `","folderId":"` + fold1 + `"}]}`},
	})
	cs := session(t, staticCred{}, h)
	env, _ := call(t, cs, "core_move-item", map[string]any{"workspace-id": ws1, "item-id": item1, "target-folder-id": fold1})
	expect(t, env, 200, "Fabric confirmed item movement (HTTP 200). Items is the complete returned metadata collection, including any returned child items, in service order. An empty collection or absent fields do not establish which items were affected. No lookup, follow-up read, polling, or automatic retry was performed.")
	res := env["results"].(map[string]any)
	if res["moveStatus"] != "Moved" || !sameJSON(res["items"], `[{"type":"Notebook","id":"`+item1+`","folderId":"`+fold1+`"}]`) {
		t.Errorf("results = %s", jsonOf(res))
	}
	// A moved item in a different folder contradicts the request.
	env, _ = call(t, cs, "core_move-item", map[string]any{"workspace-id": ws1, "item-id": item1})
	expect(t, env, 502, "Fabric confirmed item movement (HTTP 200), but the returned item collection could not be read or validated. Do not resubmit this move; inspect the item separately. No automatic retry was attempted"+help)
}

func TestFolders(t *testing.T) {
	base := "/v1/workspaces/" + ws1 + "/folders"
	f1 := `{"id":"` + fold1 + `","displayName":"F","workspaceId":"` + ws1 + `"}`
	last, h := fake(t, map[string]reply{
		"GET " + base:                          {200, nil, `{"value":[` + f1 + `],"continuationToken":"t+1","continuationUri":"https://HOST` + base + `?continuationToken=t%2B1&recursive=False"}`},
		"GET " + base + "/" + fold1:            {200, nil, f1},
		"POST " + base:                         {201, nil, `{"id":"` + fold2 + `","displayName":"G","workspaceId":"` + ws1 + `","parentFolderId":"` + fold1 + `"}`},
		"PATCH " + base + "/" + fold1:          {409, nil, `{"errorCode":"FolderDisplayNameAlreadyInUse"}`},
		"POST " + base + "/" + fold1 + "/move": {200, nil, `{"id":"` + fold1 + `","displayName":"F","workspaceId":"` + ws1 + `","parentFolderId":"` + fold2 + `"}`},
		"DELETE " + base + "/" + fold1:         {400, nil, `{"errorCode":"FolderNotEmpty"}`},
	})
	cs := session(t, staticCred{}, h)

	env, _ := call(t, cs, "core_list-folders", map[string]any{"workspace-id": ws1, "recursive": false})
	// The fake's host differs from the continuation URI's, so the page is rejected.
	expect(t, env, 502, "Fabric returned an invalid folder page. Expected matching workspace metadata and consistent continuation information as JSON up to 1 MiB and 64 nesting levels. No partial page was returned"+help)
	if q := last().query; q != "recursive=false" {
		t.Errorf("query = %s", q)
	}

	env, _ = call(t, cs, "core_get-folder", map[string]any{"workspace-id": ws1, "folder-id": fold1})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"folder":`+f1+`}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}

	env, _ = call(t, cs, "core_create-folder", map[string]any{"workspace-id": ws1, "display-name": "G", "parent-folder-id": fold1})
	expect(t, env, 201, "Fabric confirmed folder creation (HTTP 201). No lookup, follow-up read, polling, or automatic retry was performed. Do not resubmit this creation.")
	if b := last().body; b["parentFolderId"] != fold1 || b["displayName"] != "G" {
		t.Errorf("body = %v", b)
	}
	env, _ = call(t, cs, "core_create-folder", map[string]any{"workspace-id": ws1, "display-name": "a.b"})
	expect(t, env, 400, `--display-name must be valid Unicode with 1 to 255 characters, no leading or trailing spaces, C0/C1 control codes, or ~"#.&*:<>?/{|} characters. Reserved names $recycle.bin, recycled, and recycler are not allowed.`)

	env, _ = call(t, cs, "core_update-folder", map[string]any{"workspace-id": ws1, "folder-id": fold1, "display-name": "H"})
	expect(t, env, 409, "Fabric rejected the folder update because of a conflict. A duplicate name remains a failure; no replacement name was generated. No automatic retry was attempted. Fabric error code: FolderDisplayNameAlreadyInUse"+help)

	env, _ = call(t, cs, "core_move-folder", map[string]any{"workspace-id": ws1, "folder-id": fold1, "target-folder-id": fold2})
	expect(t, env, 200, "")
	env, _ = call(t, cs, "core_move-folder", map[string]any{"workspace-id": ws1, "folder-id": fold1, "target-folder-id": fold1})
	expect(t, env, 400, "--target-folder-id cannot identify the folder being moved.")

	env, _ = call(t, cs, "core_delete-folder", map[string]any{"workspace-id": ws1, "folder-id": fold1})
	expect(t, env, 400, "Fabric rejected the folder deletion. Check the explicit workspace and folder UUIDs; only empty folders can be deleted. No automatic retry or change to folder contents was attempted. Fabric error code: FolderNotEmpty. Fabric requires an empty folder; items, including soft-deleted items, or nested folders prevent deletion"+help)
	env, _ = call(t, cs, "core_delete-folder", map[string]any{"workspace-id": ws1})
	expect(t, env, 400, "Missing Required options: --folder-id No DELETE request was sent.")
	env, _ = call(t, cs, "core_get-folder", map[string]any{"workspace-id": ws1, "folder-id": " "})
	expect(t, env, 400, "Invalid Fabric Core request. Check option names and values; Boolean options must be true or false.")
}

func TestFolderNotSent(t *testing.T) {
	_, h := fake(t, nil)
	cred := staticCred{err: &auth.CredentialError{Err: errors.New("no credential"), Unavailable: true}}
	cs := session(t, cred, h)
	env, _ := call(t, cs, "core_create-folder", map[string]any{"workspace-id": ws1, "display-name": "G"})
	expect(t, env, 401, "Authentication failed. Check the configured Fabric identity. The folder creation request was not sent. No automatic retry was attempted"+help)
}

func TestOperations(t *testing.T) {
	_, h := fake(t, map[string]reply{
		"GET /v1/operations/" + op1:             {200, map[string]string{"Retry-After": "2", "x-ms-operation-id": op1}, `{"status":"Failed","createdTimeUtc":"2024-01-02T03:04:05.5Z","percentComplete":100,"error":{"errorCode":"bad code!","requestId":"` + ws1 + `"}}`},
		"GET /v1/operations/" + op1 + "/result": {200, nil, ""},
	})
	cs := session(t, staticCred{}, h)
	env, _ := call(t, cs, "core_get-operation-state", map[string]any{"operation-id": op1})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"operationId":"`+op1+`","state":{"status":"Failed","createdTimeUtc":"2024-01-02T03:04:05.5+00:00","percentComplete":100,"error":{"requestId":"`+ws1+`"}},"serverRetryAfterSeconds":2,"recommendedPollAfterSeconds":2}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "core_get-operation-result", map[string]any{"operation-id": op1})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"operationId":"`+op1+`","hasBody":false}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "core_get-operation-result", map[string]any{"operation-id": "00000000-0000-0000-0000-000000000000"})
	expect(t, env, 400, "--operation-id must be a nonempty UUID.")
}

// sameJSON reports whether v marshals to the same JSON value as want.
func sameJSON(v any, want string) bool {
	var w any
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		panic(err)
	}
	return jsonOf(v) == jsonOf(w)
}

// Fabric's operation states carry timestamps without an offset, which
// upstream's DateTimeOffset reads as local time.
func TestOperationStateLocalTime(t *testing.T) {
	_, h := fake(t, map[string]reply{
		"GET /v1/operations/" + op1: {200, nil, `{"status":"Running","createdTimeUtc":"2026-11-14T11:33:20.6490585","error":null}`},
	})
	cs := session(t, staticCred{}, h)
	env, _ := call(t, cs, "core_get-operation-state", map[string]any{"operation-id": op1})
	expect(t, env, 200, "Success")
	got := env["results"].(map[string]any)["state"].(map[string]any)["createdTimeUtc"].(string)
	if !strings.HasPrefix(got, "2026-11-14T11:33:20.6490585") || len(got) != len("2026-11-14T11:33:20.6490585+00:00") {
		t.Errorf("createdTimeUtc = %q", got)
	}
}
