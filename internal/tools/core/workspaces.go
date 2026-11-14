package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// This file ports the workspace and capacity commands.

type tag struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"displayName"`
}

func validTags(tags *[]*tag, allowEmptyName bool) bool {
	if tags == nil {
		return true
	}
	for _, t := range *tags {
		if t == nil || t.ID == uuid.Nil || !allowEmptyName && isBlank(t.DisplayName) {
			return false
		}
	}
	return true
}

// workspaceEntry ports FabricWorkspaceListEntry.
type workspaceEntry struct {
	ID             uuid.UUID  `json:"id"`
	DisplayName    string     `json:"displayName"`
	Type           *string    `json:"type,omitempty"`
	Description    *string    `json:"description,omitempty"`
	CapacityID     *uuid.UUID `json:"capacityId,omitempty"`
	CapacityRegion *string    `json:"capacityRegion,omitempty"`
	DomainID       *uuid.UUID `json:"domainId,omitempty"`
	Tags           *[]*tag    `json:"tags,omitempty"`
	APIEndpoint    *string    `json:"apiEndpoint,omitempty"`
}

// workspaceMetadata ports FabricWorkspaceMetadata.
type workspaceMetadata struct {
	ID                         uuid.UUID  `json:"id"`
	DisplayName                string     `json:"displayName"`
	Type                       *string    `json:"type,omitempty"`
	Description                *string    `json:"description,omitempty"`
	CapacityID                 *uuid.UUID `json:"capacityId,omitempty"`
	CapacityAssignmentProgress *string    `json:"capacityAssignmentProgress,omitempty"`
	CapacityRegion             *string    `json:"capacityRegion,omitempty"`
	DomainID                   *uuid.UUID `json:"domainId,omitempty"`
	WorkspaceIdentity          *struct {
		ApplicationID      *uuid.UUID `json:"applicationId,omitempty"`
		ServicePrincipalID *uuid.UUID `json:"servicePrincipalId,omitempty"`
	} `json:"workspaceIdentity,omitempty"`
	OneLakeEndpoints *struct {
		BlobEndpoint *string `json:"blobEndpoint,omitempty"`
		DfsEndpoint  *string `json:"dfsEndpoint,omitempty"`
	} `json:"oneLakeEndpoints,omitempty"`
	APIEndpoint *string `json:"apiEndpoint,omitempty"`
	Tags        *[]*tag `json:"tags,omitempty"`
}

// createdWorkspace ports CreatedWorkspaceMetadata.
type createdWorkspace struct {
	ID             uuid.UUID  `json:"id"`
	DisplayName    string     `json:"displayName"`
	Type           *string    `json:"type,omitempty"`
	Description    *string    `json:"description,omitempty"`
	CapacityID     *uuid.UUID `json:"capacityId,omitempty"`
	CapacityRegion *string    `json:"capacityRegion,omitempty"`
	DomainID       *uuid.UUID `json:"domainId,omitempty"`
	APIEndpoint    *string    `json:"apiEndpoint,omitempty"`
	Tags           *[]*tag    `json:"tags,omitempty"`
}

// updatedWorkspace ports WorkspaceUpdateResponse.
type updatedWorkspace struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"displayName"`
	Type        *string   `json:"type,omitempty"`
	Description *string   `json:"description,omitempty"`
}

// capacity ports FabricCapacityMetadata.
type capacity struct {
	ID          uuid.UUID `json:"id"`
	DisplayName string    `json:"displayName"`
	Sku         string    `json:"sku"`
	Region      string    `json:"region"`
	State       string    `json:"state"`
}

func (c *capacity) valid() bool {
	return c != nil && c.ID != uuid.Nil && !isBlank(c.DisplayName) && !isBlank(c.Sku) && !isBlank(c.Region) && !isBlank(c.State)
}

func optType(t *string) bool { return t == nil || !isBlank(*t) }

// readJSON decodes a successful response body into T; any failure is a
// JsonException.
func readJSON[T any](resp *http.Response, msg string) (T, error) {
	var out T
	b, err := readBody(resp, 64*maxJSONBytes, func() error { return jsonError(msg) })
	if err != nil {
		return out, err
	}
	if err := json.Unmarshal(b, &out); err != nil {
		return out, jsonError(msg)
	}
	return out, nil
}

