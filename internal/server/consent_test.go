package server_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

type requiredInput struct {
	Name  string `json:"name" jsonschema:"A required name."`
	Kind  string `json:"kind" jsonschema:"A required kind."`
	Extra string `json:"extra,omitempty" jsonschema:"An optional value."`
}

// dangerArea has one destructive tool and one with required arguments.
type dangerArea struct{}

func (dangerArea) Name() string { return "danger" }

func (dangerArea) Register(r *server.Registrar) {
	destructive := true
	server.AddTool(r, &mcp.Tool{Name: "delete", Description: "Deletes.", Annotations: &mcp.ToolAnnotations{DestructiveHint: &destructive}}, echoHandler)
	server.AddTool(r, &mcp.Tool{Name: "make", Description: "Makes."}, func(context.Context, *mcp.CallToolRequest, requiredInput) (*mcp.CallToolResult, any, error) {
		return nil, map[string]string{"ok": "yes"}, nil
	})
}

// connectWith is connect with a custom client, e.g. one that answers elicitation.
func connectWith(t *testing.T, opts server.Options, client *mcp.Client) *mcp.ClientSession {
	t.Helper()
	s, err := server.New(t.Context(), opts, dangerArea{})
	if err != nil {
		t.Fatal(err)
	}
	st, ct := mcp.NewInMemoryTransports()
	if _, err := s.Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := client.Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func elicitingClient(action, decision string) *mcp.Client {
	return mcp.NewClient(&mcp.Implementation{Name: "c"}, &mcp.ClientOptions{
		ElicitationHandler: func(context.Context, *mcp.ElicitRequest) (*mcp.ElicitResult, error) {
			return &mcp.ElicitResult{Action: action, Content: map[string]any{"decision": decision}}, nil
		},
	})
}

func TestConsent(t *testing.T) {
	const refused = "This tool may perform destructive operations and requires user consent, but the client does not support elicitation. Operation rejected for security."
	plain := mcp.NewClient(&mcp.Implementation{Name: "c"}, nil)
	tests := []struct {
		name   string
		opts   server.Options
		client *mcp.Client
		want   string // "" means the tool ran
	}{
		{"no elicitation", server.Options{Mode: server.ModeAll}, plain, refused},
		{"disabled", server.Options{Mode: server.ModeAll, DisableElicitation: true}, plain, ""},
		{"approved", server.Options{Mode: server.ModeAll}, elicitingClient("accept", "accept"), ""},
		{"rejected in form", server.Options{Mode: server.ModeAll}, elicitingClient("accept", "reject"), "Operation cancelled by user."},
		{"declined", server.Options{Mode: server.ModeAll}, elicitingClient("decline", ""), "Operation cancelled by user."},
		{"namespace, no elicitation", server.Options{}, plain, refused},
		{"namespace, approved", server.Options{}, elicitingClient("accept", "accept"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := connectWith(t, tt.opts, tt.client)
			params := &mcp.CallToolParams{Name: "danger_delete", Arguments: map[string]any{"value": "x"}}
			if tt.opts.Mode == "" {
				params = &mcp.CallToolParams{Name: "danger", Arguments: map[string]any{
					"intent": "delete", "command": "danger_delete", "parameters": map[string]any{"value": "x"},
				}}
			}
			res, err := cs.CallTool(t.Context(), params)
			if err != nil {
				t.Fatal(err)
			}
			text := res.Content[0].(*mcp.TextContent).Text
			if tt.want == "" {
				if res.IsError {
					t.Errorf("tool did not run: %s", text)
				}
			} else if !res.IsError || text != tt.want {
				t.Errorf("got %q (isError=%v), want %q", text, res.IsError, tt.want)
			}
		})
	}
}

func TestRequiredArguments(t *testing.T) {
	cs := connectWith(t, server.Options{Mode: server.ModeAll}, mcp.NewClient(&mcp.Implementation{Name: "c"}, nil))
	tests := []struct {
		args map[string]any
		want string
	}{
		{nil, `{"status":400,"message":"Missing Required options: --name, --kind","duration":0}`},
		{map[string]any{"name": "a"}, `{"status":400,"message":"Missing Required options: --kind","duration":0}`},
		{map[string]any{"name": " ", "kind": "k"}, `{"status":400,"message":"Option '--name' was configured to require non-empty, non-whitespace values but one or more empty or whitespace values were provided.","duration":0}`},
		{map[string]any{"name": "a", "kind": "k"}, `{"ok":"yes"}`},
	}
	for _, tt := range tests {
		res, err := cs.CallTool(t.Context(), &mcp.CallToolParams{Name: "danger_make", Arguments: tt.args})
		if err != nil {
			t.Fatal(err)
		}
		got := res.Content[0].(*mcp.TextContent).Text
		var a, b any
		_ = json.Unmarshal([]byte(got), &a)
		_ = json.Unmarshal([]byte(tt.want), &b)
		if ga, _ := json.Marshal(a); string(ga) != func() string { gb, _ := json.Marshal(b); return string(gb) }() {
			t.Errorf("args %v: got %s, want %s", tt.args, got, tt.want)
		}
	}
}
