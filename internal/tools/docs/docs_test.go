package docs_test

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/docs"
)

// connect wires an in-memory client to a fresh docs-only server, mirroring
// the pattern used throughout the go-sdk's own tests.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := t.Context()
	// This suite exercises the docs_* tools directly, so it asks for mode
	// "all" explicitly rather than relying on the default (now "namespace",
	// which would collapse them into one "docs" proxy tool).
	s, err := server.New(ctx, server.Options{Mode: server.ModeAll}, docs.New())
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}
	t1, t2 := mcp.NewInMemoryTransports()
	if _, err := s.Connect(ctx, t1, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "docs-test-client", Version: "v0.0.1"}, nil)
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

// TestListTools verifies the six docs_* tools this area registers, each
// prefixed by server.AddTool per internal/server/server.go.
func TestListTools(t *testing.T) {
	session := connect(t)
	res, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	want := []string{
		"docs_list-item-types",
		"docs_item-api-spec",
		"docs_platform-api-spec",
		"docs_item-definitions",
		"docs_best-practices",
		"docs_api-examples",
	}
	var got []string
	byName := map[string]*mcp.Tool{}
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		byName[tool.Name] = tool
	}
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}

	// Every tool in this area is read-only, non-destructive, idempotent and
	// closed-world, matching each upstream command's CommandMetadata.
	for _, name := range want {
		tool := byName[name]
		ann := tool.Annotations
		if ann == nil {
			t.Fatalf("%s: Annotations is nil", name)
		}
		if !ann.ReadOnlyHint {
			t.Errorf("%s: ReadOnlyHint = false, want true", name)
		}
		if !ann.IdempotentHint {
			t.Errorf("%s: IdempotentHint = false, want true", name)
		}
		if ann.DestructiveHint == nil || *ann.DestructiveHint {
			t.Errorf("%s: DestructiveHint = %v, want false", name, ann.DestructiveHint)
		}
		if ann.OpenWorldHint == nil || *ann.OpenWorldHint {
			t.Errorf("%s: OpenWorldHint = %v, want false", name, ann.OpenWorldHint)
		}
	}
}

// envelope is the {"status":...,"message":...,"results":...,"duration":...}
// shape every docs_* tool answers with, ported from the actual v1.4.0
// reference server rather than the (unreleased, differently-shaped) C#
// source tree - see the doc comment on handlers.go's successEnvelope.
type envelope struct {
	Status   int    `json:"status"`
	Message  string `json:"message"`
	Results  any    `json:"results"`
	Duration int    `json:"duration"`
}

// callEnvelope calls tool and decodes its single text content block as an
// envelope, asserting there's no structuredContent (this area never sets
// it - see handlers.go) and exactly one text content block.
func callEnvelope(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (*mcp.CallToolResult, envelope) {
	t.Helper()
	res, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.StructuredContent != nil {
		t.Errorf("CallTool(%s): StructuredContent = %v, want nil", name, res.StructuredContent)
	}
	if len(res.Content) != 1 {
		t.Fatalf("CallTool(%s): Content has %d blocks, want exactly 1", name, len(res.Content))
	}
	text, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool(%s): Content[0] is %T, want *mcp.TextContent", name, res.Content[0])
	}
	var env envelope
	if err := json.Unmarshal([]byte(text.Text), &env); err != nil {
		t.Fatalf("CallTool(%s): content is not a JSON envelope: %v\ntext: %s", name, err, text.Text)
	}
	return res, env
}

// TestCallTools calls each of the six tools with a real argument end to
// end through the MCP wire protocol (in-memory transport), asserting a
// successful, non-error envelope.
func TestCallTools(t *testing.T) {
	session := connect(t)

	cases := []struct {
		name string
		args map[string]any
	}{
		{"docs_list-item-types", nil},
		{"docs_item-api-spec", map[string]any{"item-type": "lakehouse"}},
		{"docs_platform-api-spec", nil},
		{"docs_item-definitions", map[string]any{"item-type": "lakehouse"}},
		{"docs_best-practices", map[string]any{"topic": "throttling"}},
		{"docs_api-examples", map[string]any{"item-type": "lakehouse"}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, env := callEnvelope(t, session, c.name, c.args)
			if res.IsError {
				t.Fatalf("CallTool(%s) returned a tool error: %v", c.name, res.Content)
			}
			if env.Status != 200 {
				t.Errorf("CallTool(%s): envelope status = %d, want 200", c.name, env.Status)
			}
			if env.Message != "Success" {
				t.Errorf("CallTool(%s): envelope message = %q, want %q", c.name, env.Message, "Success")
			}
			if env.Results == nil {
				t.Errorf("CallTool(%s): envelope results is nil", c.name)
			}
		})
	}
}

// TestCallToolsUnknownArguments ports the NotFound-style test cases from
// GetItemApisCommandTests, GetItemDefinitionCommandTests and
// GetBestPracticesCommandTests: an unknown item type or topic must come
// back as a tool error (IsError, with content the model can read and
// self-correct from), never as an MCP protocol-level error. The expected
// status codes and messages were captured from the live v1.4.0 reference
// binary (see handlers.go's handleExceptionEnvelope doc comment): only
// "common" and item-definitions/best-practices lookups get a clean 404,
// because only their commands catch the underlying not-found error
// specifically. A plain unknown item type on item-api-spec falls through
// to the generic 400 exception envelope instead.
func TestCallToolsUnknownArguments(t *testing.T) {
	session := connect(t)

	cases := []struct {
		name        string
		args        map[string]any
		wantStatus  int
		wantContain string
	}{
		{"docs_item-api-spec", map[string]any{"item-type": "common"}, 404, "platform"},
		{"docs_item-api-spec", map[string]any{"item-type": "this-item-type-does-not-exist"}, 400, "this-item-type-does-not-exist"},
		{"docs_item-definitions", map[string]any{"item-type": "this-item-type-does-not-exist"}, 404, "this-item-type-does-not-exist"},
		{"docs_best-practices", map[string]any{"topic": "this-topic-does-not-exist"}, 404, "this-topic-does-not-exist"},
	}

	for _, c := range cases {
		t.Run(c.name+"/"+argString(c.args), func(t *testing.T) {
			res, env := callEnvelope(t, session, c.name, c.args)
			if !res.IsError {
				t.Fatalf("CallTool(%s) IsError = false, want true", c.name)
			}
			if env.Status != c.wantStatus {
				t.Errorf("CallTool(%s): envelope status = %d, want %d", c.name, env.Status, c.wantStatus)
			}
			if !strings.Contains(strings.ToLower(env.Message), strings.ToLower(c.wantContain)) {
				t.Errorf("CallTool(%s) error message = %q, want it to mention %q", c.name, env.Message, c.wantContain)
			}
		})
	}
}

func argString(args map[string]any) string {
	for _, v := range args {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return "none"
}
