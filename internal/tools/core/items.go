package core

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// This file ports the item metadata commands: list, get, update, move and
// delete.

// itemSummary ports FabricItemSummary.
type itemSummary struct {
	ID               uuid.UUID  `json:"id"`
	DisplayName      string     `json:"displayName"`
	Type             string     `json:"type"`
	WorkspaceID      uuid.UUID  `json:"workspaceId"`
	Description      *string    `json:"description,omitempty"`
	FolderID         *uuid.UUID `json:"folderId,omitempty"`
	LogicalID        *uuid.UUID `json:"logicalId,omitempty"`
	Tags             *[]*tag    `json:"tags,omitempty"`
	SensitivityLabel *struct {
		ID uuid.UUID `json:"id"`
	} `json:"sensitivityLabel,omitempty"`
}

// itemMetadata ports FabricItemMetadata.
type itemMetadata struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"displayName"`
	Description *string   `json:"description,omitempty"`
	Type        string    `json:"type"`
	WorkspaceID uuid.UUID `json:"workspaceId"`
}

// --- list-items ---

func (a *Area) listItems(ctx context.Context, _ *mcp.CallToolRequest, in listItemsInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	root, ok := parseOptID(in.RootFolderID)
	if !ok {
		errs = append(errs, "--root-folder-id must be a nonempty UUID.")
	}
	if in.Type != nil && isBlank(*in.Type) {
		errs = append(errs, "--type must not be empty or whitespace.")
	}
	if in.ContinuationToken != nil && isBlank(*in.ContinuationToken) {
		errs = append(errs, "--continuation-token must not be empty or whitespace.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	typ, rootID := "", ""
	if in.Type != nil {
		typ = escapeData(*in.Type)
	}
	if root != nil {
		rootID = root.String()
	}
	recursive := in.Recursive == nil || *in.Recursive
	type page struct {
		Value             *[]*itemSummary `json:"value"`
		ContinuationToken *string         `json:"continuationToken,omitempty"`
		ContinuationURI   *string         `json:"continuationUri,omitempty"`
	}
	p, err := func() (*page, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/workspaces/"+ws.String()+"/items"+query(
			"type", typ, "recursive", boolString(recursive), "rootFolderId", rootID,
			"continuationToken", optToken(in.ContinuationToken)), nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			if resp.StatusCode == http.StatusTooManyRequests {
				if r := parseRetryAfter(resp.Header); r != nil {
					return nil, throttled("listThrottled", "request", r)
				}
			}
			return nil, httpError(nonSuccess(resp.StatusCode), "Unable to list Fabric item metadata.")
		}
		const invalid = "Fabric returned an invalid item metadata page."
		p, err := readJSON[*page](resp, invalid)
		if err != nil {
			return nil, err
		}
		if p == nil || p.Value == nil {
			return nil, jsonError(invalid)
		}
		for _, it := range *p.Value {
			if it == nil || it.ID == uuid.Nil || it.WorkspaceID != ws || isBlank(it.DisplayName) || isBlank(it.Type) {
				return nil, jsonError(invalid)
			}
		}
		return p, nil
	}()
	if err != nil {
		return readFail(asFailure(err), "listThrottled", messages{
			http.StatusBadRequest:      "Check the workspace and root folder UUIDs, item type, and continuation token",
			http.StatusUnauthorized:    "Authentication failed. Use the configured identity with permission to read the Fabric workspace",
			http.StatusForbidden:       "Access denied. The caller needs workspace Viewer access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All",
			http.StatusNotFound:        "The Fabric workspace or root folder was not found. Check its ID and the caller's access",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric item listing was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric item listing timed out",
			http.StatusBadGateway:      "Fabric returned an invalid item metadata page",
			0:                          "Unable to list Fabric item metadata. Retry the request or check service availability",
		}), nil, nil
	}
	return response.Success(struct {
		Items             []*itemSummary `json:"items"`
		ContinuationToken *string        `json:"continuationToken,omitempty"`
		ContinuationURI   *string        `json:"continuationUri,omitempty"`
	}{*p.Value, p.ContinuationToken, p.ContinuationURI}), nil, nil
}

// --- get-item ---

func (a *Area) getItem(ctx context.Context, _ *mcp.CallToolRequest, in getItemInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	id, ok := parseID(in.ItemID)
	if !ok {
		errs = append(errs, "--item-id must be a nonempty UUID.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	item, err := func() (*itemMetadata, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/workspaces/"+ws.String()+"/items/"+id.String(), nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, statusError(resp, "Unable to retrieve Fabric item metadata.", "throttled", "request")
		}
		item, err := readJSON[*itemMetadata](resp, "Fabric returned invalid item metadata.")
		if err != nil {
			return nil, err
		}
		if item == nil || item.ID != id || item.WorkspaceID != ws || isBlank(item.DisplayName) || isBlank(item.Type) {
			return nil, jsonError("Fabric returned invalid item metadata.")
		}
		return item, nil
	}()
	if err != nil {
		return readFail(asFailure(err), "throttled", messages{
			http.StatusBadRequest:      "Provide valid workspace and item UUIDs",
			http.StatusUnauthorized:    "Authentication failed. Sign in with an identity that can read the Fabric item",
			http.StatusForbidden:       "Access denied. The caller needs read permission for this Fabric item",
			http.StatusNotFound:        "The Fabric item was not found. Check its workspace and item IDs",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric item request was canceled or timed out",
			http.StatusBadGateway:      "Fabric returned an invalid item metadata response",
			0:                          "Unable to retrieve Fabric item metadata. Retry the request or check service availability",
		}), nil, nil
	}
	return response.Success(map[string]any{"item": item}), nil, nil
}

// --- update-item ---

// itemUpdate ports ItemUpdateMetadata, which keeps the IDs as returned.
type itemUpdate struct {
	ID          *string `json:"id"`
	DisplayName *string `json:"displayName"`
	Type        *string `json:"type"`
	WorkspaceID *string `json:"workspaceId"`
	Description *string `json:"description,omitempty"`
}

func (a *Area) updateItem(ctx context.Context, _ *mcp.CallToolRequest, in updateItemInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	id, ok := parseID(in.ItemID)
	if !ok {
		errs = append(errs, "--item-id must be a nonempty UUID.")
	}
	if in.DisplayName == nil && in.Description == nil {
		errs = append(errs, "Provide at least one of --display-name or --description.")
	}
	if in.DisplayName != nil && isBlank(*in.DisplayName) {
		errs = append(errs, "--display-name must not be empty or whitespace.")
	}
	if in.Description != nil && utf16Len(*in.Description) > 256 {
		errs = append(errs, "--description must not exceed 256 characters.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	const invalidResponse = "Fabric returned an invalid Update Item response."
	invalidData := &failure{kind: kInvalidData, msg: invalidResponse}
	body := struct {
		DisplayName *string `json:"displayName,omitempty"`
		Description *string `json:"description,omitempty"`
	}{in.DisplayName, in.Description}
	item, err := func() (*itemUpdate, error) {
		resp, _, err := a.send(ctx, http.MethodPatch, "/workspaces/"+ws.String()+"/items/"+id.String(), body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				return nil, invalidData
			}
			f := httpError(resp.StatusCode, "Fabric Update Item returned HTTP "+strconv.Itoa(resp.StatusCode)+".")
			f.cls = "updateRequest"
			f.retry = parseRetryAfter(resp.Header)
			return nil, f
		}
		b, err := readBody(resp, 64*maxJSONBytes, func() error { return invalidData })
		if err != nil {
			return nil, err
		}
		var item *itemUpdate
		if json.Unmarshal(b, &item) != nil || item == nil || item.ID == nil || item.WorkspaceID == nil ||
			item.DisplayName == nil || item.Type == nil {
			return nil, invalidData
		}
		if rid, ok := parseID(*item.ID); !ok || rid != id {
			return nil, invalidData
		}
		if rws, ok := parseID(*item.WorkspaceID); !ok || rws != ws {
			return nil, invalidData
		}
		if isBlank(*item.DisplayName) || isBlank(*item.Type) {
			return nil, invalidData
		}
		return item, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := baseStatus(f)
		switch f.kind {
		case kInvalidData:
			status = http.StatusBadGateway
		case kInvalidOp:
			status = http.StatusInternalServerError
		case kCanceled:
			status = http.StatusGatewayTimeout
		}
		var msg string
		switch {
		case f.kind == kAuth:
			msg = "Authentication failed. Check the configured Fabric identity and its access to the item."
		case f.kind == kArg:
			msg = "Invalid update request. Provide nonempty workspace and item UUIDs and at least one valid display name or description."
		case f.kind == kInvalidData:
			msg = "Fabric returned an invalid update response. The update may have completed; verify the item's state before retrying."
		case f.kind == kHTTP && f.status != 0:
			msg = messages{
				http.StatusBadRequest:      "Fabric rejected the update. Check the display-name rules for the item's type and the description's 256-character limit.",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity and its access to the item.",
				http.StatusForbidden:       "Access denied. The caller needs read and write permission on the item. Delegated callers need Item.ReadWrite.All or the item-specific scope. Service-principal and managed-identity support depends on item type.",
				http.StatusNotFound:        "The Fabric workspace or item was not found, or the caller does not have access.",
				http.StatusConflict:        "The update conflicts with an existing item name or state. Verify the item before retrying.",
				http.StatusTooManyRequests: "Fabric throttled the update. No automatic retry was attempted.",
				0:                          "Fabric returned HTTP " + strconv.Itoa(f.status) + ". Verify the item's state before retrying.",
			}.get(f.status)
		case f.kind == kHTTP:
			msg = "The Fabric update could not be completed because of a network error. Verify the item's state before retrying."
		case f.kind == kCanceled || f.kind == kTimeout:
			msg = "The Fabric update timed out. Verify the item's state before retrying."
		default:
			msg = "The Fabric update could not be completed. Verify the item's state before retrying."
		}
		if f.cls == "updateRequest" && f.retry != nil && f.retry.delta != nil && f.retry.delta.Seconds() <= 2147483647 {
			msg += " Wait at least " + strconv.FormatInt(int64(f.retry.delta.Seconds()), 10) + " seconds before another request."
		}
		return fail(status, strings.TrimRight(msg, ".")), nil, nil
	}
	return response.Success(map[string]any{"item": item}), nil, nil
}

// --- delete-item ---

func (a *Area) deleteItem(ctx context.Context, _ *mcp.CallToolRequest, in deleteItemInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	id, ok := parseID(in.ItemID)
	if !ok {
		errs = append(errs, "--item-id must be a nonempty UUID.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	resp, _, err := a.send(ctx, http.MethodDelete, "/workspaces/"+ws.String()+"/items/"+id.String()+query("hardDelete", optBool(in.HardDelete)), nil)
	if err == nil {
		discard(resp)
		switch {
		case resp.StatusCode == http.StatusOK:
			return response.Success(map[string]any{
				"workspaceId": ws, "itemId": id, "hardDeleteRequested": in.HardDelete != nil && *in.HardDelete,
			}), nil, nil
		case ctx.Err() != nil:
			err = ctx.Err()
		case resp.StatusCode == http.StatusTooManyRequests:
			r := parseRetryAfter(resp.Header)
			msg := "Fabric throttled the deletion request. Wait before retrying; the request was not automatically retried"
			if r != nil {
				msg = throttled("", "deletion request", r).msg
			}
			err = &failure{kind: kHTTP, status: http.StatusTooManyRequests, msg: msg, cls: "deleteThrottled"}
		default:
			err = httpError(nonSuccess(resp.StatusCode), "Fabric item deletion was not confirmed.")
		}
	}
	f := asFailure(err)
	status := baseStatus(f)
	if f.kind == kCanceled {
		status = http.StatusRequestTimeout
	}
	if status == http.StatusBadRequest && f.kind == kArg {
		return fail(status, "Provide nonempty workspace and item UUIDs and an explicit true or false value when supplying hard-delete"), nil, nil
	}
	if status == http.StatusTooManyRequests && f.cls == "deleteThrottled" {
		return fail(status, f.msg), nil, nil
	}
	const rejected = "Fabric rejected item deletion. Check the item state, item-type soft-deletion support, and tenant settings. No fallback to permanent deletion was attempted"
	const canceled = "The Fabric item deletion request was canceled or timed out. The item's deletion state was not confirmed"
	return fail(status, messages{
		http.StatusUnauthorized:        "Authentication failed. Use an identity authorized to delete the Fabric item",
		http.StatusForbidden:           "Access denied. Soft deletion needs item write permission; permanent deletion needs workspace Admin. Delegated callers also need Item.ReadWrite.All or the item-specific write scope",
		http.StatusNotFound:            "The Fabric item was not found. Check its workspace ID, item ID, and the caller's access. Deletion was not confirmed",
		http.StatusBadRequest:          rejected,
		http.StatusConflict:            rejected,
		http.StatusUnprocessableEntity: rejected,
		http.StatusTooManyRequests:     "Fabric throttled the deletion request. Wait before retrying; the request was not automatically retried",
		http.StatusRequestTimeout:      canceled,
		http.StatusGatewayTimeout:      canceled,
		http.StatusBadGateway:          "Fabric returned an unexpected item deletion response. Deletion was not confirmed",
		0:                              "Unable to confirm Fabric item deletion. Check permissions, item state, and service availability",
	}.get(status)), nil, nil
}

// --- move-item ---

// movedItem ports FabricMovedItem.
type movedItem struct {
	Type             *string         `json:"type"`
	ID               *uuid.UUID      `json:"id,omitempty"`
	DisplayName      *string         `json:"displayName,omitempty"`
	WorkspaceID      *uuid.UUID      `json:"workspaceId,omitempty"`
	Description      *string         `json:"description,omitempty"`
	FolderID         *uuid.UUID      `json:"folderId,omitempty"`
	LogicalID        *uuid.UUID      `json:"logicalId,omitempty"`
	Tags             *[]*nullableTag `json:"tags,omitempty"`
	SensitivityLabel *struct {
		ID uuid.UUID `json:"id"`
	} `json:"sensitivityLabel,omitempty"`
}

// nullableTag is a tag whose display name may be absent.
type nullableTag struct {
	ID          uuid.UUID `json:"id"`
	DisplayName *string   `json:"displayName"`
}

func (a *Area) moveItem(ctx context.Context, _ *mcp.CallToolRequest, in moveItemInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	id, ok := parseID(in.ItemID)
	if !ok {
		errs = append(errs, "--item-id must be a nonempty UUID.")
	}
	target, ok := parseOptID(in.TargetFolderID)
	if !ok {
		errs = append(errs, "--target-folder-id must be a nonempty UUID when supplied; omit it to use the workspace root.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	body := map[string]any{}
	if target != nil {
		body["targetFolderId"] = target
	}
	m, err := mutate(ctx, a, http.MethodPost, "/workspaces/"+ws.String()+"/items/"+id.String()+"/move", body,
		"Fabric item movement did not return usable moved-item metadata.",
		func(b []byte) ([]*movedItem, error) { return readMovedItems(b, ws, target) },
		"Fabric returned non-JSON moved-item metadata.", "The Fabric moved-item metadata exceeds the 1 MiB response limit.")
	if err != nil {
		return mutationFail(asFailure(err), mutationText{
			confirmed:   "Fabric confirmed item movement (HTTP 200), but the returned item collection could not be read or validated. Do not resubmit this move; inspect the item separately",
			notSent:     ". The item move request was not sent",
			maybe:       ". The item may have moved; inspect it separately before another move. Do not retry blindly",
			inputSuffix: " The item move request was not sent.",
			table: messages{
				http.StatusBadRequest:      "Fabric rejected the item move. Check the workspace/item UUIDs and destination; a child item cannot be moved independently",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. The caller needs Contributor or higher workspace access; delegated calls require Workspace.ReadWrite.All",
				http.StatusNotFound:        "The Fabric workspace, item, or destination folder was not found, or the caller does not have access",
				http.StatusConflict:        "Fabric rejected the item move because of a conflict. No alternate destination or parent item was selected",
				http.StatusTooManyRequests: "Fabric throttled this item move",
				http.StatusRequestTimeout:  "The item move request was canceled or timed out",
				http.StatusGatewayTimeout:  "The item move request was canceled or timed out",
				0:                          "Fabric item movement was not confirmed",
			},
		}), nil, nil
	}
	const guidance = "Fabric confirmed item movement (HTTP 200). Items is the complete returned metadata collection, including any returned child items, in service order. An empty collection or absent fields do not establish which items were affected. No lookup, follow-up read, polling, or automatic retry was performed."
	return response.SuccessMessage(http.StatusOK, guidance, struct {
		Items             []*movedItem `json:"items"`
		RetryAfterSeconds *int64       `json:"retryAfterSeconds,omitempty"`
		RetryAfterUtc     *netTime     `json:"retryAfterUtc,omitempty"`
		MoveStatus        string       `json:"moveStatus"`
		Guidance          string       `json:"guidance"`
	}{m.value, m.retry.seconds(), m.retry.utc(), "Moved", guidance}), nil, nil
}

func readMovedItems(b []byte, ws uuid.UUID, target *uuid.UUID) ([]*movedItem, error) {
	const invalid = "Fabric returned invalid or conflicting moved-item metadata."
	if err := validateMetadataJSON(b); err != nil {
		return nil, jsonError(invalid)
	}
	var root struct {
		Value *[]*movedItem `json:"value"`
	}
	if !strings.HasPrefix(strings.TrimSpace(string(b)), "{") {
		return nil, jsonError("Fabric returned an invalid moved-item collection.")
	}
	if err := json.Unmarshal(b, &root); err != nil {
		return nil, jsonError(invalid)
	}
	if root.Value == nil {
		return nil, jsonError("Fabric returned an invalid moved-item collection.")
	}
	for _, it := range *root.Value {
		if it == nil || it.Type == nil || isBlank(*it.Type) ||
			it.ID != nil && *it.ID == uuid.Nil || it.LogicalID != nil && *it.LogicalID == uuid.Nil ||
			it.WorkspaceID != nil && *it.WorkspaceID != ws ||
			it.FolderID != nil && (*it.FolderID == uuid.Nil || target == nil || *it.FolderID != *target) ||
			it.SensitivityLabel != nil && it.SensitivityLabel.ID == uuid.Nil {
			return nil, jsonError(invalid)
		}
		if it.Tags != nil {
			for _, t := range *it.Tags {
				if t == nil || t.ID == uuid.Nil || t.DisplayName == nil {
					return nil, jsonError(invalid)
				}
			}
		}
	}
	if *root.Value == nil {
		*root.Value = []*movedItem{}
	}
	return *root.Value, nil
}
