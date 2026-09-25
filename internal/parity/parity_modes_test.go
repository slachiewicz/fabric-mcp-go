package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestParityNamespaceMode compares --mode namespace between the reference
// server and ours for the docs and core areas: the "docs"/"core" proxy
// tools' shape (title, description, annotations, input schema - reusing
// compareTool from parity_test.go), their learn=true output (as a set of
// commands, each compared by description and input schema - command order
// isn't compared; see areaTools's doc comment in internal/server/proxy.go
// for why upstream and the go-sdk's tool registry order these
// differently), and one routed docs call end to end.
//
// Like TestParityCalls, this always needs a live reference server, so it's
// skipped when FABMCP_REF is unset.
func TestParityNamespaceMode(t *testing.T) {
	refPath := os.Getenv("FABMCP_REF")
	if refPath == "" {
		t.Skip("set FABMCP_REF to the reference fabmcp binary to run mode-parity checks")
	}
	ctx := t.Context()

	refSess := startSession(t, ctx, "reference", refPath, namespaceServerArgs(), refEnv())
	ourBin := buildOurBinary(t)
	ourSess := startSession(t, ctx, "ours", ourBin, namespaceServerArgs(), nil)

	refTools := listTools(t, ctx, refSess)
	ourTools := listTools(t, ctx, ourSess)

	if diff := setDiff(toolNames(refTools), toolNames(ourTools)); diff != "" {
		t.Fatalf("namespace-mode tool set differs from reference: %s", diff)
	}

	refByName := toolsByName(refTools)
	ourByName := toolsByName(ourTools)
	for _, name := range namespaces {
		t.Run(name+"/shape", func(t *testing.T) {
			compareTool(t, name, refByName[name], ourByName[name])
		})
		t.Run(name+"/learn", func(t *testing.T) {
			compareNamespaceLearn(t, ctx, refSess, ourSess, name)
		})
	}

	t.Run("docs/routed-call", func(t *testing.T) {
		compareRoutedDocsCall(t, ctx, refSess, ourSess)
	})
}

func namespaceServerArgs() []string {
	args := []string{"server", "start", "--mode", "namespace"}
	for _, ns := range namespaces {
		args = append(args, "--namespace", ns)
	}
	return args
}

func listTools(t *testing.T, ctx context.Context, sess *mcp.ClientSession) []*mcp.Tool {
	t.Helper()
	var tools []*mcp.Tool
	for tool, err := range sess.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("tools/list: %v", err)
		}
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// learnInfo mirrors internal/server/proxy.go's toolInfo, the shape both the
// reference and our namespace-mode "learn" output list each command as.
type learnInfo struct {
	Tool        string `json:"tool,omitempty"`
	Command     string `json:"command,omitempty"`
	Description string `json:"description,omitempty"`
	InputSchema any    `json:"inputSchema,omitempty"`
}

// compareNamespaceLearn calls tool with learn=true on both sessions and
// compares the resulting command set, plus each command's description and
// input schema (via compareInputSchema from parity_test.go). Order is not
// compared - see this file's package doc comment.
func compareNamespaceLearn(t *testing.T, ctx context.Context, refSess, ourSess *mcp.ClientSession, tool string) {
	t.Helper()

	refInfos := learnInfos(t, ctx, refSess, tool)
	ourInfos := learnInfos(t, ctx, ourSess, tool)

	refByCommand := infosByCommand(refInfos)
	ourByCommand := infosByCommand(ourInfos)

	var refCommands, ourCommands []string
	for c := range refByCommand {
		refCommands = append(refCommands, c)
	}
	for c := range ourByCommand {
		ourCommands = append(ourCommands, c)
	}
	if diff := setDiff(refCommands, ourCommands); diff != "" {
		t.Errorf("tool %s: learn command set differs: %s", tool, diff)
	}

	for command, refInfo := range refByCommand {
		ourInfo, ok := ourByCommand[command]
		if !ok {
			continue // already reported above.
		}
		if refInfo.Description != ourInfo.Description {
			t.Errorf("tool %s command %s: description =\n\t%q\nwant\n\t%q", tool, command, ourInfo.Description, refInfo.Description)
		}
		compareInputSchema(t, tool+"/"+command, refInfo.InputSchema, ourInfo.InputSchema)
	}
}

