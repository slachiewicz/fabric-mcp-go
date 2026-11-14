package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// This file ports the folder commands.

// folder ports FabricFolder.
type folder struct {
	ID             uuid.UUID  `json:"id"`
	DisplayName    *string    `json:"displayName"`
	WorkspaceID    uuid.UUID  `json:"workspaceId"`
	ParentFolderID *uuid.UUID `json:"parentFolderId,omitempty"`
}

const invalidFolderName = "--display-name must be valid Unicode with 1 to 255 characters, no leading or trailing spaces, C0/C1 control codes, or ~\"#.&*:<>?/{|} characters. Reserved names $recycle.bin, recycled, and recycler are not allowed."

// validFolderName ports FolderDisplayName.IsValid.
func validFolderName(name string) bool {
	if isBlank(name) || !utf8.ValidString(name) {
		return false
	}
	first, _ := utf8.DecodeRuneInString(name)
	last, _ := utf8.DecodeLastRuneInString(name)
	if unicode.IsSpace(first) || unicode.IsSpace(last) {
		return false
	}
	for _, reserved := range []string{"$recycle.bin", "recycled", "recycler"} {
		if strings.EqualFold(name, reserved) {
			return false
		}
	}
	n := 0
	for _, r := range name {
		n++
		if n > 255 || unicode.IsControl(r) || r < 128 && strings.ContainsRune("~\"#.&*:<>?/{|}", r) {
			return false
		}
	}
	return true
}

// readFolder ports FabricCoreService.ReadFolder.
func readFolder(b []byte, ws uuid.UUID, id *uuid.UUID, msg string) (*folder, error) {
	if validateMetadataJSON(b) != nil {
		return nil, jsonError(msg)
	}
	var f *folder
	if json.Unmarshal(b, &f) != nil || f == nil || f.WorkspaceID != ws || f.DisplayName == nil ||
		f.ParentFolderID != nil && *f.ParentFolderID == uuid.Nil {
		return nil, jsonError(msg)
	}
	if id != nil && f.ID != *id || id == nil && f.ID == uuid.Nil {
		return nil, jsonError(msg)
	}
	return f, nil
}

// folderResult is the shared shape of the folder mutation results.
type folderResult struct {
	Folder            *folder  `json:"folder"`
	RetryAfterSeconds *int64   `json:"retryAfterSeconds,omitempty"`
	RetryAfterUtc     *netTime `json:"retryAfterUtc,omitempty"`
}

// --- list-folders ---

const invalidFolderPage = "Fabric returned invalid folder page metadata. No partial page was returned."

