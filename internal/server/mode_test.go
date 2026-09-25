package server_test

import (
	"context"
	"encoding/json"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// echoInput is the input for every fake tool registered below.
type echoInput struct {
	Value string `json:"value,omitempty" jsonschema:"A value to echo back."`
}

func echoHandler(ctx context.Context, _ *mcp.CallToolRequest, in echoInput) (*mcp.CallToolResult, any, error) {
	return response.Success(map[string]string{"value": in.Value}), nil, nil
}

// fakeArea is a minimal server.Area implementing server.Describer, standing
// in for a real tool area (docs, core, ...) registered by another agent's
// work. It registers one read-only and one non-read-only tool, so tests can
// exercise --read-only filtering at both the leaf-tool and whole-area level.
type fakeArea struct {
	name        string
	description string
	title       string
}

func (a *fakeArea) Name() string        { return a.name }
func (a *fakeArea) Description() string { return a.description }
func (a *fakeArea) Title() string       { return a.title }

func (a *fakeArea) Register(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name:        "read",
		Description: "Echoes value back; read-only.",
		Annotations: &mcp.ToolAnnotations{Title: "Read", ReadOnlyHint: true},
	}, echoHandler)
	server.AddTool(r, &mcp.Tool{
		Name:        "write",
		Description: "Echoes value back; not read-only.",
		Annotations: &mcp.ToolAnnotations{Title: "Write"},
	}, echoHandler)
}

// bareArea implements server.Area but not server.Describer, so namespace and
// single mode must fall back to its bare Name() for both description and
// title.
type bareArea struct{ name string }

func (a *bareArea) Name() string { return a.name }

func (a *bareArea) Register(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name:        "ping",
		Description: "Pings; read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, echoHandler)
}

// writeOnlyArea registers a single non-read-only tool, so tests can confirm
// --read-only drops its namespace/single mode entry entirely rather than
// exposing an empty proxy tool.
type writeOnlyArea struct{ name string }

func (a *writeOnlyArea) Name() string { return a.name }

func (a *writeOnlyArea) Register(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name:        "act",
		Description: "Not read-only.",
	}, echoHandler)
}

func areas() []server.Area {
	return []server.Area{
		&fakeArea{name: "fake", description: "Fake area description.", title: "Fake Area"},
		&bareArea{name: "bare"},
		&writeOnlyArea{name: "writeonly"},
	}
}

// connect builds a server per opts and areas, and returns a connected
// in-memory client session.
func connect(t *testing.T, opts server.Options, areaList ...server.Area) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	s, err := server.New(ctx, opts, areaList...)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "mode-test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		if err := session.Close(); err != nil {
			t.Logf("close session: %v", err)
		}
	})
	return session
}

func toolNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()
	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make([]string, len(res.Tools))
	for i, tool := range res.Tools {
		names[i] = tool.Name
	}
	sort.Strings(names)
	return names
}

func toolByName(t *testing.T, session *mcp.ClientSession, name string) *mcp.Tool {
	t.Helper()
	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	for _, tool := range res.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q not found; have %v", name, res.Tools)
	return nil
}

// callText calls tool and returns its first text content block.
func callText(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) (*mcp.CallToolResult, string) {
	t.Helper()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", tool, err)
	}
	if len(res.Content) != 1 {
		t.Fatalf("CallTool(%s): Content has %d blocks, want 1", tool, len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s): Content[0] is %T, want *mcp.TextContent", tool, res.Content[0])
	}
	return res, text.Text
}

func TestDefaultModeIsNamespace(t *testing.T) {
	// server.Options{} (Mode == "") must behave like --mode namespace,
	// upstream's ModeTypes.Default.
	session := connect(t, server.Options{}, areas()...)
	got := toolNames(t, session)
	// Without --read-only every area with at least one tool is exposed.
	want := []string{"bare", "fake", "writeonly"}
	if !slices.Equal(got, want) {
		t.Fatalf("default mode tool names = %v, want %v", got, want)
	}
}

