package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// This file ports the workspace role-assignment commands.

// principal ports FabricPrincipal.
type principal struct {
	ID          uuid.UUID `json:"id"`
	Type        *string   `json:"type"`
	DisplayName *string   `json:"displayName,omitempty"`
	UserDetails *struct {
		UserPrincipalName *string `json:"userPrincipalName,omitempty"`
	} `json:"userDetails,omitempty"`
	GroupDetails *struct {
		GroupType *string `json:"groupType,omitempty"`
	} `json:"groupDetails,omitempty"`
	ServicePrincipalDetails *struct {
		AadAppID *uuid.UUID `json:"aadAppId,omitempty"`
	} `json:"servicePrincipalDetails,omitempty"`
	ServicePrincipalProfileDetails *struct {
		ParentPrincipal *principal `json:"parentPrincipal,omitempty"`
	} `json:"servicePrincipalProfileDetails,omitempty"`
}

// roleAssignment ports FabricWorkspaceRoleAssignment.
type roleAssignment struct {
	ID        uuid.UUID  `json:"id"`
	Principal *principal `json:"principal"`
	Role      *string    `json:"role"`
}

const (
	invalidRoleAssignment     = "Fabric returned invalid workspace role-assignment metadata."
	invalidRoleAssignmentPage = "Fabric returned invalid workspace role-assignment page metadata. No partial page was returned."
	maxRoleAssignmentDepth    = 61 // MaxRetainedRoleAssignmentDepth
)

func validPrincipal(p *principal) bool {
	if p == nil || p.ID == uuid.Nil || p.Type == nil || isBlank(*p.Type) ||
		p.ServicePrincipalDetails != nil && p.ServicePrincipalDetails.AadAppID != nil && *p.ServicePrincipalDetails.AadAppID == uuid.Nil ||
		p.GroupDetails != nil && p.GroupDetails.GroupType != nil && isBlank(*p.GroupDetails.GroupType) {
		return false
	}
	if d := p.ServicePrincipalProfileDetails; d != nil && d.ParentPrincipal != nil {
		return validPrincipal(d.ParentPrincipal)
	}
	return true
}

// readRoleAssignment ports FabricCoreService.ReadWorkspaceRoleAssignment.
func readRoleAssignment(raw []byte) (*roleAssignment, error) {
	var a *roleAssignment
	if json.Unmarshal(raw, &a) != nil || a == nil || a.ID == uuid.Nil || a.Role == nil || isBlank(*a.Role) || !validPrincipal(a.Principal) {
		return nil, jsonError(invalidRoleAssignment)
	}
	return a, nil
}

func readRoleAssignmentBody(b []byte) (*roleAssignment, error) {
	if validateMetadataJSON(b) != nil {
		return nil, jsonError(invalidRoleAssignment)
	}
	return readRoleAssignment(b)
}

// --- list / get ---