func (a *Area) listFolders(ctx context.Context, _ *mcp.CallToolRequest, in listFoldersInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	root, ok := parseOptID(in.RootFolderID)
	if !ok {
		errs = append(errs, "--root-folder-id must be a nonempty UUID.")
	}
	if !validToken(in.ContinuationToken) {
		errs = append(errs, invalidTokenMessage)
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	collection := a.base + "/workspaces/" + ws.String() + "/folders"
	rootID := ""
	if root != nil {
		rootID = root.String()
	}
	type result struct {
		Folders           []*folder `json:"folders"`
		ContinuationToken *string   `json:"continuationToken,omitempty"`
		RetryAfterSeconds *int64    `json:"retryAfterSeconds,omitempty"`
		RetryAfterUtc     *netTime  `json:"retryAfterUtc,omitempty"`
	}
	res, err := func() (*result, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/workspaces/"+ws.String()+"/folders"+query(
			"rootFolderId", rootID, "recursive", optBool(in.Recursive), "continuationToken", optToken(in.ContinuationToken)), nil)
		if err != nil {
			return nil, err
		}
		r := parseRetryAfter(resp.Header)
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			f := httpError(nonSuccess(resp.StatusCode), "Unable to list Fabric folders.")
			f.cls, f.retry = "folderList", r
			return nil, f
		}
		if !isJSON(resp) {
			discard(resp)
			return nil, jsonError("Fabric returned a non-JSON folder page.")
		}
		b, err := readBody(resp, maxJSONBytes, func() error {
			return jsonError("The Fabric folder page exceeds the 1 MiB (1,048,576 byte) limit. No partial page was returned.")
		})
		if err != nil {
			return nil, err
		}
		u, _ := url.Parse(collection)
		folders, token, err := readFolderPage(b, u, ws, root, in.Recursive == nil || *in.Recursive)
		if err != nil {
			return nil, err
		}
		return &result{folders, token, r.seconds(), r.utc()}, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		msg := messages{
			http.StatusBadRequest:      "Check the workspace and root folder UUIDs, recursive Boolean, and unchanged continuation token",
			http.StatusUnauthorized:    "Authentication failed. Use the configured identity with permission to read the Fabric workspace",
			http.StatusForbidden:       "Access denied. The caller needs workspace Viewer or higher access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All",
			http.StatusNotFound:        "The Fabric workspace or root folder was not found. Check its ID and the caller's access",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric folder listing was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric folder listing timed out",
			http.StatusBadGateway:      "Fabric returned an invalid folder page. Expected matching workspace metadata and consistent continuation information as JSON up to 1 MiB and 64 nesting levels. No partial page was returned",
			0:                          "Unable to list Fabric folders. Retry the request or check service availability",
		}.get(status)
		if f.cls == "folderList" && f.retry != nil {
			msg += f.retry.retryGuidance()
		}
		return fail(status, msg), nil, nil
	}
	return response.Success(res), nil, nil
}

func readFolderPage(b []byte, collection *url.URL, ws uuid.UUID, root *uuid.UUID, recursive bool) ([]*folder, *string, error) {
	if validateMetadataJSON(b) != nil {
		return nil, nil, jsonError(invalidFolderPage)
	}
	var page map[string]json.RawMessage
	if json.Unmarshal(b, &page) != nil {
		return nil, nil, jsonError(invalidFolderPage)
	}
	var value []json.RawMessage
	if raw, ok := page["value"]; !ok || json.Unmarshal(raw, &value) != nil || value == nil {
		return nil, nil, jsonError(invalidFolderPage)
	}
	token, err := readFolderToken(page, collection, root, recursive)
	if err != nil {
		return nil, nil, err
	}
	folders := []*folder{}
	for _, raw := range value {
		var f *folder
		if json.Unmarshal(raw, &f) != nil || f == nil || f.ID == uuid.Nil || f.WorkspaceID != ws ||
			f.DisplayName == nil || isBlank(*f.DisplayName) || f.ParentFolderID != nil && *f.ParentFolderID == uuid.Nil {
			return nil, nil, jsonError(invalidFolderPage)
		}
		folders = append(folders, f)
	}
	return folders, token, nil
}

func present(page map[string]json.RawMessage, key string) (json.RawMessage, bool) {
	raw, ok := page[key]
	return raw, ok && string(raw) != "null"
}

// readFolderToken ports ReadFolderContinuationToken.
func readFolderToken(page map[string]json.RawMessage, collection *url.URL, root *uuid.UUID, recursive bool) (*string, error) {
	tokRaw, hasToken := present(page, "continuationToken")
	uriRaw, hasURI := present(page, "continuationUri")
	if !hasToken {
		if hasURI {
			return nil, jsonError(invalidFolderPage)
		}
		return nil, nil
	}
	var tok string
	if json.Unmarshal(tokRaw, &tok) != nil || !validToken(&tok) {
		return nil, jsonError(invalidFolderPage)
	}
	if hasURI {
		var uri string
		if json.Unmarshal(uriRaw, &uri) != nil || !folderContinuationURI(uri, collection, tok, root, recursive) {
			return nil, jsonError(invalidFolderPage)
		}
	}
	return &tok, nil
}

