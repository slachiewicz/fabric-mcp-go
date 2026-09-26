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
// Sampling-based command/tool guessing (asking the model to pick a command
// or tool from "intent" when "command"/"tool" is missing or unknown) is
// ported using go-sdk's multi-round-trip InputRequests (protocol 2026-07-28
// forbids a server from sending a server-to-client request mid-call, so a
// handler can't just block on sampling/createMessage the way upstream's C#
// does): a handler that needs an answer returns a CallToolResult whose
// InputRequests carries a *mcp.CreateMessageParams under a well-known key
// and stops; go-sdk replies to the client with an input-required result (or,
// for a pre-2026-07-28 client, issues the sampling/createMessage request
// itself transparently) and re-invokes the handler with the answer in
// req.Params.InputResponses[key], exactly the pattern consent.go uses for
// elicitation. Where upstream chains two blocking sampling calls in one
// handler (single mode's root tool-name guess followed by a command-name
// guess scoped to the resolved tool), the resolved tool name has to survive
// across that separate round trip; it's carried in req.Params.RequestState,
// an opaque string the client must echo back on retry (see routeState).
// req.Params.RequestState is also how a command resolved via sampling
// survives a *further* round trip into consent() for a destructive command:
// callNamedTool re-encodes its resolution into RequestState whenever it
// pauses, so the retry that answers consent doesn't have to re-derive (or
// re-sample) the command from the original call's arguments. RequestState
// is not signed here (see its doc comment on CallToolResult) - this server
// runs the same trusted stdio model as every other Fabric MCP tool call, so
// there's no remote client that could forge it.

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

// samplingToolKey and samplingCommandKey name the pending input request a
// paused proxy-tool call is waiting on: samplingToolKey for single mode's
// root "which tool" guess (BaseToolLoader.GetToolNameFromIntentAsync),
// samplingCommandKey for a "which command" guess scoped to one tool or
// namespace (BaseToolLoader.GetCommandAndParametersFromIntentAsync, used by
// both namespace and single mode).
const (
	samplingToolKey    = "sampling-tool"
	samplingCommandKey = "sampling-command"
)

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
// order the tools were registered in, as upstream lists a CommandGroup's
// commands; the inner server's tools/list is sorted by name.
func areaTools(inner []*mcp.Tool, areaName string, order []string) []*mcp.Tool {
	prefix := areaName + "_"
	var out []*mcp.Tool
	for _, t := range inner {
		if strings.HasPrefix(t.Name, prefix) {
			out = append(out, t)
		}
	}
	slices.SortStableFunc(out, func(a, b *mcp.Tool) int {
		return slices.Index(order, a.Name) - slices.Index(order, b.Name)
	})
	return out
}

// --- namespace mode ---

// newNamespaceServer builds mode "namespace": one proxy tool per area that
// ends up with at least one tool on the hidden inner server (an area with
// zero, e.g. every tool filtered out by --read-only, is skipped entirely -
// porting NamespaceToolLoader.ListToolsHandler's AllToolsInGroupMatch
// check), filtered by opts.Namespaces.
func newNamespaceServer(ctx context.Context, opts Options, areas []Area) (*mcp.Server, error) {
	innerOpts := opts
	innerOpts.proxied = true
	inner, order := buildInnerServer(innerOpts, areas)
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
		tools := areaTools(innerTools.Tools, a.Name(), order)
		if len(tools) == 0 {
			continue
		}
		registerNamespaceProxy(outer, opts, a, tools, sess)
	}
	return outer, nil
}

func registerNamespaceProxy(outer *mcp.Server, opts Options, a Area, tools []*mcp.Tool, sess *mcp.ClientSession) {
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
		return handleNamespaceCall(ctx, opts, sess, areaName, tools, req)
	})
}