func infosByCommand(infos []learnInfo) map[string]learnInfo {
	m := make(map[string]learnInfo, len(infos))
	for _, info := range infos {
		m[info.Command] = info
	}
	return m
}

func learnInfos(t *testing.T, ctx context.Context, sess *mcp.ClientSession, tool string) []learnInfo {
	t.Helper()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: map[string]any{"intent": "x", "learn": true}})
	if err != nil {
		t.Fatalf("CallTool(%s, learn=true): %v", tool, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s, learn=true) returned an error: %v", tool, res.Content)
	}
	text := firstText(t, res)
	i := strings.Index(text, "[")
	if i < 0 {
		t.Fatalf("CallTool(%s, learn=true) text has no JSON array:\n%s", tool, text)
	}
	var infos []learnInfo
	if err := json.Unmarshal([]byte(text[i:]), &infos); err != nil {
		t.Fatalf("CallTool(%s, learn=true) JSON tail doesn't parse: %v\n%s", tool, err, text)
	}
	return infos
}

func firstText(t *testing.T, res *mcp.CallToolResult) string {
	t.Helper()
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			return tc.Text
		}
	}
	t.Fatalf("result has no text content block: %+v", res)
	return ""
}

// compareRoutedDocsCall routes a docs_list-item-types call through the
// "docs" namespace-mode proxy tool on both servers and compares the result
// the way compareCall (parity_test.go) compares a direct "all"-mode call:
// by JSON key-path set, then by full value with string arrays sorted (the
// item type list order comes from .NET resource enumeration - see calls.json's
// "unordered" entry for this same tool in TestParityCalls).
func compareRoutedDocsCall(t *testing.T, ctx context.Context, refSess, ourSess *mcp.ClientSession) {
	t.Helper()

	params := &mcp.CallToolParams{
		Name:      "docs",
		Arguments: map[string]any{"intent": "x", "command": "docs_list-item-types", "parameters": map[string]any{}},
	}
	refRes, err := refSess.CallTool(ctx, params)
	if err != nil {
		t.Fatalf("reference CallTool(docs, docs_list-item-types): %v", err)
	}
	ourRes, err := ourSess.CallTool(ctx, params)
	if err != nil {
		t.Fatalf("our CallTool(docs, docs_list-item-types): %v", err)
	}

	if refRes.IsError != ourRes.IsError {
		t.Errorf("IsError = %v, want %v", ourRes.IsError, refRes.IsError)
	}

	refVal, err := resultValue(refRes)
	if err != nil {
		t.Fatalf("decode reference result: %v", err)
	}
	ourVal, err := resultValue(ourRes)
	if err != nil {
		t.Fatalf("decode our result: %v", err)
	}

	refPaths := collectPaths("", refVal)
	ourPaths := collectPaths("", ourVal)
	if diff := setDiff(refPaths, ourPaths); diff != "" {
		t.Errorf("routed docs call result JSON key paths differ: %s", diff)
	}

	refVal, ourVal = sortStringArrays(refVal), sortStringArrays(ourVal)
	refJSON, err := json.Marshal(refVal)
	if err != nil {
		t.Fatalf("marshal reference result: %v", err)
	}
	ourJSON, err := json.Marshal(ourVal)
	if err != nil {
		t.Fatalf("marshal our result: %v", err)
	}
	if !bytes.Equal(refJSON, ourJSON) {
		t.Errorf("routed docs call result values differ:\n  reference: %s\n  ours:      %s", refJSON, ourJSON)
	}
}