func (a *Area) listWorkspaceRoleAssignments(ctx context.Context, _ *mcp.CallToolRequest, in listWorkspaceRoleAssignmentsInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	if !validToken(in.ContinuationToken) {
		errs = append(errs, invalidTokenMessage)
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	type result struct {
		RoleAssignments   []*roleAssignment `json:"roleAssignments"`
		ContinuationToken *string           `json:"continuationToken,omitempty"`
		RetryAfterSeconds *int64            `json:"retryAfterSeconds,omitempty"`
		RetryAfterUtc     *netTime          `json:"retryAfterUtc,omitempty"`
	}
	path := "/workspaces/" + ws.String() + "/roleAssignments"
	res, err := func() (*result, error) {
		resp, _, err := a.send(ctx, http.MethodGet, path+query("continuationToken", optToken(in.ContinuationToken)), nil)
		if err != nil {
			return nil, err
		}
		r := parseRetryAfter(resp.Header)
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			f := httpError(nonSuccess(resp.StatusCode), "Unable to list Fabric workspace role assignments.")
			f.cls, f.retry = "roleList", r
			return nil, f
		}
		if !isJSON(resp) {
			discard(resp)
			return nil, jsonError("Fabric returned a non-JSON workspace role-assignment page.")
		}
		b, err := readBody(resp, maxJSONBytes, func() error {
			return jsonError("The Fabric workspace role-assignment page exceeds the 1 MiB (1,048,576 byte) limit. No partial page was returned.")
		})
		if err != nil {
			return nil, err
		}
		var page map[string]json.RawMessage
		if validateMetadataJSON(b) != nil || json.Unmarshal(b, &page) != nil {
			return nil, jsonError(invalidRoleAssignmentPage)
		}
		var values []json.RawMessage
		if raw, ok := page["value"]; !ok || json.Unmarshal(raw, &values) != nil || values == nil {
			return nil, jsonError(invalidRoleAssignmentPage)
		}
		collection, _ := url.Parse(a.base + path)
		tok, err := jobPageToken(page, collection, false, invalidRoleAssignmentPage)
		if err != nil {
			return nil, jsonError(invalidRoleAssignmentPage)
		}
		out := &result{RoleAssignments: []*roleAssignment{}, ContinuationToken: tok}
		for _, v := range values {
			ra, err := readRoleAssignment(v)
			if err != nil {
				return nil, jsonError(invalidRoleAssignmentPage)
			}
			out.RoleAssignments = append(out.RoleAssignments, ra)
		}
		if jsonDepth(out) > maxRoleAssignmentDepth {
			return nil, jsonError(invalidRoleAssignmentPage)
		}
		out.RetryAfterSeconds, out.RetryAfterUtc = r.seconds(), r.utc()
		return out, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		msg := messages{
			http.StatusBadRequest:      "Check the workspace UUID and unchanged continuation token",
			http.StatusUnauthorized:    "Authentication failed. Use the configured identity with permission to read workspace role assignments",
			http.StatusForbidden:       "Access denied. The caller needs workspace Member or higher access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All. Service principals and managed identities also require the tenant setting permitting service principals to use Fabric APIs",
			http.StatusNotFound:        "The Fabric workspace was not found. Check its ID and the caller's access",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric workspace role-assignment listing was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric workspace role-assignment listing timed out",
			http.StatusBadGateway:      "Fabric returned an invalid workspace role-assignment page. Expected required assignment and principal metadata with consistent continuation information, up to 1 MiB and 64 JSON levels, with at most 61 retained output levels. No partial page was returned",
			0:                          "Unable to list Fabric workspace role assignments. Retry the request or check service availability",
		}.get(status)
		if f.cls == "roleList" && f.retry != nil {
			msg += f.retry.retryGuidance()
		}
		return fail(status, msg), nil, nil
	}
	return response.Success(res), nil, nil
}

type roleAssignmentResult struct {
	RoleAssignment    *roleAssignment `json:"roleAssignment"`
	RetryAfterSeconds *int64          `json:"retryAfterSeconds,omitempty"`
	RetryAfterUtc     *netTime        `json:"retryAfterUtc,omitempty"`
}

func (a *Area) getWorkspaceRoleAssignment(ctx context.Context, _ *mcp.CallToolRequest, in getWorkspaceRoleAssignmentInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	id, ok := parseID(in.WorkspaceRoleAssignmentID)
	if !ok {
		errs = append(errs, "--workspace-role-assignment-id must be a nonempty UUID.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	res, err := func() (*roleAssignmentResult, error) {
		resp, _, err := a.send(ctx, http.MethodGet, "/workspaces/"+ws.String()+"/roleAssignments/"+id.String(), nil)
		if err != nil {
			return nil, err
		}
		r := parseRetryAfter(resp.Header)
		exc := func(status int) *failure {
			f := httpError(status, "Unable to retrieve Fabric workspace role-assignment metadata.")
			f.cls, f.retry = "roleGet", r
			return f
		}
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			return nil, exc(nonSuccess(resp.StatusCode))
		}
		if !isJSON(resp) {
			discard(resp)
			return nil, exc(http.StatusBadGateway)
		}
		b, err := readBody(resp, maxJSONBytes, func() error { return jsonError(invalidRoleAssignment) })
		if err != nil {
			if ctx.Err() != nil {
				return nil, exc(http.StatusRequestTimeout)
			}
			if asFailure(err).kind == kJSON {
				return nil, exc(http.StatusBadGateway)
			}
			return nil, exc(http.StatusServiceUnavailable)
		}
		ra, err := readRoleAssignmentBody(b)
		if err != nil || ra.ID != id {
			return nil, exc(http.StatusBadGateway)
		}
		out := &roleAssignmentResult{RoleAssignment: ra}
		if jsonDepth(out) > maxRoleAssignmentDepth {
			return nil, exc(http.StatusBadGateway)
		}
		out.RetryAfterSeconds, out.RetryAfterUtc = r.seconds(), r.utc()
		return out, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		msg := messages{
			http.StatusBadRequest:      "Provide valid workspace and workspace role assignment UUIDs; the assignment ID is not a principal-ID lookup",
			http.StatusUnauthorized:    "Authentication failed. Use the configured identity with permission to read workspace role assignments",
			http.StatusForbidden:       "Access denied. The caller needs workspace Member or higher access; delegated calls require Workspace.Read.All or Workspace.ReadWrite.All. Service principals and managed identities also require the tenant setting permitting service principals to use Fabric APIs",
			http.StatusNotFound:        "The Fabric workspace or role assignment was not found. Check the supplied IDs and the caller's access",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric workspace role-assignment request was canceled or timed out",
			http.StatusGatewayTimeout:  "The Fabric workspace role-assignment request timed out",
			http.StatusBadGateway:      "Fabric returned invalid workspace role-assignment metadata. Expected a matching assignment ID and required principal metadata, up to 1 MiB and 64 JSON levels, with at most 61 retained output levels. No partial result was returned",
			0:                          "Unable to retrieve Fabric workspace role-assignment metadata. Retry the request or check service availability",
		}.get(status)
		if f.cls == "roleGet" && f.retry != nil {
			msg += f.retry.retryGuidance()
		}
		return fail(status, msg), nil, nil
	}
	return response.Success(res), nil, nil
}

