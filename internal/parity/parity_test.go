// Package parity compares the Go Fabric MCP server (github.com/slachiewicz/fabric-mcp-go)
// against the upstream .NET server (github.com/microsoft/mcp) over the MCP
// stdio transport. The reference is upstream main, built from source:
//
//	git clone --depth 1 https://github.com/microsoft/mcp.git
//	dotnet build mcp/servers/Fabric.Mcp.Server/src/Fabric.Mcp.Server.csproj -c Release
//
// Set FABMCP_REF to the built binary to run the live checks. The binary is
// framework-dependent, so DOTNET_ROOT must point at a .NET 10 runtime:
//
//	DOTNET_ROOT=~/.dotnet FABMCP_REF=mcp/servers/Fabric.Mcp.Server/src/bin/Release/fabmcp \
//	    go test ./internal/parity/ -run Parity -v
//
// The reference server takes several seconds to complete the MCP
// initialize handshake on a cold run; connectTimeout below allows for that.
//
// Run with -update (FABMCP_REF must be set) to refresh
// testdata/ref-tools.json from a live reference server:
//
//	FABMCP_REF=... go test ./internal/parity/ -run Parity -v -update
//
// Once that file exists, TestParityToolsList compares against it even when
// FABMCP_REF is unset, so the tool-list parity check runs without .NET. TestParityCalls
// always needs a live reference server, since it exercises real tool calls
// on both sides, so it is skipped when FABMCP_REF is unset.
package parity

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// namespaces lists the tool namespaces this harness compares. Extend as
// more areas land in internal/tools.
var namespaces = []string{"docs", "core"}

const (
	ourCmdImportPath = "github.com/slachiewicz/fabric-mcp-go/cmd/fabmcp"
	refToolsGolden   = "testdata/ref-tools.json"
	callsDataPath    = "testdata/calls.json"

	// connectTimeout bounds only the initialize handshake, not the
	// process's overall lifetime (see startSession).
	connectTimeout = 90 * time.Second

	// fullCompareLimit is the size, in bytes of the marshaled JSON value,
	// under which a tool call's full result is compared value-for-value
	// rather than just by its set of JSON key paths.
	fullCompareLimit = 64 * 1024
)

var update = flag.Bool("update", false, "write the live reference tools/list to testdata/ref-tools.json instead of comparing")

// TestParityToolsList compares the tools/list output of the namespaces in
// scope (see namespaces) between the reference server and ours: the tool
// name set, title, description, annotations, inputSchema property names,
// required list, and property descriptions.
func TestParityToolsList(t *testing.T) {
	ctx := t.Context()
	refPath := os.Getenv("FABMCP_REF")

	var refTools []*mcp.Tool
	if refPath != "" {
		refTools = fetchTools(t, ctx, "reference", refPath, refEnv())
		if *update {
			writeGolden(t, refTools)
			t.Logf("wrote %d reference tool(s) to %s", len(refTools), refToolsGolden)
			return
		}
	} else {
		data, err := os.ReadFile(refToolsGolden)
		if err != nil {
			t.Skipf("set FABMCP_REF to the reference fabmcp binary (or run once with FABMCP_REF set and -update to populate %s): %v", refToolsGolden, err)
		}
		if err := json.Unmarshal(data, &refTools); err != nil {
			t.Fatalf("parse %s: %v", refToolsGolden, err)
		}
	}

	ourBin := buildOurBinary(t)
	ourTools := fetchTools(t, ctx, "ours", ourBin, nil)

	compareToolLists(t, refTools, ourTools)
}

// TestParityCalls runs the data-driven tool calls in testdata/calls.json
// against both servers and compares the JSON shape (and, for small results,
// the full value) of each response.
func TestParityCalls(t *testing.T) {
	refPath := os.Getenv("FABMCP_REF")
	if refPath == "" {
		t.Skip("set FABMCP_REF to the reference fabmcp binary to run call-parity checks")
	}
	ctx := t.Context()

	calls := loadCalls(t)

	refSess := startSession(t, ctx, "reference", refPath, serverArgs(), refEnv())
	ourBin := buildOurBinary(t)
	ourSess := startSession(t, ctx, "ours", ourBin, serverArgs(), nil)

	for _, call := range calls {
		call := call
		t.Run(call.Tool, func(t *testing.T) {
			compareCall(t, ctx, refSess, ourSess, call)
		})
	}
}

// --- tools/list comparison ---

