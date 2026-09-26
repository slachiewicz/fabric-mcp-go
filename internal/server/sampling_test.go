package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// connectAreas is connect (mode_test.go) generalized to a caller-supplied
// client, so sampling and consent tests can exercise a client that declares
// capabilities a bare mcp.NewClient client doesn't.
func connectAreas(t *testing.T, opts server.Options, client *mcp.Client, areaList ...server.Area) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	s, err := server.New(ctx, opts, areaList...)
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// samplingClient answers sampling/createMessage by inspecting the prompt
// text (every prompt this package sends is single-block, assistant-role
// text - see commandSamplingParams/toolSamplingParams in proxy.go) and
// returning respond's text as the model's answer.
//
//nolint:staticcheck // SA1019: sampling is deprecated (SEP-2577) but still go-sdk v1.8.0's only mechanism for it; see proxy.go's matching section doc comment
func samplingClient(respond func(prompt string) string) *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "c"}, &mcp.ClientOptions{
		CreateMessageHandler: func(_ context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			var prompt string
			if len(req.Params.Messages) > 0 {
				if tc, ok := req.Params.Messages[0].Content.(*mcp.TextContent); ok {
					prompt = tc.Text
				}
			}
			return &mcp.CreateMessageResult{
				Role:    "assistant",
				Content: &mcp.TextContent{Text: respond(prompt)},
			}, nil
		},
	})
}

// samplingAndElicitingClient additionally approves every elicitation, so a
// destructive command resolved by sampling can complete its follow-up
// consent round trip.
//
//nolint:staticcheck // SA1019: see samplingClient above
func samplingAndElicitingClient(respond func(prompt string) string) *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "c"}, &mcp.ClientOptions{
		CreateMessageHandler: func(_ context.Context, req *mcp.CreateMessageRequest) (*mcp.CreateMessageResult, error) {
			var prompt string
			if len(req.Params.Messages) > 0 {
				if tc, ok := req.Params.Messages[0].Content.(*mcp.TextContent); ok {
					prompt = tc.Text
				}
			}
			return &mcp.CreateMessageResult{Role: "assistant", Content: &mcp.TextContent{Text: respond(prompt)}}, nil
		},
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: "accept", Content: map[string]any{"decision": "accept"}}, nil
		},
	})
}

func envelope(t *testing.T, text string) (status int, message string, results map[string]any) {
	t.Helper()
	var env struct {
		Status  int            `json:"status"`
		Message string         `json:"message"`
		Results map[string]any `json:"results"`
	}
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("result isn't the inner tool's envelope: %v\n%s", err, text)
	}
	return env.Status, env.Message, env.Results
}

// --- missing-required-options rewrap (gap 1) ---