// --- add / update ---

// validateRole ports WorkspaceRoleAssignmentInput.ValidateRole.
func validateRole(role string) error {
	switch {
	case isBlank(role):
		return itemInputError("--role is required and must be a nonempty string. No access level is inferred.")
	case !utf8.ValidString(role):
		return itemInputError("--role must contain valid Unicode. No replacement access level is selected.")
	case len(role) > maxJSONBytes:
		return itemInputError("--role exceeds the 1 MiB request budget.")
	}
	return nil
}

// validatePrincipalInput ports WorkspaceRoleAssignmentAddInput.ValidatePrincipal.
func validatePrincipalInput(raw json.RawMessage) (id uuid.UUID, typ string, ok bool) {
	obj, err := strictObject(raw, []string{"id", "type", "groupDetails", "servicePrincipalProfileDetails"}, nil)
	if err != nil || obj == nil {
		return uuid.Nil, "", false
	}
	var idStr, t string
	if json.Unmarshal(obj["id"], &idStr) != nil || len(idStr) != 36 || json.Unmarshal(obj["type"], &t) != nil || isBlank(t) {
		return uuid.Nil, "", false
	}
	if id, ok = parseID(idStr); !ok {
		return uuid.Nil, "", false
	}
	if g, has := obj["groupDetails"]; has {
		gd, err := strictObject(g, []string{"groupType"}, nil)
		if t != "Group" || err != nil || gd == nil {
			return uuid.Nil, "", false
		}
		if gt, has := gd["groupType"]; has {
			var s string
			if json.Unmarshal(gt, &s) != nil || isBlank(s) {
				return uuid.Nil, "", false
			}
		}
	}
	if p, has := obj["servicePrincipalProfileDetails"]; has {
		pd, err := strictObject(p, []string{"parentPrincipal"}, nil)
		if t != "ServicePrincipalProfile" || err != nil || pd == nil {
			return uuid.Nil, "", false
		}
		if parent, has := pd["parentPrincipal"]; has {
			if _, _, ok := validatePrincipalInput(parent); !ok {
				return uuid.Nil, "", false
			}
		}
	}
	return id, t, true
}

type addRoleRequest struct {
	ws          uuid.UUID
	principalID uuid.UUID
	typ         string
	body        []byte
}

