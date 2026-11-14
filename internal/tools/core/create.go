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

// This file ports the catalog search and item creation commands.

// catalogSearchResponse and the types below port upstream's CoreModels,
// which keep only these fields of the API response.
type catalogSearchResponse struct {
	Value             []catalogEntry `json:"value"`
	ContinuationToken *string        `json:"continuationToken,omitempty"`
}

type catalogEntry struct {
	ID               string  `json:"id"`
	Type             string  `json:"type"`
	CatalogEntryType string  `json:"catalogEntryType"`
	DisplayName      string  `json:"displayName"`
	Description      *string `json:"description,omitempty"`
	Hierarchy        *struct {
		Workspace *struct {
			ID          string `json:"id"`
			DisplayName string `json:"displayName"`
		} `json:"workspace,omitempty"`
	} `json:"hierarchy,omitempty"`
}

func (a *Area) searchCatalog(ctx context.Context, _ *mcp.CallToolRequest, in searchCatalogInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	if in.PageSize != nil && (*in.PageSize < 1 || *in.PageSize > 1000) {
		errs = append(errs, "Page size must be between 1 and 1000.")
	}
	if in.ContinuationToken != nil && (in.Search != nil || in.Filter != nil) {
		errs = append(errs, "--continuation-token must not be combined with --search or --filter. Use the returned token without search or filter.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	body := struct {
		Search            *string `json:"search,omitempty"`
		Filter            *string `json:"filter,omitempty"`
		PageSize          *int    `json:"pageSize,omitempty"`
		ContinuationToken *string `json:"continuationToken,omitempty"`
	}{in.Search, in.Filter, in.PageSize, in.ContinuationToken}
	out, err := func() (*catalogSearchResponse, error) {
		resp, _, err := a.send(ctx, http.MethodPost, "/catalog/search", body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			b, _ := readBody(resp, maxJSONBytes, func() error { return nil })
			return nil, httpError(resp.StatusCode, "Fabric API request failed with status "+strconv.Itoa(resp.StatusCode)+
				" ("+strings.ReplaceAll(http.StatusText(resp.StatusCode), " ", "")+"): "+string(b))
		}
		b, err := readBody(resp, 64*maxJSONBytes, func() error { return jsonError("too large") })
		if err != nil {
			return nil, err
		}
		var out *catalogSearchResponse
		if err := json.Unmarshal(b, &out); err != nil {
			return nil, jsonError(err.Error())
		}
		if out == nil {
			out = &catalogSearchResponse{}
		}
		return out, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := baseStatus(f)
		var msg string
		switch {
		case f.kind == kAuth && credentialUnavailable(err):
			msg = "Fabric credentials are unavailable. Authenticate the configured Fabric identity before searching the catalog"
		case f.kind == kHTTP && f.status == 0:
			msg = "Service unavailable or network connectivity issues prevented the Fabric catalog search. Check connectivity and retry later"
		case f.kind == kCanceled || f.kind == kTimeout:
			msg = "The Fabric catalog search timed out or was canceled. Retry the search later"
		default:
			msg = messages{
				http.StatusBadRequest:      "Fabric rejected the catalog search. Check the search criteria, filter, page size, and continuation token",
				http.StatusUnauthorized:    "Authentication failed while searching the Fabric catalog. Check the configured Fabric identity and its catalog access",
				http.StatusForbidden:       "Fabric denied the catalog search. Check permissions and whether catalog search is supported and enabled for the tenant and capacity",
				http.StatusNotFound:        "The Fabric catalog search resource was not found, or the caller does not have access",
				http.StatusConflict:        "The catalog search conflicts with the current Fabric state. Check the catalog state before retrying",
				http.StatusTooManyRequests: "Fabric throttled the catalog search or reached a capacity limit. Retry the request later",
				0:                          "The Fabric catalog search could not be completed. Check service availability before retrying",
			}.get(status)
			if _, known := map[int]bool{400: true, 401: true, 403: true, 404: true, 409: true, 429: true}[status]; !known && f.kind == kHTTP {
				msg = "Fabric catalog search failed with HTTP " + strconv.Itoa(f.status) + ". Check service availability before retrying"
			}
		}
		return fail(status, msg), nil, nil
	}
	if out.Value == nil {
		out.Value = []catalogEntry{}
	}
	return response.Success(map[string]any{"results": out}), nil, nil
}

// fabricItem ports CoreModels' FabricItem.
type fabricItem struct {
	ID               string   `json:"id"`
	DisplayName      string   `json:"displayName"`
	Description      *string  `json:"description,omitempty"`
	Type             string   `json:"type"`
	WorkspaceID      string   `json:"workspaceId"`
	Definition       any      `json:"definition,omitempty"`
	CreatedDate      *netDate `json:"createdDate,omitempty"`
	LastModifiedDate *netDate `json:"lastModifiedDate,omitempty"`
}

// netDate ports a .NET DateTime: it accepts a timestamp with or without an
// offset and writes it back as given.
type netDate string

func (d *netDate) UnmarshalJSON(b []byte) error {
	var t netTime
	if err := t.UnmarshalJSON(b); err != nil {
		return err
	}
	var s string
	_ = json.Unmarshal(b, &s)
	*d = netDate(s)
	return nil
}

const invalidItemMetadata = "Fabric reported completion but returned invalid created item metadata. Check whether creation occurred before retrying."

// readCreatedItem ports FabricCoreService.ReadCreatedItem.
func readCreatedItem(ws uuid.UUID) func(json.RawMessage) (*fabricItem, error) {
	return func(v json.RawMessage) (*fabricItem, error) {
		bad := opFailure("InvalidItemMetadata", invalidItemMetadata, 0, nil)
		var item *fabricItem
		if json.Unmarshal(v, &item) != nil || item == nil || isBlank(item.DisplayName) || isBlank(item.Type) {
			return nil, bad
		}
		if _, ok := parseID(item.ID); !ok {
			return nil, bad
		}
		if rws, ok := parseID(item.WorkspaceID); !ok || rws != ws {
			return nil, bad
		}
		return item, nil
	}
}

// labelSettings ports FabricSensitivityLabelSettings.
type labelSettings struct {
	LabelID                       uuid.UUID `json:"labelId"`
	SensitivityLabelApplyStrategy *string   `json:"sensitivityLabelApplyStrategy,omitempty"`
}

// parseLabelSettings ports ItemCreateRequestInput.ParseLabelSettings.
func parseLabelSettings(s *string) (*labelSettings, error) {
	raw, err := parseJSONObject(s, "--sensitivity-label-settings", 4096)
	if err != nil || raw == nil {
		return nil, err
	}
	shape := itemInputError("--sensitivity-label-settings requires labelId (a nonempty UUID) and accepts optional sensitivityLabelApplyStrategy (a string).")
	obj, err := strictObject(raw, []string{"labelId", "sensitivityLabelApplyStrategy"}, []string{"labelId"})
	if err != nil {
		return nil, shape
	}
	var ls labelSettings
	if json.Unmarshal(obj["labelId"], &ls.LabelID) != nil {
		return nil, shape
	}
	strategy, hasStrategy := obj["sensitivityLabelApplyStrategy"]
	if hasStrategy {
		if ls.SensitivityLabelApplyStrategy, err = optString(strategy); err != nil {
			return nil, shape
		}
	}
	if ls.LabelID == uuid.Nil {
		return nil, itemInputError("--sensitivity-label-settings requires a nonempty labelId UUID.")
	}
	if hasStrategy && (ls.SensitivityLabelApplyStrategy == nil ||
		*ls.SensitivityLabelApplyStrategy != "ApplyOrFail" && *ls.SensitivityLabelApplyStrategy != "Ignore") {
		return nil, itemInputError("A supplied sensitivityLabelApplyStrategy must be ApplyOrFail or Ignore. Omit it to retain Fabric's ApplyOrFail default.")
	}
	return &ls, nil
}

type createResult struct {
	Item      *fabricItem `json:"item,omitempty"`
	Operation *receipt    `json:"operation,omitempty"`
}

func (a *Area) createItem(ctx context.Context, _ *mcp.CallToolRequest, in createItemInput) (*mcp.CallToolResult, any, error) {
	wsArg := in.WorkspaceID
	if wsArg == nil || isBlank(*wsArg) {
		wsArg = in.Workspace
	}
	var ws uuid.UUID
	ok := wsArg != nil
	if ok {
		ws, ok = parseID(*wsArg)
	}
	w := waitFrom(in.Sync, in.MaxWaitSeconds, in.EarlyPoll)
	var errs []string
	if !ok {
		errs = append(errs, "Provide a nonempty workspace UUID using --workspace-id or --workspace; workspace names are not supported.")
	}
	if !validBudget(w.maxWait) {
		errs = append(errs, "--max-wait-seconds must be finite, greater than zero, and at most 600 seconds.")
	}
	if in.Description != nil && utf16Len(*in.Description) > 256 {
		errs = append(errs, "--description must be at most 256 characters.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	src := definitionInput{in.Definition, in.DefinitionDirectory, in.DefinitionFiles, in.DefinitionFormat}
	res, err := func() (*createResult, error) {
		if in.CreationPayload != nil && src.hasInput() {
			return nil, itemInputError("--creation-payload cannot be combined with any inline or saved-file definition option.")
		}
		if in.Options != nil && !src.hasInput() {
			return nil, itemInputError("--options requires an inline or saved-file definition source.")
		}
		folder, ok := parseOptID(in.FolderID)
		if !ok {
			return nil, itemInputError("--folder-id must be a nonempty UUID for an existing Fabric folder.")
		}
		payload, err := parseJSONObject(in.CreationPayload, "--creation-payload", maxJSONBytes)
		if err != nil {
			return nil, err
		}
		opts, err := parseJSONObject(in.Options, "--options", maxJSONBytes)
		if err != nil {
			return nil, err
		}
		labels, err := parseLabelSettings(in.SensitivityLabelSettings)
		if err != nil {
			return nil, err
		}
		def, err := a.readDefinitionInput(src)
		if err != nil {
			return nil, err
		}
		body := struct {
			DisplayName              string          `json:"displayName"`
			Type                     string          `json:"type"`
			Description              *string         `json:"description,omitempty"`
			Definition               *definition     `json:"definition,omitempty"`
			FolderID                 *uuid.UUID      `json:"folderId,omitempty"`
			CreationPayload          json.RawMessage `json:"creationPayload,omitempty"`
			Options                  json.RawMessage `json:"options,omitempty"`
			SensitivityLabelSettings *labelSettings  `json:"sensitivityLabelSettings,omitempty"`
		}{in.DisplayName, in.ItemType, in.Description, def, folder, payload, opts, labels}
		resp, _, err := a.send(ctx, http.MethodPost, "/workspaces/"+ws.String()+"/items", body)
		if err != nil {
			return nil, err
		}
		item, rc, err := runOperation(ctx, a, resp, opContract[*fabricItem]{
			completed: map[int]bool{http.StatusCreated: true}, read: readCreatedItem(ws),
		}, w)
		if err != nil {
			return nil, err
		}
		out := &createResult{Operation: rc}
		if item != nil {
			out.Item = *item
		}
		return out, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := baseStatus(f)
		switch {
		case f.kind == kInput:
			status = http.StatusBadRequest
		case f.op != nil:
			status = http.StatusRequestTimeout
		}
		var msg string
		switch {
		case f.kind == kInput:
			msg = f.msg
		case f.kind == kAuth && credentialUnavailable(err):
			msg = "Fabric credentials are unavailable. Authenticate the configured Fabric identity before creating an item"
		case f.cls == "operation" && f.status == http.StatusBadGateway:
			msg = f.msg
		case f.kind == kHTTP && f.status == 0:
			msg = "Service unavailable or network connectivity issues prevented Fabric item creation. Check whether the item was created before retrying"
		case f.kind == kCanceled || f.kind == kTimeout:
			msg = "Fabric item creation timed out or was canceled. Check whether the item was created before retrying"
		default:
			msg = messages{
				http.StatusBadRequest: "Fabric rejected the item creation request. Check the workspace UUID, item type, display name, and description; " +
					"also check any definition, creation-payload, folder-id, options, or sensitivity-label-settings",
				http.StatusUnauthorized:    "Authentication failed while creating the Fabric item. Check the configured Fabric identity and its access to the workspace",
				http.StatusForbidden:       "Fabric denied item creation. Check workspace permissions and whether the item type is supported and enabled for the tenant and capacity",
				http.StatusNotFound:        "The Fabric workspace was not found, or the caller does not have access",
				http.StatusConflict:        "Item creation conflicts with an existing item name or workspace state. Check for an existing item before retrying",
				http.StatusTooManyRequests: "Fabric throttled item creation or reached a capacity limit. Retry the request later",
				0:                          "Fabric item creation could not be completed. Check whether the item was created before retrying",
			}.get(status)
			if _, known := map[int]bool{400: true, 401: true, 403: true, 404: true, 409: true, 429: true}[status]; !known && f.kind == kHTTP {
				msg = "Fabric item creation failed with HTTP " + strconv.Itoa(f.status) + ". Check whether the item was created before retrying"
			}
		}
		if f.op != nil {
			return response.Status(status, f.msg, &createResult{Operation: f.op}), nil, nil
		}
		return fail(status, msg), nil, nil
	}
	msg := "Success"
	if res.Operation != nil && res.Operation.Issue != nil {
		msg = res.Operation.Issue.Message
	}
	return response.Status(receiptStatus(res.Operation), msg, res), nil, nil
}