func compareToolLists(t *testing.T, refTools, ourTools []*mcp.Tool) {
	t.Helper()

	ref := filterNamespaces(refTools, namespaces)
	our := filterNamespaces(ourTools, namespaces)

	if diff := setDiff(toolNames(ref), toolNames(our)); diff != "" {
		t.Errorf("tool set for namespace(s) %v differs from reference: %s", namespaces, diff)
	}

	refByName := toolsByName(ref)
	ourByName := toolsByName(our)
	for name, refTool := range refByName {
		ourTool, ok := ourByName[name]
		if !ok {
			continue // already reported by the set diff above.
		}
		compareTool(t, name, refTool, ourTool)
	}
}

func compareTool(t *testing.T, name string, ref, our *mcp.Tool) {
	t.Helper()

	if got, want := effectiveTitle(our), effectiveTitle(ref); got != want {
		t.Errorf("tool %s: title = %q, want %q", name, got, want)
	}
	if our.Description != ref.Description {
		t.Errorf("tool %s: description =\n\t%q\nwant\n\t%q", name, our.Description, ref.Description)
	}
	compareAnnotations(t, name, ref.Annotations, our.Annotations)
	compareInputSchema(t, name, ref.InputSchema, our.InputSchema)
}

// effectiveTitle mirrors the client display-name precedence documented on
// mcp.Tool: Title, then Annotations.Title, then Name.
func effectiveTitle(tool *mcp.Tool) string {
	if tool.Title != "" {
		return tool.Title
	}
	if tool.Annotations != nil && tool.Annotations.Title != "" {
		return tool.Annotations.Title
	}
	return tool.Name
}

func compareAnnotations(t *testing.T, name string, ref, our *mcp.ToolAnnotations) {
	t.Helper()

	if (ref == nil) != (our == nil) {
		t.Errorf("tool %s: annotations present = %v, want %v", name, our != nil, ref != nil)
		return
	}
	if ref == nil {
		return
	}
	if !reflect.DeepEqual(our, ref) {
		t.Errorf("tool %s: annotations = %s, want %s", name, formatAnnotations(our), formatAnnotations(ref))
	}
}

func formatAnnotations(a *mcp.ToolAnnotations) string {
	return fmt.Sprintf(
		"{destructiveHint:%s idempotentHint:%v openWorldHint:%s readOnlyHint:%v title:%q}",
		formatBoolPtr(a.DestructiveHint), a.IdempotentHint, formatBoolPtr(a.OpenWorldHint), a.ReadOnlyHint, a.Title,
	)
}

func formatBoolPtr(b *bool) string {
	if b == nil {
		return "<default>"
	}
	return fmt.Sprintf("%v", *b)
}

func compareInputSchema(t *testing.T, name string, refSchema, ourSchema any) {
	t.Helper()

	refProps, refRequired := schemaPropsAndRequired(refSchema)
	ourProps, ourRequired := schemaPropsAndRequired(ourSchema)

	if diff := setDiff(mapKeys(refProps), mapKeys(ourProps)); diff != "" {
		t.Errorf("tool %s: inputSchema properties differ: %s", name, diff)
	}
	if diff := setDiff(refRequired, ourRequired); diff != "" {
		t.Errorf("tool %s: inputSchema required differs: %s", name, diff)
	}

	for propName, refProp := range refProps {
		ourProp, ok := ourProps[propName]
		if !ok {
			continue // already reported above.
		}
		refDesc, _ := refProp["description"].(string)
		ourDesc, _ := ourProp["description"].(string)
		if refDesc != ourDesc {
			t.Errorf("tool %s: inputSchema property %q description =\n\t%q\nwant\n\t%q", name, propName, ourDesc, refDesc)
		}
	}
}