func TestModeAll(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeAll}, areas()...)
	got := toolNames(t, session)
	want := []string{"bare_ping", "fake_read", "fake_write", "writeonly_act"}
	if !slices.Equal(got, want) {
		t.Fatalf("mode all tool names = %v, want %v", got, want)
	}
}

func TestModeConsolidated(t *testing.T) {
	// Upstream exposes zero tools for Fabric in consolidated mode
	// regardless of which areas or filters are configured.
	for _, opts := range []server.Options{
		{Mode: server.ModeConsolidated},
		{Mode: server.ModeConsolidated, Namespaces: []string{"fake"}},
		{Mode: server.ModeConsolidated, ReadOnly: true},
	} {
		session := connect(t, opts, areas()...)
		got := toolNames(t, session)
		if len(got) != 0 {
			t.Errorf("mode consolidated (%+v) tool names = %v, want none", opts, got)
		}
	}
}

func TestModeNamespaceToolShape(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeNamespace}, areas()...)

	got := toolNames(t, session)
	want := []string{"bare", "fake", "writeonly"}
	if !slices.Equal(got, want) {
		t.Fatalf("mode namespace tool names = %v, want %v", got, want)
	}

	fake := toolByName(t, session, "fake")
	if !strings.HasPrefix(fake.Description, "Fake area description.\n\n") {
		t.Errorf("fake tool description = %q, want it to start with the area description", fake.Description)
	}
	if !strings.Contains(fake.Description, `This tool is a hierarchical MCP command router.`) {
		t.Errorf("fake tool description missing router suffix: %q", fake.Description)
	}
	if fake.Annotations == nil || fake.Annotations.Title != "Fake Area" {
		t.Errorf("fake tool title = %v, want %q", fake.Annotations, "Fake Area")
	}
	if fake.Annotations.ReadOnlyHint {
		t.Errorf("fake tool ReadOnlyHint = true, want false (upstream leaves the group's own hints unset)")
	}
	if fake.Annotations.DestructiveHint != nil || fake.Annotations.OpenWorldHint != nil {
		t.Errorf("fake tool DestructiveHint/OpenWorldHint = %v/%v, want both nil",
			fake.Annotations.DestructiveHint, fake.Annotations.OpenWorldHint)
	}

	// bareArea has no Describer: description is just the router suffix (no
	// leading area description), and the title falls back to the bare name.
	bare := toolByName(t, session, "bare")
	if bare.Description != `This tool is a hierarchical MCP command router.
To invoke a command, set "command" and wrap its args in "parameters".
Set "learn=true" to discover available sub commands.` {
		t.Errorf("bare tool description = %q, want just the router suffix", bare.Description)
	}
	if bare.Annotations == nil || bare.Annotations.Title != "bare" {
		t.Errorf("bare tool title = %v, want %q", bare.Annotations, "bare")
	}

	schema, ok := fake.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("fake tool InputSchema is %T, want map[string]any", fake.InputSchema)
	}
	if schema["additionalProperties"] != false {
		t.Errorf("fake tool InputSchema additionalProperties = %v, want false", schema["additionalProperties"])
	}
	if required, _ := schema["required"].([]any); len(required) != 1 || required[0] != "intent" {
		t.Errorf("fake tool InputSchema required = %v, want [intent]", schema["required"])
	}
	props, _ := schema["properties"].(map[string]any)
	for _, name := range []string{"intent", "command", "parameters", "learn"} {
		if _, ok := props[name]; !ok {
			t.Errorf("fake tool InputSchema properties missing %q", name)
		}
	}
}

