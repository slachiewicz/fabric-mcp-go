package server

import (
	"log/slog"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// consentSchema asks for an approve/reject decision, as upstream's
// HandleElicitationAsync does.
var consentSchema = &jsonschema.Schema{
	Type: "object",
	Properties: map[string]*jsonschema.Schema{
		"decision": {
			Type:        "string",
			Title:       "Decision",
			Description: "Approve or reject this sensitive operation.",
			OneOf: []*jsonschema.Schema{
				{Const: jsonschema.Ptr[any]("accept"), Title: "Approve"},
				{Const: jsonschema.Ptr[any]("reject"), Title: "Reject"},
			},
		},
	},
	Required: []string{"decision"},
}

const consentKey = "consent"

// consent ports upstream's HandleElicitationAsync for destructive tools
// (Fabric has no secret ones): ask the user through elicitation and return
// a result to send instead of running the tool, or nil to proceed.
func consent(req *mcp.CallToolRequest, t *mcp.Tool, disabled bool) *mcp.CallToolResult {
	if t.Annotations == nil || t.Annotations.DestructiveHint == nil || !*t.Annotations.DestructiveHint {
		return nil
	}
	const reason = "may perform destructive operations"
	if disabled {
		slog.Warn("tool "+reason+" but elicitation is disabled via --dangerously-disable-elicitation; proceeding without user consent", "tool", t.Name)
		return nil
	}
	if p := req.Session.InitializeParams(); p == nil || p.Capabilities == nil || p.Capabilities.Elicitation == nil {
		return textResult("This tool "+reason+" and requires user consent, but the client does not support elicitation. Operation rejected for security.", true)
	}
	// The client answers through a multi-round-trip retry (SEP-2322); for
	// clients on older protocol versions go-sdk sends elicitation/create
	// itself and re-invokes the handler with the response.
	resp, ok := req.Params.InputResponses[consentKey]
	if !ok {
		return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{consentKey: &mcp.ElicitParams{
			Message: "⚠️ DESTRUCTIVE OPERATION WARNING: The tool '" + t.Name + "' may delete or modify existing resources.\n\n" +
				"This operation could permanently alter or remove Azure resources, configurations, or data.\n\n" +
				"Do you want to continue with this potentially destructive operation?",
			RequestedSchema: consentSchema,
		}}}
	}
	res, _ := resp.(*mcp.ElicitResult)
	if res == nil {
		return textResult("Elicitation failed for tool '"+t.Name+"': unexpected response. Operation not executed for security.", true)
	}
	if decision, _ := res.Content["decision"].(string); res.Action != "accept" || decision != "accept" {
		return textResult("Operation cancelled by user.", true)
	}
	return nil
}
