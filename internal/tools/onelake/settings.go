package onelake

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// The types below port upstream's Models/SettingsModels.cs. As in
// security_models.go, a field is omitempty only where upstream marks it
// JsonIgnore(WhenWritingNull).

// oneLakeSettings ports OneLakeSettings, the GET /onelake/settings response.
type oneLakeSettings struct {
	Diagnostics          *oneLakeDiagnosticSettings `json:"diagnostics"`
	ImmutabilityPolicies []immutabilityPolicy       `json:"immutabilityPolicies"`
	Lifecycle            *lifecycleSettings         `json:"lifecycle"`
}

// oneLakeDiagnosticSettings ports OneLakeDiagnosticSettings: the body of
// POST .../modifyDiagnostics and the "diagnostics" block in GET.
type oneLakeDiagnosticSettings struct {
	Status      *string                         `json:"status"`
	Destination *lakehouseDiagnosticDestination `json:"destination,omitempty"`
}

// lakehouseDiagnosticDestination ports LakehouseDiagnosticDestination.
type lakehouseDiagnosticDestination struct {
	Type      string             `json:"type"`
	Lakehouse *itemReferenceByID `json:"lakehouse"`
}

// itemReferenceByID ports ItemReferenceById.
type itemReferenceByID struct {
	ReferenceType string  `json:"referenceType"`
	ItemID        *string `json:"itemId"`
	WorkspaceID   *string `json:"workspaceId"`
}

// immutabilityPolicy ports ImmutabilityPolicy: the body of POST
// .../modifyImmutabilityPolicy and items in the GET response.
type immutabilityPolicy struct {
	Scope         *string `json:"scope"`
	RetentionDays *int    `json:"retentionDays"`
}

// lifecycleSettings ports LifecycleSettings.
type lifecycleSettings struct {
	DefaultTier *string `json:"defaultTier"`
	Policy      *string `json:"policy"`
}

func (a *Area) registerSettings(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "get-settings",
		Description: "Get the OneLake settings for a workspace — diagnostics configuration and\n" +
			"immutability policy. Requires OneLake.Read.All.",
		Annotations: readOnly("Get OneLake Settings"),
	}, a.getSettings)

	server.AddTool(r, &mcp.Tool{
		Name: "modify-diagnostics",
		Description: "Enable or disable workspace-level OneLake diagnostic logging. When enabling,\n" +
			"specify the destination lakehouse where logs will be stored. When disabling,\n" +
			"destination options must be omitted. This is an LRO — the server may return\n" +
			"202 Accepted. Requires OneLake.ReadWrite.All. Caller must be a workspace Admin\n" +
			"on the source workspace and Contributor+ on the destination workspace.",
		Annotations: write("Modify OneLake Diagnostics", false, true),
	}, a.modifyDiagnostics)

	server.AddTool(r, &mcp.Tool{
		Name: "modify-immutability-policy",
		Description: "Modify the workspace-level OneLake immutability policy. Once enabled,\n" +
			"immutability cannot be disabled — confirm with the user before applying.\n" +
			"Retention days cannot be reduced below the current value. Requires\n" +
			"OneLake.ReadWrite.All. Caller must be a workspace Admin.",
		Annotations: write("Modify OneLake Immutability Policy", false, true),
	}, a.modifyImmutabilityPolicy)
}

type getSettingsInput struct {
	WorkspaceID string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace   string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
}