// addRoleInput ports WorkspaceRoleAssignmentAddInput.CreateRequest.
func addRoleInput(in addWorkspaceRoleAssignmentInput) (*addRoleRequest, error) {
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		return nil, itemInputError("--workspace-id must be a nonempty UUID.")
	}
	if err := validateRole(in.Role); err != nil {
		return nil, err
	}
	raw, err := parseJSONObject(&in.Principal, "--principal", maxJSONBytes)
	if err != nil {
		return nil, err
	}
	id, typ, ok := validatePrincipalInput(raw)
	if !ok || validateMetadataJSON(raw) != nil {
		return nil, itemInputError("Invalid principal JSON. Supply a nonempty id UUID and type, with only the documented writable groupDetails or servicePrincipalProfileDetails for that type. Use valid Unicode; read-only, unknown, and duplicate fields are not accepted.")
	}
	body, _ := json.Marshal(map[string]any{"principal": raw, "role": in.Role})
	if len(body) > maxJSONBytes {
		return nil, itemInputError("The serialized role-assignment request must not exceed the 1 MiB (1,048,576 byte) tool budget.")
	}
	return &addRoleRequest{ws, id, typ, body}, nil
}

// roleMutationText holds one role command's messages.
type roleMutationText struct {
	inputSuffix, confirmed, notSent, rejected, maybe, replay string
	table                                                    messages
}

// roleMutationFail ports the GetErrorMessage of the add and update
// role-assignment commands.
func roleMutationFail(f *failure, t roleMutationText) *mcp.CallToolResult {
	status := baseStatus(f)
	switch f.kind {
	case kInput:
		return fail(http.StatusBadRequest, f.msg+t.inputSuffix)
	case kCanceled:
		status = http.StatusRequestTimeout
	}
	msg := t.table.get(status)
	if f.confirmed {
		msg = t.confirmed
	}
	switch {
	case f.notSent || f.kind == kAuth || f.kind == kArg:
		msg += t.notSent
	case !f.confirmed:
		if f.respStatus >= 400 && f.respStatus < 500 && f.respStatus != http.StatusRequestTimeout {
			msg += t.rejected
		} else {
			msg += t.maybe
		}
	}
	msg += ". No automatic retry was attempted"
	if f.cls == "mutation" {
		if f.respStatus != 0 && !f.confirmed {
			msg += ". Fabric response HTTP " + strconv.Itoa(f.respStatus)
		}
		if f.code != "" {
			msg += ". Fabric error code: " + f.code
		}
		if f.metaUnavailable {
			msg += ". Additional error metadata was unavailable"
		}
		if f.retry != nil {
			msg += f.retry.requestGuidance() + "; this is not permission to replay the " + t.replay
		}
	}
	return fail(status, msg)
}

func (a *Area) addWorkspaceRoleAssignment(ctx context.Context, _ *mcp.CallToolRequest, in addWorkspaceRoleAssignmentInput) (*mcp.CallToolResult, any, error) {
	req, err := addRoleInput(in)
	if err != nil {
		return invalid([]string{err.Error()}), nil, nil
	}
	m, err := mutate(ctx, a, http.MethodPost, "/workspaces/"+req.ws.String()+"/roleAssignments", req.body,
		"Fabric role-assignment creation did not return usable metadata.",
		func(b []byte) (*roleAssignment, error) {
			ra, err := readRoleAssignmentBody(b)
			if err != nil {
				return nil, err
			}
			if ra.Principal.ID != req.principalID || *ra.Principal.Type != req.typ || *ra.Role != in.Role ||
				jsonDepth(roleAssignmentResult{RoleAssignment: ra}) > maxRoleAssignmentDepth {
				return nil, jsonError(invalidRoleAssignment)
			}
			return ra, nil
		}, invalidRoleAssignment, invalidRoleAssignment, http.StatusCreated)
	if err != nil {
		return roleMutationFail(asFailure(err), roleMutationText{
			inputSuffix: " The role-assignment request was not sent.",
			confirmed:   "Fabric confirmed role-assignment creation (HTTP 201), but the returned metadata could not be read, validated against the requested principal and role, or serialized safely. Do not resubmit this grant; inspect workspace assignments separately",
			notSent:     ". The role-assignment request was not sent",
			rejected:    ". Fabric rejected the request; no fallback grant was attempted",
			maybe:       ". Access may already have been granted; inspect assignments separately before any new grant. Do not retry blindly",
			replay:      "grant",
			table: messages{
				http.StatusBadRequest:      "Fabric rejected the role-assignment request. Check the workspace, principal, and requested role",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. The caller needs workspace Member or higher access; delegated calls require Workspace.ReadWrite.All. Members can grant Member or lower; granting Admin requires Admin access. Service principals and managed identities also need the tenant setting permitting Fabric API access. No alternative identity or role was selected",
				http.StatusNotFound:        "The Fabric workspace or principal was not found, or the caller does not have access",
				http.StatusConflict:        "Fabric rejected the grant because of a conflict. An existing assignment is not returned as success and no update was attempted",
				http.StatusTooManyRequests: "Fabric throttled this role-assignment request",
				http.StatusRequestTimeout:  "The role-assignment request was canceled or timed out",
				http.StatusGatewayTimeout:  "The role-assignment request was canceled or timed out",
				0:                          "Fabric role-assignment creation was not confirmed",
			},
		}), nil, nil
	}
	const guidance = "Fabric confirmed role-assignment creation (HTTP 201). No lookup, follow-up read, polling, automatic retry, or update was performed. Do not resubmit this grant."
	return response.SuccessMessage(http.StatusCreated, guidance, struct {
		roleAssignmentResult
		CreationStatus string `json:"creationStatus"`
		Guidance       string `json:"guidance"`
	}{roleAssignmentResult{m.value, m.retry.seconds(), m.retry.utc()}, "Created", guidance}), nil, nil
}