// handleNamespaceCall ports NamespaceToolLoader.CallToolHandler.
func handleNamespaceCall(ctx context.Context, opts Options, sess *mcp.ClientSession, areaName string, tools []*mcp.Tool, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// A pending command resolved by a prior sampling round (or by a direct
	// command name) that paused for consent takes priority over the
	// call's own arguments: it's what a retry answering that consent
	// request, or a stray leftover routeState, means to resume. See the
	// package doc's note on RequestState.
	if st := decodeRouteState(req.Params.RequestState); st.Command != "" {
		return callNamedTool(ctx, req, opts, sess, tools, "", st.Command, st.Parameters, func() (*mcp.CallToolResult, error) {
			return unknownCommandResult(areaName, st.Command, tools), nil
		})
	}

	intent, command, _, learn, parameters := parseRouteArgs(req.Params.Arguments)
	if !learn && intent != "" && command == "" {
		learn = true
	}

	switch {
	case learn:
		return namespaceLearnMode(ctx, opts, sess, req, areaName, tools, intent)
	case command != "":
		return callNamedTool(ctx, req, opts, sess, tools, "", command, parameters, func() (*mcp.CallToolResult, error) {
			return sampleNamespaceCommand(ctx, opts, sess, req, areaName, tools, intent, command)
		})
	default:
		return textResult(namespaceHelpMessage, false), nil
	}
}

// sampleNamespaceCommand ports InvokeChildToolAsync's "try one supported
// sampling correction without falling back to the full learn response":
// called only once command has failed to resolve directly among tools, it
// either resolves a req.Params.InputResponses[samplingCommandKey] answer
// against tools (executing the resolved command, or falling back to the
// unknown-command result if the answer doesn't name one of them), or - on
// the first attempt - asks for that answer via sampling, or - if sampling
// isn't applicable - returns the unknown-command result directly.
func sampleNamespaceCommand(ctx context.Context, opts Options, sess *mcp.ClientSession, req *mcp.CallToolRequest, areaName string, tools []*mcp.Tool, intent, command string) (*mcp.CallToolResult, error) {
	if resp, ok := req.Params.InputResponses[samplingCommandKey]; ok {
		resolved, parameters, ok := parseSampledCommand(resp, tools)
		if !ok {
			return unknownCommandResult(areaName, command, tools), nil
		}
		return callNamedTool(ctx, req, opts, sess, tools, "", resolved, parameters, func() (*mcp.CallToolResult, error) {
			return unknownCommandResult(areaName, command, tools), nil
		})
	}
	if len(tools) == 0 || !samplingSupported(req) || intent == "" {
		return unknownCommandResult(areaName, command, tools), nil
	}
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{
		samplingCommandKey: commandSamplingParams(namespaceCommandResultSchema, req, intent, tools),
	}}, nil
}

// namespaceLearnMode ports NamespaceToolLoader.InvokeToolLearn: the plain
// learn listing, or - once a sampling answer resolves a command - that
// command's own result, exactly as upstream's InvokeChildToolAsync
// follow-up call does.
func namespaceLearnMode(ctx context.Context, opts Options, sess *mcp.ClientSession, req *mcp.CallToolRequest, areaName string, tools []*mcp.Tool, intent string) (*mcp.CallToolResult, error) {
	if resp, ok := req.Params.InputResponses[samplingCommandKey]; ok {
		resolved, parameters, ok := parseSampledCommand(resp, tools)
		if !ok {
			return namespaceLearnResult(areaName, tools), nil
		}
		return callNamedTool(ctx, req, opts, sess, tools, "", resolved, parameters, func() (*mcp.CallToolResult, error) {
			return namespaceLearnResult(areaName, tools), nil
		})
	}
	if !samplingSupported(req) || intent == "" {
		return namespaceLearnResult(areaName, tools), nil
	}
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{
		samplingCommandKey: commandSamplingParams(namespaceCommandResultSchema, req, intent, tools),
	}}, nil
}

// namespaceLearnResult ports NamespaceToolLoader.InvokeToolLearn's response
// text.
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
	innerOpts := opts
	innerOpts.proxied = true
	inner, order := buildInnerServer(innerOpts, areas)
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
		tools := areaTools(innerTools.Tools, a.Name(), order)
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
		return handleSingleCall(ctx, opts, sess, roots, byArea, req)
	})
	return outer, nil
}