// schemaPropsAndRequired reads properties and required out of a decoded
// JSON Schema value. Tool.InputSchema always arrives at the client as the
// server's raw JSON unmarshaled into map[string]any, regardless of which
// language produced it, so this works uniformly for both servers and for
// schemas loaded back from testdata/ref-tools.json.
func schemaPropsAndRequired(schema any) (map[string]map[string]any, []string) {
	root, _ := schema.(map[string]any)

	props := map[string]map[string]any{}
	if raw, ok := root["properties"].(map[string]any); ok {
		for propName, v := range raw {
			if propSchema, ok := v.(map[string]any); ok {
				props[propName] = propSchema
			}
		}
	}

	var required []string
	if raw, ok := root["required"].([]any); ok {
		for _, v := range raw {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}
	sort.Strings(required)

	return props, required
}

func writeGolden(t *testing.T, tools []*mcp.Tool) {
	t.Helper()

	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	data, err := json.MarshalIndent(tools, "", "  ")
	if err != nil {
		t.Fatalf("marshal reference tools: %v", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(refToolsGolden, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", refToolsGolden, err)
	}
}

func fetchTools(t *testing.T, ctx context.Context, label, path string, env []string) []*mcp.Tool {
	t.Helper()

	sess := startSession(t, ctx, label, path, serverArgs(), env)
	var tools []*mcp.Tool
	for tool, err := range sess.Tools(ctx, nil) {
		if err != nil {
			t.Fatalf("%s tools/list: %v", label, err)
		}
		tools = append(tools, tool)
	}
	sort.Slice(tools, func(i, j int) bool { return tools[i].Name < tools[j].Name })
	return tools
}

// --- data-driven tool call comparison ---

type toolCall struct {
	Tool string         `json:"tool"`
	Args map[string]any `json:"args"`
	// Unordered compares arrays of strings as sets. Use it where the order
	// comes from .NET resource enumeration, which embed.FS can't reproduce.
	Unordered bool `json:"unordered"`
	// PathsOnly skips the value comparison, for results that carry request
	// IDs or other per-call values.
	PathsOnly bool `json:"pathsOnly"`
}

func loadCalls(t *testing.T) []toolCall {
	t.Helper()

	data, err := os.ReadFile(callsDataPath)
	if err != nil {
		t.Fatalf("read %s: %v", callsDataPath, err)
	}
	var calls []toolCall
	if err := json.Unmarshal(data, &calls); err != nil {
		t.Fatalf("parse %s: %v", callsDataPath, err)
	}
	if len(calls) == 0 {
		t.Fatalf("%s contains no calls", callsDataPath)
	}
	return calls
}

func compareCall(t *testing.T, ctx context.Context, refSess, ourSess *mcp.ClientSession, call toolCall) {
	t.Helper()

	refRes, refErr := refSess.CallTool(ctx, &mcp.CallToolParams{Name: call.Tool, Arguments: call.Args})
	ourRes, ourErr := ourSess.CallTool(ctx, &mcp.CallToolParams{Name: call.Tool, Arguments: call.Args})

	if (refErr == nil) != (ourErr == nil) {
		t.Errorf("call error mismatch: reference err = %v, ours err = %v", refErr, ourErr)
		return
	}
	if refErr != nil {
		// Both sides errored at the protocol level (e.g. the tool isn't
		// registered on either server yet); nothing further to compare.
		t.Logf("both servers returned an error for %s(%v): reference=%v ours=%v", call.Tool, call.Args, refErr, ourErr)
		return
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
		t.Errorf("result JSON key paths differ: %s", diff)
	}

	if call.Unordered {
		refVal, ourVal = sortStringArrays(refVal), sortStringArrays(ourVal)
	}
	refJSON, err := json.Marshal(refVal)
	if err != nil {
		t.Fatalf("marshal reference result: %v", err)
	}
	ourJSON, err := json.Marshal(ourVal)
	if err != nil {
		t.Fatalf("marshal our result: %v", err)
	}
	if !call.PathsOnly && len(refJSON) < fullCompareLimit && len(ourJSON) < fullCompareLimit && !bytes.Equal(refJSON, ourJSON) {
		t.Errorf("result values differ:\n  reference: %s\n  ours:      %s", refJSON, ourJSON)
	}
}

// sortStringArrays returns v with every array of strings sorted.
func sortStringArrays(v any) any {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			x[k] = sortStringArrays(e)
		}
	case []any:
		strs := make([]string, 0, len(x))
		for i, e := range x {
			x[i] = sortStringArrays(e)
			if s, ok := e.(string); ok {
				strs = append(strs, s)
			}
		}
		if len(strs) == len(x) {
			slices.Sort(strs)
			for i, s := range strs {
				x[i] = s
			}
		}
	}
	return v
}

// resultValue extracts the JSON value a call result carries: its
// StructuredContent if set, otherwise the first text content block decoded
// as JSON (falling back to the raw string if it isn't JSON).
func resultValue(res *mcp.CallToolResult) (any, error) {
	if res.StructuredContent != nil {
		return res.StructuredContent, nil
	}
	for _, c := range res.Content {
		tc, ok := c.(*mcp.TextContent)
		if !ok || tc.Text == "" {
			continue
		}
		var v any
		if err := json.Unmarshal([]byte(tc.Text), &v); err != nil {
			return tc.Text, nil // Not JSON; compare as an opaque leaf value.
		}
		return v, nil
	}
	return nil, nil
}

// collectPaths flattens a decoded JSON value into a set of key paths, one
// per leaf (scalar or empty container), e.g. "results[0].title". It is used
// to compare result *structure* without requiring identical content.
func collectPaths(prefix string, v any) []string {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 0 {
			return []string{prefix + "{}"}
		}
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var out []string
		for _, k := range keys {
			child := k
			if prefix != "" {
				child = prefix + "." + k
			}
			out = append(out, collectPaths(child, x[k])...)
		}
		return out
	case []any:
		if len(x) == 0 {
			return []string{prefix + "[]"}
		}
		var out []string
		for i, e := range x {
			out = append(out, collectPaths(fmt.Sprintf("%s[%d]", prefix, i), e)...)
		}
		return out
	default:
		if prefix == "" {
			return []string{"$"} // Scalar (or nil) result root.
		}
		return []string{prefix}
	}
}

