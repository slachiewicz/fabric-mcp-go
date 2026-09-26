package onelake

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

func (a *Area) registerSecurity(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "list-data-access-roles",
		Description: "List all data access roles defined on a single item (Lakehouse / Warehouse) —\n" +
			"the role-based policies that gate Tables/Files access for that item. Scoped\n" +
			"to one item per call; to inspect roles across multiple items, call once per\n" +
			"item. For looking up a specific role by name, fetch the list and pick by\n" +
			"name; there is no server-side search. Caller must be a workspace Admin\n" +
			"or Member on the item's workspace. Requires OneLake.Read.All.\n" +
			"Note: Built-in roles (e.g. DefaultReader) may include fabricItemMembers with\n" +
			"a 'sourcePath' field formatted as '<workspaceId>/<itemId>' — this is NOT a\n" +
			"OneLake file path; it identifies the workspace/item granting inherited access.",
		Annotations: readOnly("List OneLake Data Access Roles"),
	}, a.listDataAccessRoles)

	server.AddTool(r, &mcp.Tool{
		Name: "get-data-access-role",
		Description: "Get the full definition of a single data access role on a single item —\n" +
			"members, permissions, decision rules. Scoped to one role on one item per\n" +
			"call. Use after onelake_list-data-access-roles once you know which role\n" +
			"you need on which item. Distinct from onelake_get-principal-access,\n" +
			"which returns the effective (resolved) access for a given principal\n" +
			"across all roles on an item. Caller must be a workspace Admin or Member\n" +
			"on the item's workspace. Requires OneLake.Read.All.",
		Annotations: readOnly("Get OneLake Data Access Role"),
	}, a.getDataAccessRole)

	server.AddTool(r, &mcp.Tool{
		Name: "create-or-update-data-access-role",
		Description: "Upsert a single data access role on a single item. Use flat options (--name,\n" +
			"--entra-members, --permitted-paths, --permitted-actions) for the common case\n" +
			"of granting Read access. For advanced scenarios (multiple decision rules,\n" +
			"column/row constraints), pass the full JSON via --role-definition instead.\n" +
			"When flat options are provided, --role-definition is ignored.\n" +
			"Members can be specified by Entra object ID (GUID), email address, or UPN —\n" +
			"non-GUID values are automatically resolved via Microsoft Graph.\n" +
			"Caller must be a workspace Admin or Member. Requires OneLake.ReadWrite.All and\n" +
			"User.Read.All + GroupMember.Read.All for principal resolution.",
		Annotations: write("Create or Update OneLake Data Access Role", false, true),
	}, a.createOrUpdateDataAccessRole)

	server.AddTool(r, &mcp.Tool{
		Name: "delete-data-access-role",
		Description: "Delete a single data access role from a single item. Scoped to one role\n" +
			"on one item per call. Destructive — principals that gained access only\n" +
			"via this role lose it on this item. Does not affect roles on other items.\n" +
			"Caller must be a workspace Admin or Member on the item's workspace.\n" +
			"Requires OneLake.ReadWrite.All.",
		Annotations: write("Delete OneLake Data Access Role", true, true),
	}, a.deleteDataAccessRole)
}

// validateWorkspaceGUID ports the workspace-identifier validation every
// OneLake security and settings command shares: a workspace is required,
// and — unlike the workspace/item commands — must be a GUID; these commands
// don't support name-based resolution.
func validateWorkspaceGUID(id, name string) []string {
	var errs []string
	if strings.TrimSpace(id) == "" && strings.TrimSpace(name) == "" {
		errs = append(errs, errWorkspaceRequired)
	}
	if eff := workspaceOf(id, name); strings.TrimSpace(eff) != "" && !isGUID(eff) {
		errs = append(errs, "Workspace must be a valid GUID. Name-based resolution is not supported for this command.")
	}
	return errs
}