// handleSingleCall ports SingleProxyToolLoader.CallToolHandler.
func handleSingleCall(ctx context.Context, opts Options, sess *mcp.ClientSession, roots []toolInfo, byArea map[string][]*mcp.Tool, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// See handleNamespaceCall's identical check: a pending command resolved
	// by a prior sampling round, or by a direct command name, that paused
	// for consent takes priority over the call's own arguments.
	if st := decodeRouteState(req.Params.RequestState); st.Command != "" {
		tools, ok := lookupArea(byArea, st.Tool)
		if !ok {
			return rootLearnResult(roots), nil
		}
		return callNamedTool(ctx, req, opts, sess, tools, st.Tool, st.Command, st.Parameters, func() (*mcp.CallToolResult, error) {
			return toolLearnResult(st.Tool, byArea, roots), nil
		})
	}
	// A pending root tool-name sampling answer (single mode's two-hop root
	// -> tool -> command chain; see resolveSampledTool) is likewise checked
	// before re-parsing the original arguments.
	if resp, ok := req.Params.InputResponses[samplingToolKey]; ok {
		return resolveSampledTool(ctx, opts, sess, req, roots, byArea, resp)
	}
	// Same for a pending command-name sampling answer reached via that
	// chain: the original arguments named no "tool" at all (that's what
	// put us on the chain in the first place), so the tool resolved in the
	// previous round - carried in RequestState by toolLearnMode's ask -
	// has to be recovered before this answer can be resolved; toolLearnMode
	// itself does that resolution once routed back to it.
	if _, ok := req.Params.InputResponses[samplingCommandKey]; ok {
		st := decodeRouteState(req.Params.RequestState)
		intent, _, _, _, _ := parseRouteArgs(req.Params.Arguments)
		return toolLearnMode(ctx, opts, sess, req, roots, byArea, st.Tool, intent)
	}

	intent, command, tool, learn, parameters := parseRouteArgs(req.Params.Arguments)
	if intent != "" && tool == "" && command == "" && !learn {
		learn = true
	}

	switch {
	case learn && tool == "":
		return rootLearnMode(req, roots, intent)
	case learn:
		return toolLearnMode(ctx, opts, sess, req, roots, byArea, tool, intent)
	case tool != "" && command != "":
		tools, ok := lookupArea(byArea, tool)
		if !ok {
			// Upstream: GetToolsInGroupAsync finds nothing -> RootLearnModeAsync.
			return rootLearnMode(req, roots, intent)
		}
		return callNamedTool(ctx, req, opts, sess, tools, tool, command, parameters, func() (*mcp.CallToolResult, error) {
			// Upstream: no resolved tool -> ToolLearnModeAsync (a soft
			// fallback, not an error - see NamedTool's caller for the
			// namespace-mode equivalent, which errors instead), itself
			// trying a sampling-based command guess.
			return toolLearnMode(ctx, opts, sess, req, roots, byArea, tool, intent)
		})
	default:
		return textResult(singleHelpMessage, false), nil
	}
}

// rootLearnMode ports SingleProxyToolLoader.RootLearnModeAsync: the plain
// root listing, or - when sampling is available - a request for a
// samplingToolKey answer naming the best-matching tool (resolved by
// resolveSampledTool on the next round).
func rootLearnMode(req *mcp.CallToolRequest, roots []toolInfo, intent string) (*mcp.CallToolResult, error) {
	if !samplingSupported(req) || intent == "" {
		return rootLearnResult(roots), nil
	}
	return &mcp.CallToolResult{InputRequests: mcp.InputRequestMap{
		samplingToolKey: toolSamplingParams(intent, roots),
	}}, nil
}

// resolveSampledTool ports the follow-up half of RootLearnModeAsync: once a
// samplingToolKey answer resolves a tool name, hand off to that tool's own
// learn mode (which may itself ask a further samplingCommandKey question -
// upstream's chained ToolLearnModeAsync call).
func resolveSampledTool(ctx context.Context, opts Options, sess *mcp.ClientSession, req *mcp.CallToolRequest, roots []toolInfo, byArea map[string][]*mcp.Tool, resp mcp.InputResponse) (*mcp.CallToolResult, error) {
	intent, _, _, _, _ := parseRouteArgs(req.Params.Arguments)
	tool, ok := parseSampledToolName(resp, roots)
	if !ok {
		return rootLearnResult(roots), nil
	}
	return toolLearnMode(ctx, opts, sess, req, roots, byArea, tool, intent)
}