// folderContinuationURI ports IsFolderContinuationUri.
func folderContinuationURI(value string, collection *url.URL, tok string, root *uuid.UUID, recursive bool) bool {
	u := matchingCollectionURI(value, collection, true)
	if u == nil {
		return false
	}
	q := map[string]string{}
	for _, pair := range strings.Split(u.RawQuery, "&") {
		i := strings.IndexByte(pair, '=')
		if i <= 0 {
			return false
		}
		v, err := url.PathUnescape(strings.ReplaceAll(pair[i+1:], "+", " "))
		if err != nil {
			return false
		}
		if _, dup := q[pair[:i]]; dup {
			return false
		}
		q[pair[:i]] = v
	}
	for k := range q {
		if k != "continuationToken" && k != "rootFolderId" && k != "recursive" {
			return false
		}
	}
	want, _ := url.PathUnescape(encodeToken(tok))
	if got, ok := q["continuationToken"]; !ok || got != want {
		return false
	}
	if r, ok := q["rootFolderId"]; ok {
		id, valid := parseID(r)
		if !valid || root == nil || id != *root {
			return false
		}
	}
	n, ok := q["recursive"]
	return !ok || strings.EqualFold(n, boolString(recursive))
}

// --- get-folder ---

func (a *Area) getFolder(ctx context.Context, _ *mcp.CallToolRequest, in getFolderInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	id, ok := parseID(in.FolderID)
	if !ok {
		errs = append(errs, "--folder-id must be a nonempty UUID.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	res, err := func() (*folderResult, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/workspaces/"+ws.String()+"/folders/"+id.String(), nil)
		if err != nil {
			return nil, err
		}
		r := parseRetryAfter(resp.Header)
		exc := func(status int) *failure {
			f := httpError(status, "Unable to retrieve Fabric folder metadata.")
			f.cls, f.retry = "folderGet", r
			return f
		}
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				discard(resp)
				return nil, exc(http.StatusBadGateway)
			}
			f := exc(resp.StatusCode)
			f.code, f.metaUnavailable = readErrorCode(resp)
			return nil, f
		}
		if !isJSON(resp) {
			discard(resp)
			return nil, exc(http.StatusBadGateway)
		}
		b, err := readBody(resp, maxJSONBytes, func() error { return jsonError("") })
		if err != nil {
			if ctx.Err() != nil {
				return nil, exc(http.StatusRequestTimeout)
			}
			if f := asFailure(err); f.kind != kJSON {
				return nil, exc(http.StatusServiceUnavailable)
			}
			return nil, exc(http.StatusBadGateway)
		}
		fo, err := readFolder(b, ws, &id, "Fabric returned invalid folder metadata.")
		if err != nil {
			return nil, exc(http.StatusBadGateway)
		}
		return &folderResult{fo, r.seconds(), r.utc()}, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		msg := messages{
			http.StatusBadRequest:      "Provide valid workspace and folder UUIDs",
			http.StatusUnauthorized:    "Authentication failed. Use the configured identity with permission to read the Fabric workspace",
			http.StatusForbidden:       "Access denied. The caller needs workspace Viewer or higher access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All",
			http.StatusNotFound:        "The Fabric workspace or folder was not found. Check its ID and the caller's access",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric folder request was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric folder request timed out",
			http.StatusBadGateway:      "Fabric returned invalid folder metadata. Expected matching workspace and folder IDs and a display name as JSON up to 1 MiB and 64 nesting levels",
			0:                          "Unable to retrieve Fabric folder metadata. Retry the request or check service availability",
		}.get(status)
		if f.cls == "folderGet" {
			if safeErrorCode(f.code) {
				msg += ". Fabric error code: " + f.code
			}
			if f.metaUnavailable {
				msg += ". Additional error metadata was unavailable"
			}
			if f.retry != nil {
				msg += f.retry.retryGuidance()
			}
		}
		return fail(status, msg), nil, nil
	}
	return response.Success(res), nil, nil
}

// --- create-folder / update-folder / move-folder ---