// --- server process management ---

// startSession launches path as an MCP server over stdio and connects a
// client to it. The process's lifetime is tied to ctx (typically the
// test's own context, so it's torn down when the test ends); connectTimeout
// bounds only the initialize handshake, since the reference binary is a
// full .NET host that can take upward of 15-20s just to start responding.
func startSession(t *testing.T, ctx context.Context, label, path string, args, env []string) *mcp.ClientSession {
	t.Helper()

	cmd := exec.CommandContext(ctx, path, args...)
	if env != nil {
		cmd.Env = env
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	connectCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "fabmcp-parity", Version: "0.0.0"}, nil)
	sess, err := client.Connect(connectCtx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to %s server (%s %s): %v\nstderr:\n%s", label, path, strings.Join(args, " "), err, stderr.String())
	}
	t.Cleanup(func() {
		err := sess.Close()
		// Once ctx is canceled (test/subtest teardown), exec.CommandContext
		// races its own kill of the process against this graceful Close,
		// which routinely surfaces as a "signal: killed" error here. That's
		// expected teardown, not a real failure, so only log Close errors
		// that happen while ctx is still live.
		if err != nil && ctx.Err() == nil {
			t.Logf("close %s session: %v", label, err)
		}
	})
	return sess
}

func serverArgs() []string {
	args := []string{"server", "start", "--mode", "all"}
	for _, ns := range namespaces {
		args = append(args, "--namespace", ns)
	}
	return args
}

// refEnv disables telemetry the reference server would otherwise try to
// send, so the parity run doesn't depend on outbound network access.
func refEnv() []string {
	return append(os.Environ(),
		"AZURE_MCP_COLLECT_TELEMETRY=false",
		"DOTNET_CLI_TELEMETRY_OPTOUT=1",
	)
}

func buildOurBinary(t *testing.T) string {
	t.Helper()

	out := filepath.Join(t.TempDir(), "fabmcp")
	cmd := exec.Command("go", "build", "-o", out, ourCmdImportPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("go build %s: %v\n%s", ourCmdImportPath, err, stderr.String())
	}
	return out
}

// --- small generic helpers ---

func filterNamespaces(tools []*mcp.Tool, ns []string) []*mcp.Tool {
	var out []*mcp.Tool
	for _, tool := range tools {
		for _, n := range ns {
			if strings.HasPrefix(tool.Name, n+"_") {
				out = append(out, tool)
				break
			}
		}
	}
	return out
}

func toolsByName(tools []*mcp.Tool) map[string]*mcp.Tool {
	m := make(map[string]*mcp.Tool, len(tools))
	for _, tool := range tools {
		m[tool.Name] = tool
	}
	return m
}

func toolNames(tools []*mcp.Tool) []string {
	names := make([]string, len(tools))
	for i, tool := range tools {
		names[i] = tool.Name
	}
	return names
}

func mapKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// setDiff compares want and got as sets and, if they differ, returns a
// human-readable summary; it returns "" when the sets are equal.
func setDiff(want, got []string) string {
	wantSet := toSet(want)
	gotSet := toSet(got)

	var missing, extra []string
	for k := range wantSet {
		if !gotSet[k] {
			missing = append(missing, k)
		}
	}
	for k := range gotSet {
		if !wantSet[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)

	if len(missing) == 0 && len(extra) == 0 {
		return ""
	}
	var b strings.Builder
	if len(missing) > 0 {
		fmt.Fprintf(&b, "missing %v", missing)
	}
	if len(extra) > 0 {
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "extra %v", extra)
	}
	return b.String()
}

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}