// toolLearnMode ports SingleProxyToolLoader.ToolLearnModeAsync: the plain
// per-tool listing, or - once a samplingCommandKey answer resolves a
// command - that command's own result (upstream's chained CommandModeAsync
// call), or - when sampling is available and hasn't been asked yet - a
// request for that answer.
func toolLearnMode(ctx context.Context, opts Options, sess *mcp.ClientSession, req *mcp.CallToolRequest, roots []toolInfo, byArea map[string][]*mcp.Tool, tool, intent string) (*mcp.CallToolResult, error) {
	tools, ok := lookupArea(byArea, tool)
	if !ok || len(tools) == 0 {
		return rootLearnMode(req, roots, intent)
	}
	if resp, ok := req.Params.InputResponses[samplingCommandKey]; ok {
		resolved, parameters, ok := parseSampledCommand(resp, tools)
		if !ok {
			return toolLearnResult(tool, byArea, roots), nil
		}
		return callNamedTool(ctx, req, opts, sess, tools, tool, resolved, parameters, func() (*mcp.CallToolResult, error) {
			return toolLearnResult(tool, byArea, roots), nil
		})
	}
	if !samplingSupported(req) || intent == "" {
		return toolLearnResult(tool, byArea, roots), nil
	}
	return &mcp.CallToolResult{
		InputRequests: mcp.InputRequestMap{
			samplingCommandKey: commandSamplingParams(singleCommandResultSchema, req, intent, tools),
		},
		RequestState: encodeRouteState(routeState{Tool: tool}),
	}, nil
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
// tool matches. tool is the single-mode area name command belongs to
// (unused, pass "" in namespace mode, which has just one implicit area per
// proxy tool); it's threaded into a paused result's RequestState so a later
// round answering consent can resume directly on this same resolution
// rather than re-deriving (or re-sampling) it from the original call's
// arguments - see the package doc's note on RequestState.
func callNamedTool(ctx context.Context, req *mcp.CallToolRequest, opts Options, sess *mcp.ClientSession, tools []*mcp.Tool, tool, command string, parameters map[string]any, notFound func() (*mcp.CallToolResult, error)) (*mcp.CallToolResult, error) {
	var resolved *mcp.Tool
	for _, t := range tools {
		if strings.EqualFold(t.Name, command) {
			resolved = t
			break
		}
	}
	if resolved == nil {
		return notFound()
	}
	if res := consent(req, resolved, opts.DisableElicitation); res != nil {
		if res.InputRequests != nil {
			res.RequestState = encodeRouteState(routeState{Tool: tool, Command: resolved.Name, Parameters: parameters})
		}
		return res, nil
	}
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: resolved.Name, Arguments: parameters})
	if err != nil {
		return nil, fmt.Errorf("call %s: %w", resolved.Name, err)
	}
	return rewrapMissingRequired(res, resolved), nil
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

// --- missing-required-options rewrap ---

// missingRequiredMarker is the substring upstream's proxy loaders search
// for (case-insensitively) in a routed command's response message, porting
// NamespaceToolLoader/SingleProxyToolLoader's
// commandResponse.Message.Contains("Missing required options",
// OrdinalIgnoreCase) check.
const missingRequiredMarker = "missing required options"

// rewrapMissingRequired ports upstream's "Command Spec" rewrap: when a
// routed command's response envelope reports it's missing required
// options, the proxy tool re-wraps that response with the child tool's
// full input schema, so the model knows what to retry with, instead of
// just returning the bare 400 unwrapped.
func rewrapMissingRequired(res *mcp.CallToolResult, resolved *mcp.Tool) *mcp.CallToolResult {
	if !res.IsError {
		return res
	}
	message, raw, ok := envelopeMessage(res)
	if !ok || !strings.Contains(strings.ToLower(message), missingRequiredMarker) {
		return res
	}
	spec, _ := json.Marshal(toolInfo{Command: resolved.Name, Description: resolved.Description, InputSchema: resolved.InputSchema})
	text := fmt.Sprintf(`%s

- Review the following command spec and identify the required arguments from the input schema.
- Omit any arguments that are not required or do not apply to your use case.
- Wrap all command arguments into the root "parameters" argument.
- If required data is missing infer the data from your context or prompt the user as needed.
- Run the tool again with the "command" and root "parameters" object.

Command Spec:
%s`, message, spec)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}, &mcp.TextContent{Text: raw}},
		IsError: true,
	}
}