type listDataAccessRolesInput struct {
	WorkspaceID       string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace         string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID            string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	ContinuationToken string `json:"continuation-token,omitempty" jsonschema:"Token for retrieving the next page of results."`
}

// listDataAccessRoles ports DataAccessRoleListCommand / ListDataAccessRolesAsync.
func (a *Area) listDataAccessRoles(ctx context.Context, _ *mcp.CallToolRequest, in listDataAccessRolesInput) (*mcp.CallToolResult, any, error) {
	if errs := validateWorkspaceGUID(in.WorkspaceID, in.Workspace); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	u := a.c.ep.fabric + "/workspaces/" + ws + "/items/" + in.ItemID + "/dataAccessRoles"
	if in.ContinuationToken != "" {
		u += "?continuationToken=" + url.QueryEscape(in.ContinuationToken)
	}
	b, err := a.c.fabric(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	var resp dataAccessRoleListResponse
	if len(b) > 0 {
		if err := json.Unmarshal(b, &resp); err != nil {
			return response.Error(err), nil, nil
		}
	}
	roles := resp.Value
	if roles == nil {
		roles = []dataAccessRole{}
	}
	result := map[string]any{"roles": roles}
	if resp.ContinuationToken != nil {
		result["continuationToken"] = *resp.ContinuationToken
	}
	if resp.ContinuationUri != nil {
		result["continuationUri"] = *resp.ContinuationUri
	}
	return response.Success(result), nil, nil
}

type getDataAccessRoleInput struct {
	WorkspaceID string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace   string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID      string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	RoleName    string `json:"role-name" jsonschema:"The name of the data access role."`
}

// getDataAccessRole ports DataAccessRoleGetCommand / GetDataAccessRoleAsync.
func (a *Area) getDataAccessRole(ctx context.Context, _ *mcp.CallToolRequest, in getDataAccessRoleInput) (*mcp.CallToolResult, any, error) {
	if errs := validateWorkspaceGUID(in.WorkspaceID, in.Workspace); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	u := a.c.ep.fabric + "/workspaces/" + ws + "/items/" + in.ItemID + "/dataAccessRoles/" + url.PathEscape(in.RoleName) + "?preview=true"
	b, err := a.c.fabric(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	role := dataAccessRole{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &role); err != nil {
			return response.Error(err), nil, nil
		}
	}
	return response.Success(map[string]any{"role": role}), nil, nil
}

type createOrUpdateDataAccessRoleInput struct {
	WorkspaceID       string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace         string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID            string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	RoleName          string `json:"role-name,omitempty" jsonschema:"The name of the data access role."`
	RoleDefinition    string `json:"role-definition,omitempty" jsonschema:"JSON definition of the data access role. Must include 'name', 'members'\n(with microsoftEntraMembers), and 'decisionRules'.\nmembers.microsoftEntraMembers[].objectId accepts EITHER an Entra object ID\n(GUID) OR an email address / UPN — non-GUID values are automatically\nresolved to object IDs via Microsoft Graph (tries /users first, then\n/groups by mail, so mail-enabled groups and DLs work too). Do NOT call\nGraph yourself to convert emails to GUIDs first; pass the email or UPN\ndirectly. tenantId may be omitted — it is filled in during resolution.\nTo scope access to a specific folder, include a Path attribute in\ndecisionRules. Omitting Path grants access to the entire item.\nExample with emails (preferred when you know the address, not the GUID):\n{\"name\":\"ImagesReadOnly\",\n \"members\":{\"microsoftEntraMembers\":[\n   {\"objectId\":\"alice@contoso.com\"},\n   {\"objectId\":\"data-readers@contoso.com\"}]},\n \"decisionRules\":[{\"effect\":\"Permit\",\"permission\":[\n   {\"attributeName\":\"Action\",\"attributeValueIncludedIn\":[\"Read\"]},\n   {\"attributeName\":\"Path\",\"attributeValueIncludedIn\":[\"Files/images/*\"]}]}]}\nExample with GUIDs (use when you already have the object ID):\n{\"name\":\"ImagesReadOnly\",\n \"members\":{\"microsoftEntraMembers\":[\n   {\"objectId\":\"514402e2-4238-4672-b021-ff9000307b66\"}]},\n \"decisionRules\":[{\"effect\":\"Permit\",\"permission\":[\n   {\"attributeName\":\"Action\",\"attributeValueIncludedIn\":[\"Read\"]}]}]}"`
	EntraMembers      string `json:"entra-members,omitempty" jsonschema:"Comma-separated Entra member identifiers (object IDs, emails, or UPNs). Non-GUID values are resolved via Microsoft Graph."`
	FabricItemMembers string `json:"fabric-item-members,omitempty" jsonschema:"Comma-separated Fabric item member references in format 'itemId:permission' (e.g. 'dfbe1234-...:Read')."`
	PermittedPaths    string `json:"permitted-paths,omitempty" jsonschema:"Comma-separated paths to grant access to (e.g. 'Files/images/*,Tables/sales'). Omit to grant access to the entire item."`
	PermittedActions  string `json:"permitted-actions,omitempty" jsonschema:"Comma-separated actions to permit. Currently only 'Read' is supported. Defaults to 'Read' if omitted."`
}

// validateCreateOrUpdate ports DataAccessRoleCreateOrUpdateCommand.ValidateOptions.
func validateCreateOrUpdate(in createOrUpdateDataAccessRoleInput) []string {
	errs := validateWorkspaceGUID(in.WorkspaceID, in.Workspace)

	hasFlat := in.RoleName != "" || in.EntraMembers != "" || in.FabricItemMembers != ""
	if !hasFlat && in.RoleDefinition == "" {
		errs = append(errs, "Provide either flat options (--role-name + --entra-members/--fabric-item-members) or --role-definition.")
	}
	if hasFlat {
		if in.RoleName == "" {
			errs = append(errs, "--role-name is required when using flat options.")
		}
		if in.EntraMembers == "" && in.FabricItemMembers == "" {
			errs = append(errs, "At least one of --entra-members or --fabric-item-members is required.")
		}
		if in.EntraMembers != "" {
			for _, m := range splitTrim(in.EntraMembers) {
				if !isGUID(m) && !strings.Contains(m, "@") {
					errs = append(errs, fmt.Sprintf("Invalid --entra-members value '%s'. Must be a GUID, email, or UPN.", m))
				}
			}
		}
		if in.PermittedActions != "" {
			for _, act := range splitTrim(in.PermittedActions) {
				if !strings.EqualFold(act, "Read") {
					errs = append(errs, fmt.Sprintf("Unsupported --permitted-actions value '%s'. Only 'Read' is currently supported.", act))
				}
			}
		}
	}
	return errs
}

// splitTrim splits a comma-separated option value, trims each part and
// drops empty entries, ported from .NET's Split(',', RemoveEmptyEntries |
// TrimEntries).
func splitTrim(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// buildRoleDefinition ports BuildRoleDefinitionJson: assembles a DataAccessRole
// from the flat options, granting Read (by default) via a single decision rule.
func buildRoleDefinition(in createOrUpdateDataAccessRoleInput) dataAccessRole {
	members := dataAccessRoleMembers{}
	for _, m := range splitTrim(in.EntraMembers) {
		m := m
		members.MicrosoftEntraMembers = append(members.MicrosoftEntraMembers, microsoftEntraMember{ObjectID: &m})
	}
	for _, m := range splitTrim(in.FabricItemMembers) {
		source, access, found := strings.Cut(m, ":")
		itemAccess := []string{"Read"}
		if found {
			itemAccess = []string{access}
		}
		members.FabricItemMembers = append(members.FabricItemMembers, fabricItemMember{SourcePath: strPtr(source), ItemAccess: itemAccess})
	}

	actions := []string{"Read"}
	if in.PermittedActions != "" {
		actions = splitTrim(in.PermittedActions)
	}
	permissions := []decisionRuleScope{{AttributeName: strPtr("Action"), AttributeValueIncludedIn: actions}}
	if in.PermittedPaths != "" {
		permissions = append(permissions, decisionRuleScope{AttributeName: strPtr("Path"), AttributeValueIncludedIn: splitTrim(in.PermittedPaths)})
	}

	return dataAccessRole{
		Name:          in.RoleName,
		Members:       &members,
		DecisionRules: []decisionRule{{Effect: strPtr("Permit"), Permission: permissions}},
	}
}

// createOrUpdateDataAccessRole ports DataAccessRoleCreateOrUpdateCommand /
// CreateOrUpdateDataAccessRoleAsync.
func (a *Area) createOrUpdateDataAccessRole(ctx context.Context, _ *mcp.CallToolRequest, in createOrUpdateDataAccessRoleInput) (*mcp.CallToolResult, any, error) {
	if errs := validateCreateOrUpdate(in); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}

	var role dataAccessRole
	if in.RoleName != "" {
		role = buildRoleDefinition(in)
	} else {
		if err := json.Unmarshal([]byte(in.RoleDefinition), &role); err != nil {
			return response.Error(&argError{msg: fmt.Sprintf("Invalid role definition JSON: %s (Parameter 'roleDefinitionJson')", err.Error())}), nil, nil
		}
	}
	if strings.TrimSpace(role.Name) == "" {
		return response.Error(&argError{msg: "Role definition must include a non-empty 'name' property. (Parameter 'roleDefinitionJson')"}), nil, nil
	}

	if role.Members != nil && len(role.Members.MicrosoftEntraMembers) > 0 {
		needsTenant := false
		for _, m := range role.Members.MicrosoftEntraMembers {
			if m.TenantID == nil || strings.TrimSpace(*m.TenantID) == "" {
				needsTenant = true
				break
			}
		}
		if needsTenant {
			if tid := a.c.tenantIDFromToken(ctx); tid != "" {
				for i := range role.Members.MicrosoftEntraMembers {
					m := &role.Members.MicrosoftEntraMembers[i]
					if m.TenantID == nil {
						m.TenantID = &tid
					}
				}
			}
		}
		if err := a.resolvePrincipals(ctx, role.Members.MicrosoftEntraMembers); err != nil {
			return response.Error(err), nil, nil
		}
	}

	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	u := a.c.ep.fabric + "/workspaces/" + ws + "/items/" + in.ItemID + "/dataAccessRoles?preview=true&dataAccessRoleConflictPolicy=Overwrite"
	b, err := a.c.fabric(ctx, http.MethodPost, u, role)
	if err != nil {
		return response.Error(err), nil, nil
	}
	result := role
	if len(b) > 0 {
		if err := json.Unmarshal(b, &result); err != nil {
			return response.Error(err), nil, nil
		}
	}
	return response.Success(map[string]any{"role": result}), nil, nil
}

type deleteDataAccessRoleInput struct {
	WorkspaceID string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace   string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ItemID      string `json:"item-id" jsonschema:"The ID of the Fabric item."`
	RoleName    string `json:"role-name" jsonschema:"The name of the data access role."`
}

// deleteDataAccessRole ports DataAccessRoleDeleteCommand / DeleteDataAccessRoleAsync.
func (a *Area) deleteDataAccessRole(ctx context.Context, _ *mcp.CallToolRequest, in deleteDataAccessRoleInput) (*mcp.CallToolResult, any, error) {
	if errs := validateWorkspaceGUID(in.WorkspaceID, in.Workspace); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	u := a.c.ep.fabric + "/workspaces/" + ws + "/items/" + in.ItemID + "/dataAccessRoles/" + url.PathEscape(in.RoleName) + "?preview=true"
	if err := a.c.fabricDelete(ctx, u); err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"roleName": in.RoleName, "message": "Data access role deleted successfully."}), nil, nil
}