func (a *Area) updateWorkspaceRoleAssignment(ctx context.Context, _ *mcp.CallToolRequest, in updateWorkspaceRoleAssignmentInput) (*mcp.CallToolResult, any, error) {
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		return invalid([]string{"--workspace-id must be a nonempty UUID."}), nil, nil
	}
	id, ok := parseID(in.WorkspaceRoleAssignmentID)
	if !ok {
		return invalid([]string{"--workspace-role-assignment-id must be a nonempty UUID identifying the existing assignment, not its principal."}), nil, nil
	}
	if err := validateRole(in.Role); err != nil {
		return invalid([]string{err.Error()}), nil, nil
	}
	body, _ := json.Marshal(map[string]string{"role": in.Role})
	if len(body) > maxJSONBytes {
		return invalid([]string{"The serialized role-assignment request must not exceed the 1 MiB (1,048,576 byte) tool budget."}), nil, nil
	}
	m, err := mutate(ctx, a, http.MethodPatch, "/workspaces/"+ws.String()+"/roleAssignments/"+id.String(), body,
		"Fabric role-assignment update did not return usable metadata.",
		func(b []byte) (*roleAssignment, error) {
			ra, err := readRoleAssignmentBody(b)
			if err != nil {
				return nil, err
			}
			if ra.ID != id || *ra.Role != in.Role || jsonDepth(roleAssignmentResult{RoleAssignment: ra}) > maxRoleAssignmentDepth {
				return nil, jsonError(invalidRoleAssignment)
			}
			return ra, nil
		}, invalidRoleAssignment, invalidRoleAssignment)
	if err != nil {
		return roleMutationFail(asFailure(err), roleMutationText{
			inputSuffix: " The role-assignment update request was not sent.",
			confirmed:   "Fabric confirmed role-assignment update (HTTP 200), but the returned metadata could not be read, validated against the requested assignment and role, or serialized safely. Do not resubmit this update; inspect the assignment separately",
			notSent:     ". The role-assignment update request was not sent",
			rejected:    ". Fabric rejected the request; no fallback permission change was attempted",
			maybe:       ". Access may already have changed; inspect the assignment separately before any new update. Do not retry blindly",
			replay:      "update",
			table: messages{
				http.StatusBadRequest:      "Fabric rejected the role-assignment update. Check the workspace, existing assignment, and requested role. The last admin's assignment cannot be changed",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. The caller needs workspace Admin access; delegated calls require Workspace.ReadWrite.All. The last admin's assignment cannot be changed. Service principals and managed identities also need the tenant setting permitting Fabric API access. No alternative identity or role was selected",
				http.StatusNotFound:        "The Fabric workspace or assignment was not found, or the caller does not have access. No assignment was created",
				http.StatusConflict:        "Fabric rejected the role-assignment update because of a conflict. No fallback or automatic conflict update was attempted",
				http.StatusTooManyRequests: "Fabric throttled this role-assignment update",
				http.StatusRequestTimeout:  "The role-assignment update request was canceled or timed out",
				http.StatusGatewayTimeout:  "The role-assignment update request was canceled or timed out",
				0:                          "Fabric role-assignment update was not confirmed",
			},
		}), nil, nil
	}
	const guidance = "Fabric confirmed role-assignment update (HTTP 200). No lookup, follow-up read, creation, polling, or automatic retry was performed. Do not resubmit this update."
	return response.SuccessMessage(http.StatusOK, guidance, struct {
		roleAssignmentResult
		UpdateStatus string `json:"updateStatus"`
		Guidance     string `json:"guidance"`
	}{roleAssignmentResult{m.value, m.retry.seconds(), m.retry.utc()}, "Updated", guidance}), nil, nil
}