// envelopeMessage extracts the "message" field from res's first text
// content block, decoded as a response envelope (see internal/response).
func envelopeMessage(res *mcp.CallToolResult) (message, raw string, ok bool) {
	for _, c := range res.Content {
		tc, isText := c.(*mcp.TextContent)
		if !isText || tc.Text == "" {
			continue
		}
		var env struct {
			Message string `json:"message"`
		}
		if json.Unmarshal([]byte(tc.Text), &env) != nil {
			continue
		}
		return env.Message, tc.Text, true
	}
	return "", "", false
}

// --- sampling-based command/tool guessing ---
//
// Every mcp.CreateMessage*/SamplingMessage/ClientCapabilities.Sampling
// reference below is go-sdk's sampling API, deprecated (SEP-2577) but still
// functional and, as of v1.8.0, still the only typed way to build this
// InputRequest - matching upstream's own "sampling-based" feature name,
// itself unchanged by the deprecation. Each use is annotated rather than
// silenced file-wide, so a genuinely new deprecated-API use elsewhere in
// this file still gets caught.

// samplingSupported reports whether req's client declared the sampling
// capability, porting BaseToolLoader.SupportsSampling.
func samplingSupported(req *mcp.CallToolRequest) bool {
	p := req.Session.InitializeParams()
	return p != nil && p.Capabilities != nil && p.Capabilities.Sampling != nil //nolint:staticcheck // SA1019: see the section doc comment above
}

// routeState is the small piece of state a paused proxy-tool call carries
// forward in a CallToolResult/CallToolParams' RequestState, an opaque
// string the client must echo back unchanged on the next round: either a
// resolved tool name alone (single mode's root -> tool hop, see
// toolLearnMode), or a fully resolved command and its parameters pending a
// further round trip (a consent request - see callNamedTool).
type routeState struct {
	Tool       string         `json:"tool,omitempty"`
	Command    string         `json:"command,omitempty"`
	Parameters map[string]any `json:"parameters,omitempty"`
}

func encodeRouteState(st routeState) string {
	b, err := json.Marshal(st)
	if err != nil {
		return ""
	}
	return string(b)
}

// decodeRouteState decodes s, returning the zero routeState for an empty or
// malformed string (a fresh call, or one that never paused).
func decodeRouteState(s string) routeState {
	var st routeState
	if s != "" {
		_ = json.Unmarshal([]byte(s), &st)
	}
	return st
}

// namespaceCommandResultSchema ports
// NamespaceToolLoader.ToolCallProxyInputSchema, the result schema namespace
// mode's sampling prompt asks the model to answer in.
const namespaceCommandResultSchema = `{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "The name of the command to call."
    },
    "parameters": {
      "type": "object",
      "description": "The parameters to pass to the tool command."
    }
  },
  "additionalProperties": false
}`

// singleCommandResultSchema ports SingleProxyToolLoader.ToolCallProxySchema.
const singleCommandResultSchema = `{
  "type": "object",
  "properties": {
    "command": {
      "type": "string",
      "description": "The name of the tool to call."
    },
    "parameters": {
      "type": "object",
      "description": "A key/value pair of parameters names and values to pass to the tool call command."
    }
  },
  "additionalProperties": false
}`

// commandSamplingParams ports GetCommandAndParametersFromIntentAsync's
// sampling/createMessage prompt (namespace and single mode share the same
// wording; only resultSchema differs between them).
func commandSamplingParams(resultSchema string, req *mcp.CallToolRequest, intent string, tools []*mcp.Tool) *mcp.CreateMessageParams { //nolint:staticcheck // SA1019: see the section doc comment above
	known := knownParametersJSON(req)
	available, _ := json.Marshal(toolInfos(tools))
	text := fmt.Sprintf(`Your task:
- Select the single command that best matches the user's intent.
- Return a valid JSON object that matches the provided result schema.
- Map the user's intent and known parameters to the command's input schema, ensuring parameter names and types match the schema exactly (no extra or missing parameters).
- Only include parameters that are defined in the selected command's input schema.
- Do not guess or invent parameters.
- If no command matches, return JSON schema with "Unknown" command name.

Result Schema:
%s

Intent:
%s

Known Parameters:
%s

Available Commands:
%s`, resultSchema, intent, known, available)
	return &mcp.CreateMessageParams{ //nolint:staticcheck // SA1019: see the section doc comment above
		MaxTokens: 1000,
		Messages:  []*mcp.SamplingMessage{{Role: "assistant", Content: &mcp.TextContent{Text: text}}}, //nolint:staticcheck // SA1019: see the section doc comment above
	}
}