// statusError ports the common "throttled with Retry-After, else
// HttpRequestException" handling of a non-200 response.
func statusError(resp *http.Response, msg, cls, subject string) *failure {
	discard(resp)
	if resp.StatusCode == http.StatusTooManyRequests {
		if r := parseRetryAfter(resp.Header); r != nil {
			return throttled(cls, subject, r)
		}
	}
	return httpError(nonSuccess(resp.StatusCode), msg)
}

// nonSuccess maps an unexpected 2xx to 502, as upstream does.
func nonSuccess(status int) int {
	if status >= 200 && status < 300 {
		return http.StatusBadGateway
	}
	return status
}

func query(params ...string) string {
	var q []string
	for i := 0; i+1 < len(params); i += 2 {
		if params[i+1] != "" {
			q = append(q, params[i]+"="+params[i+1])
		}
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + strings.Join(q, "&")
}

func boolString(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func optBool(b *bool) string {
	if b == nil {
		return ""
	}
	return boolString(*b)
}

func optToken(tok *string) string {
	if tok == nil {
		return ""
	}
	return encodeToken(*tok)
}

// standardStatus is the GetStatusCode override most read commands share:
// OperationCanceledException → 408, JsonException → 502.
func standardStatus(f *failure) int {
	switch f.kind {
	case kCanceled:
		return http.StatusRequestTimeout
	case kJSON:
		return http.StatusBadGateway
	}
	return baseStatus(f)
}

// messages is a GetErrorMessage table keyed by status; 0 is the default.
type messages map[int]string

func (m messages) get(status int) string {
	if s, ok := m[status]; ok {
		return s
	}
	return m[0]
}

// readFail is the error result of a read command with the standard status
// mapping, where a throttling exception of class cls supplies its own
// message.
func readFail(f *failure, cls string, m messages) *mcp.CallToolResult {
	status := standardStatus(f)
	if status == http.StatusTooManyRequests && cls != "" && f.cls == cls {
		return fail(status, f.msg)
	}
	return fail(status, m.get(status))
}

// --- list-workspaces ---

var workspaceRoles = []string{"Admin", "Member", "Contributor", "Viewer"}

// normalizeRoles ports WorkspaceListInputValidator.TryNormalizeRoles.
func normalizeRoles(roles *string) (*string, bool) {
	if roles == nil {
		return nil, true
	}
	var out []string
	for _, r := range strings.Split(*roles, ",") {
		r = strings.TrimSpace(r)
		canonical := ""
		for _, v := range workspaceRoles {
			if strings.EqualFold(v, r) {
				canonical = v
			}
		}
		if canonical == "" {
			return nil, false
		}
		if !contains(out, canonical) {
			out = append(out, canonical)
		}
	}
	s := strings.Join(out, ",")
	return &s, true
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func (a *Area) listWorkspaces(ctx context.Context, _ *mcp.CallToolRequest, in listWorkspacesInput) (*mcp.CallToolResult, any, error) {
	roles, ok := normalizeRoles(in.Roles)
	var errs []string
	if !ok {
		errs = append(errs, "--roles must be a comma-separated list of Admin, Member, Contributor, or Viewer without empty entries.")
	}
	if in.ContinuationToken != nil && isBlank(*in.ContinuationToken) {
		errs = append(errs, "--continuation-token must not be empty or whitespace. Omit it to request the first page.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	r := ""
	if roles != nil {
		r = escapeData(*roles)
	}
	type page struct {
		Value             *[]*workspaceEntry `json:"value"`
		ContinuationToken *string            `json:"continuationToken"`
		ContinuationURI   *string            `json:"continuationUri"`
	}
	p, err := func() (*page, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/workspaces"+query(
			"roles", r, "continuationToken", optToken(in.ContinuationToken),
			"preferWorkspaceSpecificEndpoints", optBool(in.PreferWorkspaceSpecificEndpoints)), nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, statusError(resp, "Unable to list Fabric workspaces.", "throttled", "request")
		}
		p, err := readJSON[*page](resp, "Fabric returned invalid workspace metadata.")
		if err != nil {
			return nil, err
		}
		if p == nil || p.Value == nil {
			return nil, jsonError("Fabric returned invalid workspace metadata.")
		}
		for _, w := range *p.Value {
			if w == nil || w.ID == uuid.Nil || isBlank(w.DisplayName) || !optType(w.Type) || !validTags(w.Tags, false) {
				return nil, jsonError("Fabric returned invalid workspace metadata.")
			}
		}
		return p, nil
	}()
	if err != nil {
		return readFail(asFailure(err), "throttled", messages{
			http.StatusBadRequest:      "Check the workspace role filter and continuation token",
			http.StatusUnauthorized:    "Authentication failed. Use a configured identity that can read Fabric workspaces",
			http.StatusForbidden:       "Access denied. Check the caller's workspace access, delegated Workspace.Read.All or Workspace.ReadWrite.All scope when applicable, and Fabric tenant settings for service principals",
			http.StatusNotFound:        "The Fabric workspace listing endpoint was not found",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric workspace request was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric workspace request timed out",
			http.StatusBadGateway:      "Fabric returned an invalid workspace list response",
			0:                          "Unable to list Fabric workspaces. Retry the request or check service availability",
		}), nil, nil
	}
	return response.Success(struct {
		Workspaces        []*workspaceEntry `json:"workspaces"`
		ContinuationToken *string           `json:"continuationToken,omitempty"`
		ContinuationURI   *string           `json:"continuationUri,omitempty"`
	}{*p.Value, p.ContinuationToken, p.ContinuationURI}), nil, nil
}

// --- get-workspace ---

func (a *Area) getWorkspace(ctx context.Context, _ *mcp.CallToolRequest, in getWorkspaceInput) (*mcp.CallToolResult, any, error) {
	id, ok := parseID(in.WorkspaceID)
	if !ok {
		return invalid([]string{"--workspace-id must be a nonempty UUID."}), nil, nil
	}
	ws, err := func() (*workspaceMetadata, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/workspaces/"+id.String()+query(
			"preferWorkspaceSpecificEndpoints", optBool(in.PreferWorkspaceSpecificEndpoints)), nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, statusError(resp, "Unable to retrieve Fabric workspace metadata.", "throttled", "request")
		}
		ws, err := readJSON[*workspaceMetadata](resp, "Fabric returned invalid workspace metadata.")
		if err != nil {
			return nil, err
		}
		if ws == nil || ws.ID != id || isBlank(ws.DisplayName) || !optType(ws.Type) || !validTags(ws.Tags, false) {
			return nil, jsonError("Fabric returned invalid workspace metadata.")
		}
		return ws, nil
	}()
	if err != nil {
		return readFail(asFailure(err), "throttled", messages{
			http.StatusBadRequest:      "Provide a valid workspace UUID and a Boolean endpoint preference when specified",
			http.StatusUnauthorized:    "Authentication failed. Use an identity authorized to read the Fabric workspace",
			http.StatusForbidden:       "Access denied. The caller needs Viewer or higher workspace access; delegated callers also need Workspace.Read.All or Workspace.ReadWrite.All",
			http.StatusNotFound:        "The Fabric workspace was not found. Check its ID and the caller's access",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric workspace request was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric workspace request timed out",
			http.StatusBadGateway:      "Fabric returned an invalid workspace metadata response",
			0:                          "Unable to retrieve Fabric workspace metadata. Retry the request or check service availability",
		}), nil, nil
	}
	return response.Success(map[string]any{"workspace": ws}), nil, nil
}

// --- create-workspace ---

// workspaceCreateErrors ports WorkspaceCreateInputValidator.GetErrors.
func workspaceCreateErrors(in createWorkspaceInput) []string {
	var errs []string
	if isBlank(in.DisplayName) || utf16Len(in.DisplayName) > 256 {
		errs = append(errs, "--display-name must not be empty or whitespace and must be at most 256 characters.")
	} else if strings.EqualFold(strings.TrimSpace(in.DisplayName), "Admin monitoring") {
		errs = append(errs, "--display-name cannot use the reserved workspace name 'Admin monitoring'.")
	}
	if in.Description != nil && utf16Len(*in.Description) > 4000 {
		errs = append(errs, "--description must be at most 4000 characters.")
	}
	if _, ok := parseOptID(in.CapacityID); !ok {
		errs = append(errs, "--capacity-id must be a nonempty UUID when supplied.")
	}
	if _, ok := parseOptID(in.DomainID); !ok {
		errs = append(errs, "--domain-id must be a nonempty UUID when supplied.")
	}
	return errs
}

func (a *Area) createWorkspace(ctx context.Context, _ *mcp.CallToolRequest, in createWorkspaceInput) (*mcp.CallToolResult, any, error) {
	if errs := workspaceCreateErrors(in); len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	capID, _ := parseOptID(in.CapacityID)
	domID, _ := parseOptID(in.DomainID)
	body := struct {
		DisplayName string     `json:"displayName"`
		Description *string    `json:"description,omitempty"`
		CapacityID  *uuid.UUID `json:"capacityId,omitempty"`
		DomainID    *uuid.UUID `json:"domainId,omitempty"`
	}{in.DisplayName, in.Description, capID, domID}
	type result struct {
		Workspace *createdWorkspace `json:"workspace"`
		Location  *string           `json:"location,omitempty"`
	}
	res, err := func() (*result, error) {
		resp, _, err := a.send(ctx, http.MethodPost, "/workspaces", body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusCreated {
			discard(resp)
			f := httpError(nonSuccess(resp.StatusCode), "Fabric did not return a successful workspace creation response.")
			f.cls = "createRequest"
			f.retry = parseRetryAfter(resp.Header)
			return nil, f
		}
		const invalid = "Fabric returned invalid created workspace metadata."
		ws, err := readJSON[*createdWorkspace](resp, invalid)
		if err != nil {
			return nil, err
		}
		if ws == nil || ws.ID == uuid.Nil || isBlank(ws.DisplayName) || !optType(ws.Type) ||
			ws.CapacityID != nil && *ws.CapacityID == uuid.Nil || ws.DomainID != nil && *ws.DomainID == uuid.Nil ||
			!validTags(ws.Tags, false) {
			return nil, jsonError(invalid)
		}
		var loc *string
		if values := resp.Header.Values("Location"); len(values) > 0 {
			if len(values) != 1 || isBlank(values[0]) {
				return nil, jsonError("Fabric returned an invalid workspace Location header.")
			}
			if _, err := url.Parse(values[0]); err != nil {
				return nil, jsonError("Fabric returned an invalid workspace Location header.")
			}
			loc = &values[0]
		}
		return &result{ws, loc}, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		const uncertain = "The workspace may already have been created. Check before making another creation request"
		msg := messages{
			http.StatusBadRequest:      "Check the display name and any supplied description, capacity UUID, or domain UUID. Verify the name is unused",
			http.StatusUnauthorized:    "Authentication failed. Use an identity authorized to create Fabric workspaces; delegated callers need Workspace.ReadWrite.All",
			http.StatusForbidden:       "Access denied. Check Fabric workspace-creation permission, the service-principal workspace-creation tenant setting when applicable, capacity contributor/admin access, and domain assignment permission",
			http.StatusNotFound:        "Fabric could not find a requested resource. Check the supplied capacity/domain IDs and the caller's access",
			http.StatusConflict:        "Fabric rejected workspace creation because of a conflict. Check whether the workspace name is already in use",
			http.StatusTooManyRequests: "Fabric throttled workspace creation. Wait before making further requests; creation is not automatically retried",
			http.StatusRequestTimeout:  "Workspace creation was canceled or timed out. " + uncertain,
			http.StatusGatewayTimeout:  "Workspace creation was canceled or timed out. " + uncertain,
			http.StatusBadGateway:      "Fabric returned an invalid or unexpected workspace creation response. " + uncertain,
			0:                          "Unable to confirm Fabric workspace creation. " + uncertain,
		}.get(status)
		if f.cls == "createRequest" && f.retry != nil {
			msg += ". Retry-After: " + f.retry.String()
		}
		return fail(status, msg), nil, nil
	}
	return response.SuccessStatus(http.StatusCreated, res), nil, nil
}

// --- update-workspace ---

func (a *Area) updateWorkspace(ctx context.Context, _ *mcp.CallToolRequest, in updateWorkspaceInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	id, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	if in.DisplayName != nil {
		if isBlank(*in.DisplayName) || utf16Len(*in.DisplayName) > 256 {
			errs = append(errs, "--display-name must not be empty or whitespace-only and must contain no more than 256 characters.")
		}
		if strings.EqualFold(*in.DisplayName, "Admin monitoring") {
			errs = append(errs, "--display-name cannot use the reserved workspace name Admin monitoring.")
		}
	}
	if in.Description != nil && utf16Len(*in.Description) > 4000 {
		errs = append(errs, "--description must contain no more than 4000 characters.")
	}
	if in.DisplayName == nil && in.Description == nil {
		errs = append(errs, "Provide at least one update: --display-name or --description. An empty description clears it.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	body := struct {
		DisplayName *string `json:"displayName,omitempty"`
		Description *string `json:"description,omitempty"`
	}{in.DisplayName, in.Description}
	ws, err := func() (*updatedWorkspace, error) {
		resp, _, err := a.send(ctx, http.MethodPatch, "/workspaces/"+id.String(), body)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			if resp.StatusCode == http.StatusTooManyRequests {
				if r := parseRetryAfter(resp.Header); r != nil {
					return nil, throttled("updateThrottled", "update", r)
				}
			}
			return nil, httpError(nonSuccess(resp.StatusCode), "Unable to update Fabric workspace metadata.")
		}
		ws, err := readJSON[*updatedWorkspace](resp, "Fabric returned invalid workspace metadata.")
		if err != nil {
			return nil, err
		}
		if ws == nil || ws.ID != id || isBlank(ws.DisplayName) || !optType(ws.Type) {
			return nil, jsonError("Fabric returned invalid workspace metadata.")
		}
		return ws, nil
	}()
	if err != nil {
		const verify = "The workspace update was canceled or timed out. It may have been applied; verify the workspace before retrying"
		return readFail(asFailure(err), "updateThrottled", messages{
			http.StatusBadRequest:      "The update was rejected. Check the workspace UUID, supplied properties, and workspace naming rules",
			http.StatusUnauthorized:    "Authentication failed. Use an identity authorized to update the Fabric workspace",
			http.StatusForbidden:       "Access denied. The caller needs the workspace Admin role; delegated callers also need Workspace.ReadWrite.All",
			http.StatusNotFound:        "The Fabric workspace was not found. Check its ID and the caller's access",
			http.StatusConflict:        "The update conflicts with the workspace state or an existing workspace name. Workspace names must be unique within the tenant",
			http.StatusTooManyRequests: "Fabric throttled the update. Wait before retrying",
			http.StatusRequestTimeout:  verify,
			http.StatusGatewayTimeout:  verify,
			http.StatusBadGateway:      "Fabric returned an invalid workspace update response. The update may have been applied",
			0:                          "Unable to complete the workspace update. Check service availability and verify the workspace before retrying",
		}), nil, nil
	}
	return response.Success(map[string]any{"workspace": ws}), nil, nil
}

// --- delete-workspace ---

func (a *Area) deleteWorkspace(ctx context.Context, _ *mcp.CallToolRequest, in deleteWorkspaceInput) (*mcp.CallToolResult, any, error) {
	id, ok := parseID(in.WorkspaceID)
	if !ok {
		return invalid([]string{"--workspace-id must be a nonempty UUID."}), nil, nil
	}
	resp, _, err := a.send(ctx, http.MethodDelete, "/workspaces/"+id.String(), nil)
	if err == nil {
		if resp.StatusCode == http.StatusOK {
			discard(resp)
			return response.Success(map[string]any{"workspaceId": id, "deleted": true}), nil, nil
		}
		if ctx.Err() != nil {
			discard(resp)
			err = ctx.Err()
		} else {
			err = statusError(resp, "Fabric workspace deletion was not confirmed.", "throttled", "request")
		}
	}
	f := asFailure(err)
	status := baseStatus(f)
	if f.kind == kCanceled {
		status = http.StatusRequestTimeout
	}
	if status == http.StatusTooManyRequests && f.cls == "throttled" {
		return fail(status, f.msg), nil, nil
	}
	if status == http.StatusBadRequest && f.kind == kArg {
		return fail(status, "Provide a nonempty workspace UUID"), nil, nil
	}
	return fail(status, messages{
		http.StatusBadRequest:      "Fabric rejected workspace deletion. Check the workspace state and tenant restrictions before trying again. Deletion was not confirmed",
		http.StatusUnauthorized:    "Authentication failed. Use an identity authorized to delete the Fabric workspace",
		http.StatusForbidden:       "Access denied. The caller needs the Admin workspace role; delegated callers also need Workspace.ReadWrite.All",
		http.StatusNotFound:        "The Fabric workspace was not found. Check its ID and the caller's access. Deletion was not confirmed",
		http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
		http.StatusRequestTimeout:  "The Fabric workspace deletion request was canceled or timed out; its outcome is not confirmed",
		http.StatusGatewayTimeout:  "The Fabric workspace deletion request timed out; its outcome is not confirmed",
		http.StatusBadGateway:      "Fabric returned an unexpected response; workspace deletion was not confirmed",
		0:                          "Unable to confirm Fabric workspace deletion. Check the workspace state and service availability before attempting another deletion",
	}.get(status)), nil, nil
}

// --- assign-workspace-to-capacity ---

// capacityAssignmentMessage ports WorkspaceCapacityAssignmentException.
func capacityAssignmentMessage(status int, retrySeconds *int64) string {
	msg := messages{
		http.StatusBadRequest:      "Invalid capacity assignment request. Check the workspace and capacity IDs and the documented region and capacity restrictions.",
		http.StatusUnauthorized:    "Authentication failed. Use an identity authorized for Fabric with Capacity.ReadWrite.All and Workspace.ReadWrite.All scopes.",
		http.StatusForbidden:       "Access denied. Workspace Admin and capacity Contributor or Admin permissions are required, along with Capacity.ReadWrite.All and Workspace.ReadWrite.All scopes.",
		http.StatusNotFound:        "The workspace or capacity was not found or is not accessible. Verify both IDs and the caller's permissions.",
		http.StatusConflict:        "The capacity assignment conflicts with the current workspace state. Inspect the workspace before submitting again.",
		http.StatusTooManyRequests: "Fabric rate limited the capacity assignment request. Follow Retry-After guidance before making further requests.",
		http.StatusBadGateway:      "Fabric returned an unexpected response instead of 202 Accepted. Assignment acceptance could not be confirmed; inspect the workspace before submitting again.",
		http.StatusGatewayTimeout:  "The capacity assignment request timed out. Acceptance could not be confirmed; inspect the workspace before submitting again.",
		0:                          "The capacity assignment request failed. Acceptance could not be confirmed; inspect the workspace before submitting again.",
	}.get(status)
	if retrySeconds != nil && *retrySeconds >= 0 {
		msg += " Retry-After: wait at least " + strconv.FormatInt(*retrySeconds, 10) + " seconds before making further requests."
	}
	return msg
}

func (a *Area) assignWorkspaceToCapacity(ctx context.Context, _ *mcp.CallToolRequest, in assignWorkspaceToCapacityInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty GUID.")
	}
	cp, ok := parseID(in.CapacityID)
	if !ok {
		errs = append(errs, "--capacity-id must be a nonempty GUID.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	resp, _, err := a.send(ctx, http.MethodPost, "/workspaces/"+ws.String()+"/assignToCapacity", map[string]any{"capacityId": cp})
	var status int
	var retry *int64
	switch {
	case err != nil:
		f := asFailure(err)
		status = baseStatus(f)
		if f.kind == kCanceled {
			status = http.StatusGatewayTimeout
		}
	case resp.StatusCode != http.StatusAccepted:
		discard(resp)
		status = nonSuccess(resp.StatusCode)
		retry = parseRetryAfter(resp.Header).seconds()
	default:
		discard(resp)
		return response.SuccessMessage(http.StatusAccepted, "Request accepted. Capacity assignment is pending; completion has not been verified.",
			map[string]any{"workspaceId": ws, "capacityId": cp, "accepted": true, "state": "Pending"}), nil, nil
	}
	msg := capacityAssignmentMessage(status, retry)
	return response.Exc(status, "WorkspaceCapacityAssignmentException", msg, msg), nil, nil
}

// --- list-capacities / get-capacity ---

func (a *Area) listCapacities(ctx context.Context, _ *mcp.CallToolRequest, in listCapacitiesInput) (*mcp.CallToolResult, any, error) {
	if in.ContinuationToken != nil && isBlank(*in.ContinuationToken) {
		return invalid([]string{"--continuation-token must not be empty or whitespace."}), nil, nil
	}
	type page struct {
		Value             *[]*capacity `json:"value"`
		ContinuationToken *string      `json:"continuationToken"`
		ContinuationURI   *string      `json:"continuationUri"`
	}
	p, err := func() (*page, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/capacities"+query("continuationToken", optToken(in.ContinuationToken)), nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, statusError(resp, "Unable to list Fabric capacities.", "throttled", "request")
		}
		const invalid = "Fabric returned an invalid capacity metadata page."
		p, err := readJSON[*page](resp, invalid)
		if err != nil {
			return nil, err
		}
		if p == nil || p.Value == nil {
			return nil, jsonError(invalid)
		}
		for _, c := range *p.Value {
			if !c.valid() {
				return nil, jsonError(invalid)
			}
		}
		return p, nil
	}()
	if err != nil {
		return readFail(asFailure(err), "throttled", messages{
			http.StatusBadRequest:      "Check the continuation token and pass it unchanged from a previous capacity listing",
			http.StatusUnauthorized:    "Authentication failed. Use the configured identity with permission to read Fabric capacities",
			http.StatusForbidden:       "Access denied. The caller needs capacity administrator or contributor access; delegated calls also require Capacity.Read.All or Capacity.ReadWrite.All",
			http.StatusNotFound:        "The Fabric capacity listing was not found. Check service availability and the caller's access",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric capacity listing was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric capacity listing timed out",
			http.StatusBadGateway:      "Fabric returned an invalid capacity metadata page",
			0:                          "Unable to list Fabric capacities. Retry the request or check service availability",
		}), nil, nil
	}
	return response.Success(struct {
		Capacities        []*capacity `json:"capacities"`
		ContinuationToken *string     `json:"continuationToken,omitempty"`
		ContinuationURI   *string     `json:"continuationUri,omitempty"`
	}{*p.Value, p.ContinuationToken, p.ContinuationURI}), nil, nil
}

func (a *Area) getCapacity(ctx context.Context, _ *mcp.CallToolRequest, in getCapacityInput) (*mcp.CallToolResult, any, error) {
	id, ok := parseID(in.CapacityID)
	if !ok {
		return invalid([]string{"--capacity-id must be a nonempty UUID."}), nil, nil
	}
	c, err := func() (*capacity, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/capacities/"+id.String(), nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, statusError(resp, "Unable to retrieve Fabric capacity metadata.", "throttled", "request")
		}
		c, err := readJSON[*capacity](resp, "Fabric returned invalid capacity metadata.")
		if err != nil {
			return nil, err
		}
		if !c.valid() || c.ID != id {
			return nil, jsonError("Fabric returned invalid capacity metadata.")
		}
		return c, nil
	}()
	if err != nil {
		return readFail(asFailure(err), "throttled", messages{
			http.StatusBadRequest:      "Provide a valid capacity UUID",
			http.StatusUnauthorized:    "Authentication failed. Use an identity authorized to read the Fabric capacity",
			http.StatusForbidden:       "Access denied. The caller needs Administrator or Contributor permission on the capacity; delegated callers also need Capacity.Read.All or Capacity.ReadWrite.All",
			http.StatusNotFound:        "The Fabric capacity was not found. Check its ID and the caller's access",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric capacity request was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric capacity request timed out",
			http.StatusBadGateway:      "Fabric returned an invalid capacity metadata response",
			0:                          "Unable to retrieve Fabric capacity metadata. Retry the request or check service availability",
		}), nil, nil
	}
	return response.Success(map[string]any{"capacity": c}), nil, nil
}

// utf16Len is .NET's string.Length.
func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}
