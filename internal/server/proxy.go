package server

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// This file implements namespace and single mode by registering every tool
// on a hidden inner server exactly as mode "all" does, connecting to it with
// an in-memory client session (mcp.NewInMemoryTransports), and implementing
// the proxy tools on the outer server by calling ListTools/CallTool on that
// session. This ports the behavior of upstream's NamespaceToolLoader and
// SingleProxyToolLoader without duplicating their tool-registration or
// argument-parsing plumbing.
//
// Sampling-based command correction (asking the model to guess a command
// from "intent" alone) and elicitation for destructive/secret commands are
// deliberately not ported: no area registered by this server currently
// declares a destructive or secret tool, or is exercised by "learn"-only
// intent guessing, so there is nothing to gate against, and the go-sdk's
// sampling/elicitation client plumbing would need to be threaded through the
// in-memory session for no observable behavior change today. See the
// package doc for the follow-up if a future area needs either.

// routerSuffix is appended to every namespace-mode proxy tool's description,
// porting NamespaceToolLoader.ListToolsHandler's tool.Description suffix.
const routerSuffix = `This tool is a hierarchical MCP command router.
To invoke a command, set "command" and wrap its args in "parameters".
Set "learn=true" to discover available sub commands.`

// namespaceInputSchema ports NamespaceToolLoader.s_toolSchema.
var namespaceInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "intent": {
      "type": "string",
      "description": "The intent of the operation to perform."
    },
    "command": {
      "type": "string",
      "description": "The command to execute against the specified tool."
    },
    "parameters": {
      "type": "object",
      "description": "The parameters to pass to the tool command."
    },
    "learn": {
      "type": "boolean",
      "description": "To learn about the tool and its supported child tools and parameters.",
      "default": false
    }
  },
  "required": ["intent"],
  "additionalProperties": false
}`)

// namespaceHelpMessage ports NamespaceToolLoader.CallToolHandler's helpMessage.
const namespaceHelpMessage = `The "command" parameter is required when not learning.
Run again with the "learn" argument to get a list of available tools and their parameters.
To learn about a specific tool, use the "command" argument with the name of the tool.`

// toolInfo ports Microsoft.Mcp.Core...Models.ToolCommandInfo: a compact
// summary of one child tool used in "learn" output. Root/tool-level listings
// (single mode's RootLearnModeAsync) set Tool; command-level listings
// (namespace mode, and single mode's ToolLearnModeAsync) set Command and
// InputSchema.
type toolInfo struct {
	Tool        string `json:"tool,omitempty"`
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema,omitempty"`
}

// connectInMemory connects server to a freshly created in-memory client
// session, the pattern used throughout the go-sdk's own tests.
func connectInMemory(ctx context.Context, server *mcp.Server) (*mcp.ClientSession, error) {
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, t1, nil); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "fabric-mcp-go-inner", Version: Version}, nil)
	return client.Connect(ctx, t2, nil)
}

// textResult builds a plain-text CallToolResult, the shape every proxy
// response in this file uses (StructuredContent is never set, matching
// upstream with structured output mode disabled, the default).
func textResult(text string, isError bool) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}, IsError: isError}
}

// parseRouteArgs extracts the router meta-fields from a proxy tool call's
// raw arguments the way upstream does: each field is read only if present
// and of the expected JSON type, everything else is ignored rather than
// rejected. This intentionally bypasses the generic mcp.AddTool's schema
// validation (which would hard-reject a missing "intent" or an unrecognized
// key), since upstream's own handlers never enforce their declared schema
// that strictly - see BaseToolLoader.GetParametersDictionary and each
// loader's CallToolHandler.
func parseRouteArgs(raw json.RawMessage) (intent, command, tool string, learn bool, parameters map[string]any) {
	var m map[string]any
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	intent, _ = m["intent"].(string)
	command, _ = m["command"].(string)
	tool, _ = m["tool"].(string)
	learn, _ = m["learn"].(bool)
	parameters, _ = m["parameters"].(map[string]any)
	return intent, command, tool, learn, parameters
}