// toolSamplingParams ports GetToolNameFromIntentAsync's
// sampling/createMessage prompt (single mode's root tool-name guess).
func toolSamplingParams(intent string, roots []toolInfo) *mcp.CreateMessageParams { //nolint:staticcheck // SA1019: see the section doc comment above
	available, _ := json.Marshal(roots)
	text := fmt.Sprintf(`Your task:
- Select a single tool that best matches the user's intent and return the name of the tool.
- Only return tool names that are defined in the provided list.
- If no tool matches, return "Unknown".

Intent:
%s

Available Tools:
%s`, intent, available)
	return &mcp.CreateMessageParams{ //nolint:staticcheck // SA1019: see the section doc comment above
		MaxTokens: 1000,
		Messages:  []*mcp.SamplingMessage{{Role: "assistant", Content: &mcp.TextContent{Text: text}}}, //nolint:staticcheck // SA1019: see the section doc comment above
	}
}

// knownParametersJSON ports GetParametersJsonElement(request).GetRawText():
// the "parameters" object from the original call's arguments, or "{}".
func knownParametersJSON(req *mcp.CallToolRequest) string {
	_, _, _, _, parameters := parseRouteArgs(req.Params.Arguments)
	if parameters == nil {
		return "{}"
	}
	b, err := json.Marshal(parameters)
	if err != nil {
		return "{}"
	}
	return string(b)
}

// sampledText extracts the trimmed text of resp's first content block, or
// "", false if resp doesn't carry text. A CreateMessageParams input request
// is always answered as a *mcp.CreateMessageWithToolsResult: go-sdk
// upgrades a plain *mcp.CreateMessageResult from a CreateMessageHandler
// client (fulfillInputRequest/fulfillServerInputRequest both go through
// createMessageParamsToWithTools), so that's the shape to expect; the plain
// result type is handled too, defensively.
func sampledText(resp mcp.InputResponse) (string, bool) {
	switch r := resp.(type) {
	case *mcp.CreateMessageWithToolsResult: //nolint:staticcheck // SA1019: see the section doc comment above
		if r == nil || len(r.Content) == 0 {
			return "", false
		}
		tc, ok := r.Content[0].(*mcp.TextContent)
		if !ok {
			return "", false
		}
		return strings.TrimSpace(tc.Text), true
	case *mcp.CreateMessageResult: //nolint:staticcheck // SA1019: see the section doc comment above
		if r == nil {
			return "", false
		}
		tc, ok := r.Content.(*mcp.TextContent)
		if !ok {
			return "", false
		}
		return strings.TrimSpace(tc.Text), true
	default:
		return "", false
	}
}

// parseSampledToolName ports ResolveSampledCommandName as used by
// GetToolNameFromIntentAsync's caller: the plain tool name text the model
// answered, resolved case-insensitively against roots, or "", false if
// it's empty, "Unknown", or doesn't match one of them.
func parseSampledToolName(resp mcp.InputResponse, roots []toolInfo) (string, bool) {
	name, ok := sampledText(resp)
	if !ok || name == "" || name == "Unknown" {
		return "", false
	}
	for _, r := range roots {
		if strings.EqualFold(r.Tool, name) {
			return r.Tool, true
		}
	}
	return "", false
}

// parseSampledCommand ports ResolveSampledCommandName as used by
// GetCommandAndParametersFromIntentAsync's callers: the {"command",
// "parameters"} JSON object the model answered, with command resolved
// case-insensitively against tools, or "", nil, false if the answer isn't
// that shape, names no command, names "Unknown", or doesn't match one of
// them.
func parseSampledCommand(resp mcp.InputResponse, tools []*mcp.Tool) (string, map[string]any, bool) {
	text, ok := sampledText(resp)
	if !ok {
		return "", nil, false
	}
	var answer struct {
		Command    string         `json:"command"`
		Parameters map[string]any `json:"parameters"`
	}
	_ = json.Unmarshal([]byte(text), &answer) // Malformed JSON leaves Command "", resolved below.
	if strings.TrimSpace(answer.Command) == "" || answer.Command == "Unknown" {
		return "", nil, false
	}
	for _, t := range tools {
		if strings.EqualFold(t.Name, answer.Command) {
			return t.Name, answer.Parameters, true
		}
	}
	return "", nil, false
}