func TestModeNamespaceLearn(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeNamespace}, areas()...)

	res, text := callText(t, session, "fake", map[string]any{"intent": "x", "learn": true})
	if res.IsError {
		t.Fatalf("learn call returned an error: %s", text)
	}
	if !strings.HasPrefix(text, "Here are the available commands and their input schema for 'fake' tool.\n") {
		t.Fatalf("learn text = %q, doesn't start with the expected preamble", text)
	}
	jsonStart := strings.Index(text, "[")
	if jsonStart < 0 {
		t.Fatalf("learn text has no JSON array: %q", text)
	}
	var infos []map[string]any
	if err := json.Unmarshal([]byte(text[jsonStart:]), &infos); err != nil {
		t.Fatalf("learn text JSON tail doesn't parse: %v\n%s", err, text)
	}
	var commands []string
	for _, info := range infos {
		commands = append(commands, info["command"].(string))
		if _, ok := info["inputSchema"]; !ok {
			t.Errorf("learn entry %v missing inputSchema", info)
		}
		if info["tool"] != nil {
			t.Errorf("learn entry %v has a non-nil 'tool' field; namespace mode's per-command listing shouldn't set it", info)
		}
	}
	sort.Strings(commands)
	if want := []string{"fake_read", "fake_write"}; !slices.Equal(commands, want) {
		t.Errorf("learn commands = %v, want %v", commands, want)
	}

	// "intent" alone (no "command", no explicit "learn") implicitly learns,
	// porting NamespaceToolLoader.CallToolHandler.
	_, implicitText := callText(t, session, "fake", map[string]any{"intent": "do something"})
	if !strings.HasPrefix(implicitText, "Here are the available commands") {
		t.Errorf("intent-only call = %q, want an implicit learn response", implicitText)
	}
}

func TestModeNamespaceRouting(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeNamespace}, areas()...)

	res, text := callText(t, session, "fake", map[string]any{
		"intent": "echo", "command": "fake_read", "parameters": map[string]any{"value": "hi"},
	})
	if res.IsError {
		t.Fatalf("routed call returned an error: %s", text)
	}
	var env struct {
		Status  int            `json:"status"`
		Message string         `json:"message"`
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("routed call result isn't the inner tool's envelope: %v\n%s", err, text)
	}
	if env.Status != 200 || env.Results["value"] != "hi" {
		t.Errorf("routed call envelope = %+v, want status 200 and results.value = hi", env)
	}
}

func TestModeNamespaceUnknownCommand(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeNamespace}, areas()...)

	res, text := callText(t, session, "fake", map[string]any{"intent": "x", "command": "fake_nope"})
	if !res.IsError {
		t.Fatalf("unknown command call IsError = false, want true; text: %s", text)
	}
	want := `The command 'fake_nope' is not available for the 'fake' tool.
Available commands: fake_read, fake_write
Select the command that best matches the current intent. Use "learn=true" with an empty "intent" to get command descriptions and parameter schemas without executing a command.`
	if text != want {
		t.Errorf("unknown command text =\n%q\nwant\n%q", text, want)
	}
}

func TestModeNamespaceMissingCommand(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeNamespace}, areas()...)

	res, text := callText(t, session, "fake", map[string]any{"intent": ""})
	if res.IsError {
		t.Fatalf("missing-command call IsError = true, want false; text: %s", text)
	}
	want := `The "command" parameter is required when not learning.
Run again with the "learn" argument to get a list of available tools and their parameters.
To learn about a specific tool, use the "command" argument with the name of the tool.`
	if text != want {
		t.Errorf("missing-command text =\n%q\nwant\n%q", text, want)
	}
}

func TestModeSingleToolShape(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeSingle}, areas()...)

	got := toolNames(t, session)
	if want := []string{"fabric"}; !slices.Equal(got, want) {
		t.Fatalf("mode single tool names = %v, want %v", got, want)
	}
	tool := toolByName(t, session, "fabric")
	if !strings.HasPrefix(tool.Description, "This server/tool provides real-time, programmatic access to all Microsoft Fabric") {
		t.Errorf("fabric tool description = %q", tool.Description)
	}
	schema, ok := tool.InputSchema.(map[string]any)
	if !ok {
		t.Fatalf("fabric tool InputSchema is %T, want map[string]any", tool.InputSchema)
	}
	props, _ := schema["properties"].(map[string]any)
	for _, name := range []string{"intent", "tool", "command", "parameters", "learn"} {
		if _, ok := props[name]; !ok {
			t.Errorf("fabric tool InputSchema properties missing %q", name)
		}
	}
}