// areaTools returns the inner tools registered for area, in the inner
// server's tools/list order (alphabetical by name; the go-sdk's tool
// registry doesn't preserve registration order - see internal/parity's
// TestParityNamespaceLearn for why this is compared as a set against
// upstream's registration-order listing).
func areaTools(inner []*mcp.Tool, areaName string) []*mcp.Tool {
	prefix := areaName + "_"
	var out []*mcp.Tool
	for _, t := range inner {
		if strings.HasPrefix(t.Name, prefix) {
			out = append(out, t)
		}
	}
	return out
}

// --- namespace mode ---

// newNamespaceServer builds mode "namespace": one proxy tool per area that
// ends up with at least one tool on the hidden inner server (an area with
// zero, e.g. every tool filtered out by --read-only, is skipped entirely -
// porting NamespaceToolLoader.ListToolsHandler's AllToolsInGroupMatch
// check), filtered by opts.Namespaces.
func newNamespaceServer(ctx context.Context, opts Options, areas []Area) (*mcp.Server, error) {
	inner := buildInnerServer(opts, areas)
	sess, err := connectInMemory(ctx, inner)
	if err != nil {
		return nil, fmt.Errorf("connect inner server: %w", err)
	}
	innerTools, err := sess.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list inner tools: %w", err)
	}

	outer := mcp.NewServer(implementation(), nil)
	for _, a := range areas {
		if len(opts.Namespaces) > 0 && !slices.Contains(opts.Namespaces, a.Name()) {
			continue
		}
		tools := areaTools(innerTools.Tools, a.Name())
		if len(tools) == 0 {
			continue
		}
		registerNamespaceProxy(outer, a, tools, sess)
	}
	return outer, nil
}

func registerNamespaceProxy(outer *mcp.Server, a Area, tools []*mcp.Tool, sess *mcp.ClientSession) {
	description, title := describe(a)
	fullDescription := routerSuffix
	if description != "" {
		fullDescription = description + "\n\n" + routerSuffix
	}
	areaName := a.Name()
	tool := &mcp.Tool{
		Name:        areaName,
		Description: fullDescription,
		InputSchema: namespaceInputSchema,
		Annotations: &mcp.ToolAnnotations{Title: title},
	}
	outer.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleNamespaceCall(ctx, sess, areaName, tools, req)
	})
}

// handleNamespaceCall ports NamespaceToolLoader.CallToolHandler, minus the
// sampling-based command correction (see the package doc at the top of this
// file).
func handleNamespaceCall(ctx context.Context, sess *mcp.ClientSession, areaName string, tools []*mcp.Tool, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	intent, command, _, learn, parameters := parseRouteArgs(req.Params.Arguments)
	if !learn && intent != "" && command == "" {
		learn = true
	}

	switch {
	case learn:
		return namespaceLearnResult(areaName, tools), nil
	case command != "":
		return callNamedTool(ctx, sess, tools, command, parameters, func() *mcp.CallToolResult {
			return unknownCommandResult(areaName, command, tools)
		})
	default:
		return textResult(namespaceHelpMessage, false), nil
	}
}

// namespaceLearnResult ports NamespaceToolLoader.InvokeToolLearn's response
// text (minus the sampling-based follow-up call).
func namespaceLearnResult(areaName string, tools []*mcp.Tool) *mcp.CallToolResult {
	b, _ := json.Marshal(toolInfos(tools))
	text := fmt.Sprintf(
		"Here are the available commands and their input schema for '%s' tool.\n"+
			"If you do not find a suitable \"command\", run again with the \"learn=true\" to get a list of available commands and their parameters.\n"+
			"Next, identify the command you want to execute and run again with the \"command\" and \"parameters\" arguments, respecting \"required\" parameters if present.\n\n%s",
		areaName, b)
	return textResult(text, false)
}

// --- single mode ---

const singleToolName = "fabric"