// getSettings ports SettingsGetCommand / GetSettingsAsync.
func (a *Area) getSettings(ctx context.Context, _ *mcp.CallToolRequest, in getSettingsInput) (*mcp.CallToolResult, any, error) {
	if errs := validateWorkspaceGUID(in.WorkspaceID, in.Workspace); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	u := a.c.ep.fabric + "/workspaces/" + ws + "/onelake/settings"
	b, err := a.c.fabric(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	settings := oneLakeSettings{}
	if len(b) > 0 {
		if err := json.Unmarshal(b, &settings); err != nil {
			return response.Error(err), nil, nil
		}
	}
	return response.Success(map[string]any{"settings": settings}), nil, nil
}

type modifyDiagnosticsInput struct {
	WorkspaceID                     string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace                       string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	Status                          string `json:"status" jsonschema:"The status of diagnostics: Enabled or Disabled."`
	DestinationLakehouseWorkspaceID string `json:"destination-lakehouse-workspace-id,omitempty" jsonschema:"The workspace ID (GUID) of the destination lakehouse for diagnostic logs. Required when --status is Enabled."`
	DestinationLakehouseItemID      string `json:"destination-lakehouse-item-id,omitempty" jsonschema:"The item ID (GUID) of the destination lakehouse for diagnostic logs. Required when --status is Enabled."`
}

// validateModifyDiagnostics ports DiagnosticsModifyCommand.ValidateOptions.
func validateModifyDiagnostics(in modifyDiagnosticsInput) []string {
	errs := validateWorkspaceGUID(in.WorkspaceID, in.Workspace)

	enabled := strings.EqualFold(in.Status, "Enabled")
	disabled := strings.EqualFold(in.Status, "Disabled")
	if !enabled && !disabled {
		errs = append(errs, "--status must be 'Enabled' or 'Disabled'.")
	}
	if enabled {
		if in.DestinationLakehouseWorkspaceID == "" {
			errs = append(errs, "--destination-lakehouse-workspace-id is required when --status is Enabled.")
		} else if !isGUID(in.DestinationLakehouseWorkspaceID) {
			errs = append(errs, "--destination-lakehouse-workspace-id must be a valid GUID.")
		}
		if in.DestinationLakehouseItemID == "" {
			errs = append(errs, "--destination-lakehouse-item-id is required when --status is Enabled.")
		} else if !isGUID(in.DestinationLakehouseItemID) {
			errs = append(errs, "--destination-lakehouse-item-id must be a valid GUID.")
		}
	} else if disabled {
		if in.DestinationLakehouseWorkspaceID != "" || in.DestinationLakehouseItemID != "" {
			errs = append(errs, "Destination options must be omitted when --status is Disabled.")
		}
	}
	return errs
}

// modifyDiagnostics ports DiagnosticsModifyCommand / ModifyDiagnosticsAsync.
// The underlying request is an LRO (client.fabric polls a 202's Location).
func (a *Area) modifyDiagnostics(ctx context.Context, _ *mcp.CallToolRequest, in modifyDiagnosticsInput) (*mcp.CallToolResult, any, error) {
	if errs := validateModifyDiagnostics(in); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	settings := oneLakeDiagnosticSettings{Status: &in.Status}
	if strings.EqualFold(in.Status, "Enabled") {
		settings.Destination = &lakehouseDiagnosticDestination{
			Type: "Lakehouse",
			Lakehouse: &itemReferenceByID{
				ReferenceType: "ById",
				ItemID:        &in.DestinationLakehouseItemID,
				WorkspaceID:   &in.DestinationLakehouseWorkspaceID,
			},
		}
	}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	u := a.c.ep.fabric + "/workspaces/" + ws + "/onelake/settings/modifyDiagnostics"
	if _, err := a.c.fabric(ctx, http.MethodPost, u, settings); err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"message": "Diagnostics settings modified successfully."}), nil, nil
}

type modifyImmutabilityPolicyInput struct {
	WorkspaceID   string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace     string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	Scope         string `json:"scope" jsonschema:"The scope of the immutability policy. Currently only 'DiagnosticLogs' is supported."`
	RetentionDays int    `json:"retention-days" jsonschema:"Number of days to retain diagnostic logs (minimum 1). Cannot be reduced below the current value."`
}

// validateModifyImmutabilityPolicy ports ImmutabilityPolicyModifyCommand.ValidateOptions.
func validateModifyImmutabilityPolicy(in modifyImmutabilityPolicyInput) []string {
	errs := validateWorkspaceGUID(in.WorkspaceID, in.Workspace)
	if !strings.EqualFold(in.Scope, "DiagnosticLogs") {
		errs = append(errs, "--scope must be 'DiagnosticLogs'. No other scopes are currently supported.")
	}
	if in.RetentionDays < 1 {
		errs = append(errs, "--retention-days must be at least 1.")
	}
	return errs
}

// modifyImmutabilityPolicy ports ImmutabilityPolicyModifyCommand /
// ModifyImmutabilityPolicyAsync. This call is never made against the live
// tenant (an immutability policy cannot be shortened or removed once set);
// unit tests cover it with a fake, and live parity is limited to its error
// paths (e.g. missing/invalid arguments), which fail before any request.
func (a *Area) modifyImmutabilityPolicy(ctx context.Context, _ *mcp.CallToolRequest, in modifyImmutabilityPolicyInput) (*mcp.CallToolResult, any, error) {
	if errs := validateModifyImmutabilityPolicy(in); len(errs) > 0 {
		return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n")), nil, nil
	}
	policy := immutabilityPolicy{Scope: &in.Scope, RetentionDays: &in.RetentionDays}
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	u := a.c.ep.fabric + "/workspaces/" + ws + "/onelake/settings/modifyImmutabilityPolicy"
	if _, err := a.c.fabric(ctx, http.MethodPost, u, policy); err != nil {
		return response.Error(err), nil, nil
	}
	return response.Success(map[string]any{"message": "Immutability policy modified successfully."}), nil, nil
}