func (a *Area) createFolder(ctx context.Context, _ *mcp.CallToolRequest, in createFolderInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	parent, ok := parseOptID(in.ParentFolderID)
	if !ok {
		errs = append(errs, "--parent-folder-id must be a nonempty UUID when supplied.")
	}
	if !validFolderName(in.DisplayName) {
		errs = append(errs, invalidFolderName)
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	body := map[string]any{"displayName": in.DisplayName}
	if parent != nil {
		body["parentFolderId"] = parent
	}
	m, err := mutate(ctx, a, http.MethodPost, "/workspaces/"+ws.String()+"/folders", body,
		"Fabric folder creation did not return usable folder metadata.",
		func(b []byte) (*folder, error) {
			const msg = "Fabric returned invalid created folder metadata."
			f, err := readFolder(b, ws, nil, msg)
			if err != nil {
				return nil, err
			}
			if isBlank(*f.DisplayName) || f.ParentFolderID != nil && (parent == nil || *f.ParentFolderID != *parent) {
				return nil, jsonError(msg)
			}
			return f, nil
		},
		"Fabric returned non-JSON folder metadata.", "The Fabric folder metadata exceeds the 1 MiB response limit.", http.StatusCreated)
	if err != nil {
		return mutationFail(asFailure(err), mutationText{
			confirmed:   "Fabric confirmed folder creation (HTTP 201), but the returned folder metadata could not be read or validated. Do not resubmit this creation; inspect the workspace separately",
			notSent:     ". The folder creation request was not sent",
			maybe:       ". A folder may already have been created; verify the workspace separately before any new creation. Do not retry blindly",
			inputSuffix: " The folder creation request was not sent.",
			maybeOn4xx:  true,
			table: messages{
				http.StatusBadRequest:      "Check the workspace/parent UUIDs and Fabric folder naming rules",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. The caller needs Contributor or higher workspace access; delegated calls require Workspace.ReadWrite.All",
				http.StatusNotFound:        "The Fabric workspace or parent folder was not found, or the caller does not have access",
				http.StatusConflict:        "Fabric rejected folder creation because of a conflict. An existing folder is not returned as a successful creation",
				http.StatusTooManyRequests: "Fabric throttled this folder creation",
				http.StatusRequestTimeout:  "The folder creation request was canceled or timed out",
				http.StatusGatewayTimeout:  "The folder creation request was canceled or timed out",
				0:                          "Fabric folder creation was not confirmed",
			},
		}), nil, nil
	}
	const guidance = "Fabric confirmed folder creation (HTTP 201). No lookup, follow-up read, polling, or automatic retry was performed. Do not resubmit this creation."
	return response.SuccessMessage(http.StatusCreated, guidance, struct {
		folderResult
		CreationStatus string `json:"creationStatus"`
		Guidance       string `json:"guidance"`
	}{folderResult{m.value, m.retry.seconds(), m.retry.utc()}, "Created", guidance}), nil, nil
}

func (a *Area) updateFolder(ctx context.Context, _ *mcp.CallToolRequest, in updateFolderInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	id, ok := parseID(in.FolderID)
	if !ok {
		errs = append(errs, "--folder-id must be a nonempty UUID.")
	}
	if !validFolderName(in.DisplayName) {
		errs = append(errs, invalidFolderName)
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	m, err := mutate(ctx, a, http.MethodPatch, "/workspaces/"+ws.String()+"/folders/"+id.String(),
		map[string]any{"displayName": in.DisplayName},
		"Fabric folder update did not return usable folder metadata.",
		func(b []byte) (*folder, error) {
			f, err := readFolder(b, ws, &id, "Fabric returned invalid folder metadata.")
			if err == nil && isBlank(*f.DisplayName) {
				err = jsonError("Fabric returned invalid updated folder metadata.")
			}
			return f, err
		},
		"Fabric returned non-JSON folder metadata.", "The Fabric folder metadata exceeds the 1 MiB response limit.")
	if err != nil {
		return mutationFail(asFailure(err), mutationText{
			confirmed:   "Fabric confirmed folder update (HTTP 200), but the returned folder metadata could not be read or validated. Do not resubmit this update; inspect the folder separately",
			notSent:     ". The folder update request was not sent",
			maybe:       ". The update may have been applied; inspect the folder separately before another update. Do not retry blindly",
			inputSuffix: " The folder update request was not sent.",
			table: messages{
				http.StatusBadRequest:      "Fabric rejected the folder update. Check the workspace/folder UUIDs and Fabric folder naming rules",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. The caller needs Contributor or higher workspace access; delegated calls require Workspace.ReadWrite.All",
				http.StatusNotFound:        "The Fabric workspace or folder was not found, or the caller does not have access",
				http.StatusConflict:        "Fabric rejected the folder update because of a conflict. A duplicate name remains a failure; no replacement name was generated",
				http.StatusTooManyRequests: "Fabric throttled this folder update",
				http.StatusRequestTimeout:  "The folder update request was canceled or timed out",
				http.StatusGatewayTimeout:  "The folder update request was canceled or timed out",
				0:                          "Fabric folder update was not confirmed",
			},
		}), nil, nil
	}
	const guidance = "Fabric confirmed folder update (HTTP 200). This is the returned metadata snapshot. No lookup, follow-up read, polling, or automatic retry was performed."
	return response.SuccessMessage(http.StatusOK, guidance, struct {
		folderResult
		UpdateStatus string `json:"updateStatus"`
		Guidance     string `json:"guidance"`
	}{folderResult{m.value, m.retry.seconds(), m.retry.utc()}, "Updated", guidance}), nil, nil
}

func (a *Area) moveFolder(ctx context.Context, _ *mcp.CallToolRequest, in moveFolderInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	id, idOK := parseID(in.FolderID)
	if !idOK {
		errs = append(errs, "--folder-id must be a nonempty UUID.")
	}
	target, ok := parseOptID(in.TargetFolderID)
	switch {
	case !ok:
		errs = append(errs, "--target-folder-id must be a nonempty UUID when supplied; omit it to use the workspace root.")
	case target != nil && idOK && *target == id:
		errs = append(errs, "--target-folder-id cannot identify the folder being moved.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	body := map[string]any{}
	if target != nil {
		body["targetFolderId"] = target
	}
	m, err := mutate(ctx, a, http.MethodPost, "/workspaces/"+ws.String()+"/folders/"+id.String()+"/move", body,
		"Fabric folder movement did not return usable folder metadata.",
		func(b []byte) (*folder, error) {
			f, err := readFolder(b, ws, &id, "Fabric returned invalid folder metadata.")
			if err == nil && f.ParentFolderID != nil && (target == nil || *f.ParentFolderID != *target) {
				err = jsonError("Fabric returned folder metadata with a conflicting destination.")
			}
			return f, err
		},
		"Fabric returned non-JSON folder metadata.", "The Fabric folder metadata exceeds the 1 MiB response limit.")
	if err != nil {
		return mutationFail(asFailure(err), mutationText{
			confirmed:   "Fabric confirmed folder movement (HTTP 200), but the returned folder metadata could not be read or validated. Do not resubmit this move; inspect the folder separately",
			notSent:     ". The folder move request was not sent",
			maybe:       ". The folder may have moved; inspect it separately before another move. Do not retry blindly",
			inputSuffix: " The folder move request was not sent.",
			table: messages{
				http.StatusBadRequest:      "Fabric rejected the folder move. Check the workspace/folder UUIDs, destination, and hierarchy constraints",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. The caller needs Contributor or higher workspace access; delegated calls require Workspace.ReadWrite.All",
				http.StatusNotFound:        "The Fabric workspace, folder, or destination was not found, or the caller does not have access",
				http.StatusConflict:        "Fabric rejected the folder move because of a conflict. No alternate destination or name was selected",
				http.StatusTooManyRequests: "Fabric throttled this folder move",
				http.StatusRequestTimeout:  "The folder move request was canceled or timed out",
				http.StatusGatewayTimeout:  "The folder move request was canceled or timed out",
				0:                          "Fabric folder movement was not confirmed",
			},
		}), nil, nil
	}
	const guidance = "Fabric confirmed folder movement (HTTP 200). This is the returned metadata snapshot. No lookup, descendant enumeration, follow-up read, polling, or automatic retry was performed."
	return response.SuccessMessage(http.StatusOK, guidance, struct {
		folderResult
		MoveStatus string `json:"moveStatus"`
		Guidance   string `json:"guidance"`
	}{folderResult{m.value, m.retry.seconds(), m.retry.utc()}, "Moved", guidance}), nil, nil
}

// --- delete-folder ---

func (a *Area) deleteFolder(ctx context.Context, _ *mcp.CallToolRequest, in deleteFolderInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID. No DELETE request was sent.")
	}
	id, ok := parseID(in.FolderID)
	if !ok {
		errs = append(errs, "--folder-id must be a nonempty UUID. No DELETE request was sent.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	resp, sent, err := a.send(ctx, http.MethodDelete, "/workspaces/"+ws.String()+"/folders/"+id.String(), nil)
	var f *failure
	if err != nil {
		e := asFailure(err)
		f = &failure{kind: kHTTP, cls: "folderDelete", notSent: !sent}
		switch e.kind {
		case kCanceled:
			f.status = http.StatusRequestTimeout
		case kTimeout:
			f.status = http.StatusGatewayTimeout
		case kAuth:
			f.status = http.StatusUnauthorized
		default:
			f.status = baseStatus(e)
		}
	} else if resp.StatusCode == http.StatusOK {
		discard(resp)
		const guidance = "Fabric confirmed deletion of the requested empty folder (HTTP 200). No folder snapshot was returned. " +
			"No items or nested folders were deleted or moved. No lookup, follow-up read, polling, or automatic retry was performed. " +
			"Do not resubmit this confirmed deletion. The API does not document folder recovery or a retention period."
		return response.SuccessMessage(http.StatusOK, guidance, map[string]any{
			"workspaceId": ws, "folderId": id, "deletionStatus": "Deleted", "guidance": guidance,
		}), nil, nil
	} else {
		f = &failure{kind: kHTTP, cls: "folderDelete", status: nonSuccess(resp.StatusCode),
			retry: parseRetryAfter(resp.Header), respStatus: resp.StatusCode}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			discard(resp)
		} else {
			f.code, f.metaUnavailable = readErrorCode(resp)
		}
	}
	status := f.status
	msg := messages{
		http.StatusBadRequest:      "Fabric rejected the folder deletion. Check the explicit workspace and folder UUIDs; only empty folders can be deleted",
		http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
		http.StatusForbidden:       "Access denied. The caller needs Contributor or higher workspace access; delegated calls require Workspace.ReadWrite.All",
		http.StatusNotFound:        "The Fabric workspace or folder was not found or is inaccessible. This is not confirmed deletion",
		http.StatusConflict:        "Fabric rejected the folder deletion because of a conflict; only empty folders can be deleted",
		http.StatusTooManyRequests: "Fabric throttled this folder deletion",
		http.StatusRequestTimeout:  "The folder deletion request was canceled or timed out",
		http.StatusGatewayTimeout:  "The folder deletion request was canceled or timed out",
		0:                          "Fabric folder deletion was not confirmed",
	}.get(status)
	certain4xx := f.respStatus >= 400 && f.respStatus < 500 && f.respStatus != http.StatusRequestTimeout
	switch {
	case f.notSent:
		msg += ". No DELETE request was sent"
	case !certain4xx:
		msg += ". The folder may have been deleted; inspect it with core_get-folder or core_list-folders separately before another deletion. Do not retry blindly"
	}
	msg += ". No automatic retry or change to folder contents was attempted"
	if safeErrorCode(f.code) {
		msg += ". Fabric error code: " + f.code
	}
	if f.code == "FolderNotEmpty" {
		msg += ". Fabric requires an empty folder; items, including soft-deleted items, or nested folders prevent deletion"
	}
	if f.metaUnavailable {
		msg += ". Optional Fabric error metadata was unavailable"
	}
	if f.retry != nil {
		msg += f.retry.requestGuidance()
	}
	return fail(status, msg), nil, nil
}