// singleToolDescription ports the Fabric.Mcp.Server appsettings.json
// "Description" SingleProxyToolLoader reads via ServerRuntimeConfiguration.
const singleToolDescription = `This server/tool provides real-time, programmatic access to all Microsoft Fabric workspaces, items, and data platform capabilities. Use this tool for any Microsoft Fabric operation, including workspace, item, and data platform management and automation. To discover available capabilities, call the tool with the "learn" parameter to get a list of top-level tools. To explore further, set "learn" and specify a tool name to retrieve supported commands and their parameters. To execute an action, set the "tool", "command", and convert the user's intent into the "parameters" based on the discovered schema. Always use this tool for any Microsoft Fabric related operation requiring up-to-date, dynamic, and interactive capabilities. Always include the "intent" parameter to specify the operation you want to perform.`

// singleInputSchema ports SingleProxyToolLoader.BuildToolSchema("fabric").
var singleInputSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "intent": {
      "type": "string",
      "description": "The intent of the fabric operation to perform."
    },
    "tool": {
      "type": "string",
      "description": "The fabric tool to use to execute the operation."
    },
    "command": {
      "type": "string",
      "description": "The command to execute against the specified tool."
    },
    "parameters": {
      "type": "object",
      "description": "The parameters to pass to the tool command."
    },
    "learn": {
      "type": "boolean",
      "description": "To learn about the tool and its supported child tools and parameters.",
      "default": false
    }
  },
  "required": ["intent"],
  "additionalProperties": false
}`)

// singleHelpMessage ports SingleProxyToolLoader.CallToolHandler's helpMessage.
const singleHelpMessage = `The "tool" and "command" parameters are required when not learning
Run again with the "learn" argument to get a list of available tools and their parameters.
To learn about a specific tool, use the "tool" argument with the name of the tool.`

// newSingleServer builds mode "single": one "fabric" tool routing across
// every area that ends up with at least one tool on the hidden inner
// server, filtered by opts.Namespaces (porting SingleProxyToolLoader's
// IsNamespaceAllowed and its own AllToolsInGroupMatch check).
func newSingleServer(ctx context.Context, opts Options, areas []Area) (*mcp.Server, error) {
	inner := buildInnerServer(opts, areas)
	sess, err := connectInMemory(ctx, inner)
	if err != nil {
		return nil, fmt.Errorf("connect inner server: %w", err)
	}
	innerTools, err := sess.ListTools(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("list inner tools: %w", err)
	}

	var roots []toolInfo
	byArea := map[string][]*mcp.Tool{}
	for _, a := range areas {
		if len(opts.Namespaces) > 0 && !slices.Contains(opts.Namespaces, a.Name()) {
			continue
		}
		tools := areaTools(innerTools.Tools, a.Name())
		if len(tools) == 0 {
			continue
		}
		description, _ := describe(a)
		roots = append(roots, toolInfo{Tool: a.Name(), Description: description})
		byArea[a.Name()] = tools
	}

	outer := mcp.NewServer(implementation(), nil)
	tool := &mcp.Tool{
		Name:        singleToolName,
		Description: singleToolDescription,
		InputSchema: singleInputSchema,
		Annotations: &mcp.ToolAnnotations{},
	}
	outer.AddTool(tool, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return handleSingleCall(ctx, sess, roots, byArea, req)
	})
	return outer, nil
}

// handleSingleCall ports SingleProxyToolLoader.CallToolHandler, minus the
// sampling-based tool/command guessing (see the package doc at the top of
// this file).
func handleSingleCall(ctx context.Context, sess *mcp.ClientSession, roots []toolInfo, byArea map[string][]*mcp.Tool, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	intent, command, tool, learn, parameters := parseRouteArgs(req.Params.Arguments)
	if intent != "" && tool == "" && command == "" && !learn {
		learn = true
	}

	switch {
	case learn && tool == "":
		return rootLearnResult(roots), nil
	case learn:
		return toolLearnResult(tool, byArea, roots), nil
	case tool != "" && command != "":
		tools, ok := lookupArea(byArea, tool)
		if !ok {
			// Upstream: GetToolsInGroupAsync finds nothing -> RootLearnModeAsync.
			return rootLearnResult(roots), nil
		}
		return callNamedTool(ctx, sess, tools, command, parameters, func() *mcp.CallToolResult {
			// Upstream: no resolved tool -> ToolLearnModeAsync (a soft
			// fallback, not an error - see NamedTool's caller for the
			// namespace-mode equivalent, which errors instead).
			return toolLearnResult(tool, byArea, roots)
		})
	default:
		return textResult(singleHelpMessage, false), nil
	}
}

// rootLearnResult ports SingleProxyToolLoader.RootLearnModeAsync's response
// text (minus the sampling-based follow-up call).
func rootLearnResult(roots []toolInfo) *mcp.CallToolResult {
	if roots == nil {
		roots = []toolInfo{}
	}
	b, _ := json.Marshal(roots)
	text := fmt.Sprintf(
		"Here are the available tools.\n"+
			"Next, identify the tool you want to learn about and run again with the \"learn\" argument and the \"tool\" name to get a list of available commands and their parameters.\n\n%s",
		b)
	return textResult(text, false)
}

// toolLearnResult ports SingleProxyToolLoader.ToolLearnModeAsync's response
// text (minus the sampling-based follow-up call), falling back to the root
// listing for an unknown tool name exactly as upstream does.
func toolLearnResult(tool string, byArea map[string][]*mcp.Tool, roots []toolInfo) *mcp.CallToolResult {
	tools, ok := lookupArea(byArea, tool)
	if !ok {
		return rootLearnResult(roots)
	}
	b, _ := json.Marshal(toolInfos(tools))
	text := fmt.Sprintf(
		"Here are the available commands and their input schema for '%s' tool.\n"+
			"If you do not find a suitable command, run again with the \"learn\" argument and empty \"command\" to get a list of available commands and their input schema.\n"+
			"Next, identify the command you want to execute and run again with the \"tool\", \"command\", and \"parameters\" arguments.\n\n%s",
		tool, b)
	return textResult(text, false)
}

// --- shared helpers ---

// callNamedTool resolves command against tools (case-insensitively, as
// upstream does) and calls it on sess, or returns notFound()'s result if no
// tool matches.
func callNamedTool(ctx context.Context, sess *mcp.ClientSession, tools []*mcp.Tool, command string, parameters map[string]any, notFound func() *mcp.CallToolResult) (*mcp.CallToolResult, error) {
	var resolved *mcp.Tool
	for _, t := range tools {
		if strings.EqualFold(t.Name, command) {
			resolved = t
			break
		}
	}
	if resolved == nil {
		return notFound(), nil
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: resolved.Name, Arguments: parameters})
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", resolved.Name, err)
	}
	return res, nil
}

// unknownCommandResult ports BaseToolLoader.CreateUnknownCommandResult.
func unknownCommandResult(toolName, command string, tools []*mcp.Tool) *mcp.CallToolResult {
	names := make([]string, len(tools))
	for i, t := range tools {
		names[i] = t.Name
	}
	sort.Strings(names)

	available := "No commands are available for this tool in the current server configuration."
	selection := ""
	if len(names) > 0 {
		available = "Available commands: " + strings.Join(names, ", ")
		selection = "Select the command that best matches the current intent. "
	}
	text := fmt.Sprintf(
		"The command '%s' is not available for the '%s' tool.\n%s\n%sUse \"learn=true\" with an empty \"intent\" to get command descriptions and parameter schemas without executing a command.",
		command, toolName, available, selection)
	return textResult(text, true)
}

// toolInfos maps tools to their toolInfo learn-listing form (Command +
// Description + InputSchema), porting ToolCommandInfo(tool, includeSchema: true).
func toolInfos(tools []*mcp.Tool) []toolInfo {
	out := make([]toolInfo, len(tools))
	for i, t := range tools {
		out[i] = toolInfo{Command: t.Name, Description: t.Description, InputSchema: t.InputSchema}
	}
	return out
}

// lookupArea finds byArea's entry for name case-insensitively, matching
// upstream's OrdinalIgnoreCase group-name comparisons.
func lookupArea(byArea map[string][]*mcp.Tool, name string) ([]*mcp.Tool, bool) {
	if tools, ok := byArea[name]; ok {
		return tools, true
	}
	for k, tools := range byArea {
		if strings.EqualFold(k, name) {
			return tools, true
		}
	}
	return nil, false
}