func TestModeSingleLearn(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeSingle}, areas()...)

	// Root learn (no "tool"): lists every included area by name.
	_, rootText := callText(t, session, "fabric", map[string]any{"intent": "x", "learn": true})
	jsonStart := strings.Index(rootText, "[")
	var roots []map[string]any
	if err := json.Unmarshal([]byte(rootText[jsonStart:]), &roots); err != nil {
		t.Fatalf("root learn JSON tail doesn't parse: %v\n%s", err, rootText)
	}
	var toolField []string
	for _, r := range roots {
		toolField = append(toolField, r["tool"].(string))
		if _, ok := r["command"]; ok {
			t.Errorf("root learn entry %v has a 'command' field; root listing shouldn't set it", r)
		}
	}
	sort.Strings(toolField)
	if want := []string{"bare", "fake", "writeonly"}; !slices.Equal(toolField, want) {
		t.Errorf("root learn tools = %v, want %v", toolField, want)
	}

	// Tool-level learn: lists fake's two commands.
	_, toolText := callText(t, session, "fabric", map[string]any{"intent": "x", "learn": true, "tool": "fake"})
	if !strings.HasPrefix(toolText, "Here are the available commands and their input schema for 'fake' tool.\n") {
		t.Fatalf("tool-level learn text = %q", toolText)
	}
	jsonStart = strings.Index(toolText, "[")
	var infos []map[string]any
	if err := json.Unmarshal([]byte(toolText[jsonStart:]), &infos); err != nil {
		t.Fatalf("tool-level learn JSON tail doesn't parse: %v\n%s", err, toolText)
	}
	var commands []string
	for _, info := range infos {
		commands = append(commands, info["command"].(string))
	}
	sort.Strings(commands)
	if want := []string{"fake_read", "fake_write"}; !slices.Equal(commands, want) {
		t.Errorf("tool-level learn commands = %v, want %v", commands, want)
	}
}

func TestModeSingleRoutingAndFallbacks(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeSingle}, areas()...)

	// A valid tool+command routes through to the inner tool's own result.
	res, text := callText(t, session, "fabric", map[string]any{
		"intent": "echo", "tool": "fake", "command": "fake_read", "parameters": map[string]any{"value": "hi"},
	})
	if res.IsError {
		t.Fatalf("routed call returned an error: %s", text)
	}
	if !strings.Contains(text, `"value":"hi"`) {
		t.Errorf("routed call result = %q, want it to contain the echoed value", text)
	}

	// An unknown tool name falls back to the root listing (soft, not an
	// error), porting SingleProxyToolLoader.CommandModeAsync.
	res, unknownToolText := callText(t, session, "fabric", map[string]any{
		"intent": "x", "tool": "nope", "command": "whatever",
	})
	if res.IsError {
		t.Errorf("unknown-tool call IsError = true, want false (soft fallback)")
	}
	if !strings.HasPrefix(unknownToolText, "Here are the available tools.\n") {
		t.Errorf("unknown-tool call text = %q, want the root listing", unknownToolText)
	}

	// An unknown command for a known tool falls back to that tool's
	// listing (soft, not an error).
	res, unknownCommandText := callText(t, session, "fabric", map[string]any{
		"intent": "x", "tool": "fake", "command": "nope",
	})
	if res.IsError {
		t.Errorf("unknown-command call IsError = true, want false (soft fallback)")
	}
	if !strings.HasPrefix(unknownCommandText, "Here are the available commands and their input schema for 'fake' tool.\n") {
		t.Errorf("unknown-command call text = %q, want fake's tool listing", unknownCommandText)
	}
}