func TestNamespaceMissingRequiredRewrap(t *testing.T) {
	cs := connectAreas(t, server.Options{Mode: server.ModeNamespace}, mcp.NewClient(&mcp.Implementation{Name: "c"}, nil), dangerArea{})

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "danger", Arguments: map[string]any{
		"intent": "make one", "command": "danger_make", "parameters": map[string]any{},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("missing-required call IsError = false, want true; content: %+v", res.Content)
	}
	if len(res.Content) != 2 {
		t.Fatalf("missing-required result has %d content blocks, want 2 (guidance + original envelope)", len(res.Content))
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "Missing Required options: --name, --kind\n\n") {
		t.Errorf("missing-required text doesn't start with the response message:\n%s", text)
	}
	for _, want := range []string{
		`- Review the following command spec and identify the required arguments from the input schema.`,
		`- Wrap all command arguments into the root "parameters" argument.`,
		"Command Spec:",
		`"command":"danger_make"`,
		`"inputSchema"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("missing-required text missing %q:\n%s", want, text)
		}
	}
	raw, ok := res.Content[1].(*mcp.TextContent)
	if !ok {
		t.Fatalf("second content block is %T, want *mcp.TextContent", res.Content[1])
	}
	status, message, _ := envelope(t, raw.Text)
	if status != 400 || message != "Missing Required options: --name, --kind" {
		t.Errorf("original envelope = status %d, message %q", status, message)
	}
}

func TestSingleMissingRequiredRewrap(t *testing.T) {
	cs := connectAreas(t, server.Options{Mode: server.ModeSingle}, mcp.NewClient(&mcp.Implementation{Name: "c"}, nil), dangerArea{})

	res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "fabric", Arguments: map[string]any{
		"intent": "make one", "tool": "danger", "command": "danger_make", "parameters": map[string]any{"name": "a"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !res.IsError {
		t.Fatalf("missing-required call IsError = false, want true; content: %+v", res.Content)
	}
	text := res.Content[0].(*mcp.TextContent).Text
	if !strings.HasPrefix(text, "Missing Required options: --kind\n\n") {
		t.Errorf("missing-required text doesn't start with the response message:\n%s", text)
	}
	if !strings.Contains(text, "Command Spec:") || !strings.Contains(text, `"command":"danger_make"`) {
		t.Errorf("missing-required text missing the command spec:\n%s", text)
	}
}

func TestMissingRequiredRewrapOnlyWrapsThatError(t *testing.T) {
	// A tool call that fails for an unrelated reason (still IsError) must
	// not be rewrapped: rewrapMissingRequired only triggers on the
	// "Missing Required options" message.
	cs := connectAreas(t, server.Options{Mode: server.ModeNamespace}, mcp.NewClient(&mcp.Implementation{Name: "c"}, nil), dangerArea{})

	res, text := callText(t, cs, "danger", map[string]any{
		"intent": "x", "command": "danger_nope",
	})
	if !res.IsError {
		t.Fatalf("unknown-command call IsError = false, want true")
	}
	if strings.Contains(text, "Command Spec:") {
		t.Errorf("unrelated error was rewrapped as if missing required options:\n%s", text)
	}
}

// --- sampling-based command/tool guessing (gap 2) ---

func TestNamespaceSamplingResolvesUnknownCommand(t *testing.T) {
	client := samplingClient(func(prompt string) string {
		if !strings.Contains(prompt, "Select the single command") {
			t.Fatalf("unexpected sampling prompt:\n%s", prompt)
		}
		return `{"command":"fake_read","parameters":{"value":"sampled"}}`
	})
	cs := connectAreas(t, server.Options{Mode: server.ModeNamespace}, client, &fakeArea{name: "fake", description: "d", title: "t"})

	res, text := callText(t, cs, "fake", map[string]any{
		"intent": "please read something", "command": "read", // not a real command name; only sampling can resolve it
	})
	if res.IsError {
		t.Fatalf("sampled call returned an error: %s", text)
	}
	status, _, results := envelope(t, text)
	if status != 200 || results["value"] != "sampled" {
		t.Errorf("sampled call envelope = status %d results %v, want 200 and value=sampled", status, results)
	}
}

func TestNamespaceSamplingUnknownFallsBack(t *testing.T) {
	client := samplingClient(func(string) string { return `{"command":"Unknown"}` })
	cs := connectAreas(t, server.Options{Mode: server.ModeNamespace}, client, &fakeArea{name: "fake", description: "d", title: "t"})

	res, text := callText(t, cs, "fake", map[string]any{"intent": "no match", "command": "nope"})
	if !res.IsError {
		t.Fatalf("unresolved sampled call IsError = false, want true")
	}
	if !strings.HasPrefix(text, "The command 'nope' is not available for the 'fake' tool.") {
		t.Errorf("unresolved sampled call text = %q, want the unknown-command result", text)
	}
}

func TestNamespaceLearnSamplingExecutes(t *testing.T) {
	client := samplingClient(func(prompt string) string {
		return `{"command":"fake_write","parameters":{"value":"from-learn"}}`
	})
	cs := connectAreas(t, server.Options{Mode: server.ModeNamespace}, client, &fakeArea{name: "fake", description: "d", title: "t"})

	// learn=true (not just intent-only) still tries sampling first, porting
	// InvokeToolLearn's unconditional follow-up sampling call.
	res, text := callText(t, cs, "fake", map[string]any{"intent": "write something", "learn": true})
	if res.IsError {
		t.Fatalf("learn-mode sampled call returned an error: %s", text)
	}
	status, _, results := envelope(t, text)
	if status != 200 || results["value"] != "from-learn" {
		t.Errorf("learn-mode sampled call envelope = status %d results %v, want 200 and value=from-learn", status, results)
	}
}

func TestSingleToolLearnSamplingExecutes(t *testing.T) {
	client := samplingClient(func(prompt string) string {
		if !strings.Contains(prompt, "The name of the tool to call") {
			t.Fatalf("unexpected sampling prompt (want the single-mode command schema):\n%s", prompt)
		}
		return `{"command":"fake_read","parameters":{"value":"single"}}`
	})
	cs := connectAreas(t, server.Options{Mode: server.ModeSingle}, client, &fakeArea{name: "fake", description: "d", title: "t"})

	// A known tool with an unresolved command falls into ToolLearnModeAsync,
	// which itself tries sampling.
	res, text := callText(t, cs, "fabric", map[string]any{"intent": "read it", "tool": "fake", "command": "nope"})
	if res.IsError {
		t.Fatalf("single-mode sampled call returned an error: %s", text)
	}
	status, _, results := envelope(t, text)
	if status != 200 || results["value"] != "single" {
		t.Errorf("single-mode sampled call envelope = status %d results %v, want 200 and value=single", status, results)
	}
}

func TestSingleRootLearnSamplingChainsToCommand(t *testing.T) {
	// The root "which tool" question and the "which command" question are
	// two separate sampling round trips (RootLearnModeAsync ->
	// ToolLearnModeAsync); the resolved tool name has to survive between
	// them via RequestState.
	client := samplingClient(func(prompt string) string {
		switch {
		case strings.Contains(prompt, "Select a single tool"):
			return "fake"
		case strings.Contains(prompt, "Select the single command"):
			return `{"command":"fake_write","parameters":{"value":"chained"}}`
		default:
			t.Fatalf("unexpected sampling prompt:\n%s", prompt)
			return ""
		}
	})
	cs := connectAreas(t, server.Options{Mode: server.ModeSingle}, client, &fakeArea{name: "fake", description: "d", title: "t"}, &bareArea{name: "bare"})

	res, text := callText(t, cs, "fabric", map[string]any{"intent": "write something to fake"})
	if res.IsError {
		t.Fatalf("chained sampled call returned an error: %s", text)
	}
	status, _, results := envelope(t, text)
	if status != 200 || results["value"] != "chained" {
		t.Errorf("chained sampled call envelope = status %d results %v, want 200 and value=chained", status, results)
	}
}

func TestSingleRootLearnSamplingUnknownToolFallsBack(t *testing.T) {
	client := samplingClient(func(string) string { return "Unknown" })
	cs := connectAreas(t, server.Options{Mode: server.ModeSingle}, client, &fakeArea{name: "fake", description: "d", title: "t"})

	res, text := callText(t, cs, "fabric", map[string]any{"intent": "do something unrelated"})
	if res.IsError {
		t.Errorf("unresolved root sampling IsError = true, want false (soft fallback)")
	}
	if !strings.HasPrefix(text, "Here are the available tools.\n") {
		t.Errorf("unresolved root sampling text = %q, want the root listing", text)
	}
}

func TestNamespaceSamplingDestructiveStillAsksConsent(t *testing.T) {
	// A sampled command that turns out to be destructive still needs a
	// second, separate round trip for consent; the resolution (not just
	// the command name) has to survive that round too - see callNamedTool's
	// RequestState handling.
	client := samplingAndElicitingClient(func(string) string {
		return `{"command":"danger_delete","parameters":{"value":"x"}}`
	})
	cs := connectAreas(t, server.Options{Mode: server.ModeNamespace}, client, dangerArea{})

	res, text := callText(t, cs, "danger", map[string]any{"intent": "remove it", "command": "delete-please"})
	if res.IsError {
		t.Fatalf("sampled destructive call returned an error: %s", text)
	}
	status, _, results := envelope(t, text)
	if status != 200 || results["value"] != "x" {
		t.Errorf("sampled destructive call envelope = status %d results %v, want 200 and value=x", status, results)
	}
}