// --- delete ---

func (a *Area) deleteWorkspaceRoleAssignment(ctx context.Context, _ *mcp.CallToolRequest, in deleteWorkspaceRoleAssignmentInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID. No DELETE request was sent.")
	}
	id, ok := parseID(in.WorkspaceRoleAssignmentID)
	if !ok {
		errs = append(errs, "--workspace-role-assignment-id must be a nonempty UUID identifying the existing assignment, not its principal. No DELETE request was sent.")
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	resp, sent, err := a.send(ctx, http.MethodDelete, "/workspaces/"+ws.String()+"/roleAssignments/"+id.String(), nil)
	var f *failure
	switch {
	case err != nil:
		e := asFailure(err)
		f = &failure{kind: kHTTP, cls: "mutation", notSent: !sent}
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
	case resp.StatusCode == http.StatusOK:
		discard(resp)
		const guidance = "Fabric confirmed deletion of the requested workspace role assignment (HTTP 200). " +
			"Only that assignment was removed, not the principal, group membership, workspace, or other assignments. " +
			"This does not establish that the principal has no remaining effective access. No assignment snapshot was returned. " +
			"No lookup, follow-up read, polling, or automatic retry was performed. Do not resubmit this confirmed deletion."
		return response.SuccessMessage(http.StatusOK, guidance, map[string]any{
			"workspaceId": ws, "workspaceRoleAssignmentId": id, "deletionStatus": "Deleted", "guidance": guidance,
		}), nil, nil
	default:
		f = &failure{kind: kHTTP, cls: "mutation", status: nonSuccess(resp.StatusCode),
			retry: parseRetryAfter(resp.Header), respStatus: resp.StatusCode}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			discard(resp)
		} else {
			f.code, f.metaUnavailable = readErrorCode(resp)
		}
	}
	msg := messages{
		http.StatusBadRequest:      "Fabric rejected the role-assignment deletion. Check the workspace and existing assignment UUIDs. The last admin's assignment cannot be deleted",
		http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
		http.StatusForbidden:       "Access denied. The caller needs workspace Admin access; delegated calls require Workspace.ReadWrite.All. The last admin's assignment cannot be deleted. Service principals and managed identities also need the tenant setting permitting Fabric API access. No alternative identity was selected",
		http.StatusNotFound:        "The Fabric workspace or assignment was not found or is inaccessible. This is not confirmed deletion",
		http.StatusConflict:        "Fabric rejected the role-assignment deletion because of a conflict. The last admin's assignment cannot be deleted",
		http.StatusTooManyRequests: "Fabric throttled this role-assignment deletion",
		http.StatusRequestTimeout:  "The role-assignment deletion request was canceled or timed out",
		http.StatusGatewayTimeout:  "The role-assignment deletion request was canceled or timed out",
		0:                          "Fabric role-assignment deletion was not confirmed",
	}.get(f.status)
	switch {
	case f.notSent:
		msg += ". No DELETE request was sent"
	case f.respStatus >= 400 && f.respStatus < 500 && f.respStatus != http.StatusRequestTimeout:
		msg += ". Fabric rejected the request; no fallback permission change was attempted"
	default:
		msg += ". The assignment may already have been deleted; inspect it with core_get-workspace-role-assignment separately before any new deletion. Do not retry blindly"
	}
	msg += ". No automatic retry or change to other assignments was attempted"
	if f.respStatus != 0 {
		msg += ". Fabric response HTTP " + strconv.Itoa(f.respStatus)
	}
	if safeErrorCode(f.code) {
		msg += ". Fabric error code: " + f.code
	}
	if f.metaUnavailable {
		msg += ". Optional Fabric error metadata was unavailable"
	}
	if f.retry != nil {
		msg += f.retry.requestGuidance() + "; this is not permission to replay the deletion"
	}
	return fail(f.status, strings.TrimSpace(msg)), nil, nil
}