func TestModeSingleMissingToolOrCommand(t *testing.T) {
	session := connect(t, server.Options{Mode: server.ModeSingle}, areas()...)

	// An empty intent (with no tool, command or learn) is the one input
	// that reaches the plain help message: any non-empty intent alone
	// implicitly learns instead (see TestModeSingleLearn).
	res, text := callText(t, session, "fabric", map[string]any{"intent": ""})
	if res.IsError {
		t.Fatalf("missing tool/command call IsError = true, want false; text: %s", text)
	}
	want := `The "tool" and "command" parameters are required when not learning
Run again with the "learn" argument to get a list of available tools and their parameters.
To learn about a specific tool, use the "tool" argument with the name of the tool.`
	if text != want {
		t.Errorf("missing tool/command text =\n%q\nwant\n%q", text, want)
	}
}

func TestReadOnlyFilter(t *testing.T) {
	// --read-only drops the non-read-only leaf tool from "fake" (leaving
	// its area exposed with one command) and drops "writeonly" entirely
	// (every one of its commands was filtered out), porting
	// NamespaceToolLoader.ListToolsHandler's AllToolsInGroupMatch check.
	session := connect(t, server.Options{Mode: server.ModeNamespace, ReadOnly: true}, areas()...)

	got := toolNames(t, session)
	want := []string{"bare", "fake"}
	if !slices.Equal(got, want) {
		t.Fatalf("read-only namespace mode tool names = %v, want %v", got, want)
	}

	_, text := callText(t, session, "fake", map[string]any{"intent": "x", "learn": true})
	if strings.Contains(text, "fake_write") {
		t.Errorf("read-only learn text still lists fake_write: %s", text)
	}
	if !strings.Contains(text, "fake_read") {
		t.Errorf("read-only learn text is missing fake_read: %s", text)
	}
}

func TestNamespaceFilter(t *testing.T) {
	for _, mode := range []string{server.ModeNamespace, server.ModeAll} {
		t.Run(mode, func(t *testing.T) {
			session := connect(t, server.Options{Mode: mode, Namespaces: []string{"fake"}}, areas()...)
			got := toolNames(t, session)
			var want []string
			if mode == server.ModeAll {
				want = []string{"fake_read", "fake_write"}
			} else {
				want = []string{"fake"}
			}
			if !slices.Equal(got, want) {
				t.Fatalf("--namespace fake tool names (%s) = %v, want %v", mode, got, want)
			}
		})
	}
}

func TestToolForcesAllMode(t *testing.T) {
	// --tool always runs in mode "all", even if --mode namespace/single/
	// consolidated was also given, porting ServerStartCommand.PostBindOptions.
	for _, mode := range []string{"", server.ModeNamespace, server.ModeSingle, server.ModeConsolidated, server.ModeAll} {
		t.Run("mode="+mode, func(t *testing.T) {
			session := connect(t, server.Options{Mode: mode, Tools: []string{"fake_read"}}, areas()...)
			got := toolNames(t, session)
			if want := []string{"fake_read"}; !slices.Equal(got, want) {
				t.Fatalf("--tool fake_read (mode %q) tool names = %v, want %v", mode, got, want)
			}
		})
	}
}

func TestInvalidMode(t *testing.T) {
	ctx := t.Context()
	_, err := server.New(ctx, server.Options{Mode: "bogus"}, areas()...)
	if err == nil {
		t.Fatal("server.New with an invalid mode returned no error")
	}
}

func TestNamespaceAndToolMutuallyExclusive(t *testing.T) {
	ctx := t.Context()
	_, err := server.New(ctx, server.Options{Namespaces: []string{"fake"}, Tools: []string{"fake_read"}}, areas()...)
	if err == nil {
		t.Fatal("server.New with both --namespace and --tool returned no error")
	}
}
