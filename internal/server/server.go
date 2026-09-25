// Package server builds the Fabric MCP server and selects which tools it exposes.
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// Version is set at build time with -ldflags "-X .../internal/server.Version=...".
var Version = "dev"

// Tool exposure modes, porting upstream's ModeTypes.
const (
	ModeAll          = "all"          // one tool per command (ModeTypes.All).
	ModeNamespace    = "namespace"    // one proxy tool per area (ModeTypes.NamespaceProxy).
	ModeSingle       = "single"       // one "fabric" tool routing across all areas (ModeTypes.SingleToolProxy).
	ModeConsolidated = "consolidated" // consolidated command groups; Fabric defines none (ModeTypes.ConsolidatedProxy).

	// DefaultMode is upstream's ModeTypes.Default.
	DefaultMode = ModeNamespace
)

// Options mirrors the upstream "server start" options that affect tool selection.
type Options struct {
	Mode       string   // "", "all", "namespace", "single" or "consolidated"; "" means DefaultMode.
	Namespaces []string // expose only these areas, e.g. "docs"
	Tools      []string // expose only these tools, e.g. "docs_list-item-types"
	ReadOnly   bool     // expose only tools annotated read-only

	// DisableElicitation runs destructive tools without asking the user
	// (--dangerously-disable-elicitation).
	DisableElicitation bool

	// proxied marks the hidden server behind the namespace and single mode
	// proxies, which ask for consent themselves.
	proxied bool
}

// Area is one tool namespace, e.g. docs or onelake.
type Area interface {
	Name() string
	Register(r *Registrar)
}

// Describer is implemented by an Area that wants to control the description
// and title of the proxy tool that namespace mode exposes for it (and that
// single mode lists it by), porting the upstream CommandGroup description
// and title each area's Setup.cs declares. An Area that doesn't implement it
// falls back to an empty description and its bare Name() as the title,
// matching upstream's "group.Title ?? namespaceName".
type Describer interface {
	Description() string
	Title() string
}

// Registrar adds an area's tools to the server, applying the Options filters.
type Registrar struct {
	server *mcp.Server
	opts   Options
	area   string
	order  *[]string // tool names in registration order, shared by all areas
}

// Area returns the namespace the registrar is currently registering for.
func (r *Registrar) Area() string { return r.area }

// AddTool registers a typed tool named "<area>_<name>" unless the Options
// filter it out.
//
// Arguments are checked the way upstream's option binder does, before the
// handler runs: missing required options and blank required strings are
// reported as a 400 envelope rather than go-sdk's schema validation error,
// so the handler is registered raw and In is decoded here.
func AddTool[In, Out any](r *Registrar, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	t.Name = r.area + "_" + t.Name
	if !r.include(t) {
		return
	}
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("tool %s: input schema: %v", t.Name, err))
	}
	t.InputSchema = schema
	r.server.AddTool(t, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		if !r.opts.proxied {
			if res := consent(req, t, r.opts.DisableElicitation); res != nil {
				return res, nil
			}
		}
		raw := req.Params.Arguments
		if len(raw) == 0 || string(raw) == "null" {
			raw = json.RawMessage("{}")
		}
		var args map[string]json.RawMessage
		if err := json.Unmarshal(raw, &args); err != nil {
			return response.Fail(http.StatusBadRequest, "Invalid arguments: "+err.Error()), nil
		}
		if res := checkRequired(schema, args); res != nil {
			return res, nil
		}
		var in In
		if err := json.Unmarshal(raw, &in); err != nil {
			return response.Fail(http.StatusBadRequest, "Invalid arguments: "+err.Error()), nil
		}
		res, out, err := h(ctx, req, in)
		if err != nil {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}}}, nil
		}
		if res == nil {
			b, err := json.Marshal(out)
			if err != nil {
				return nil, err
			}
			res = &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}}
		}
		return res, nil
	})
	if r.order != nil {
		*r.order = append(*r.order, t.Name)
	}
}

// checkRequired ports upstream's required-option validation: first every
// missing option, else the first blank required string.
func checkRequired(schema *jsonschema.Schema, args map[string]json.RawMessage) *mcp.CallToolResult {
	var missing []string
	for _, name := range schema.Required {
		if v, ok := args[name]; !ok || string(v) == "null" {
			missing = append(missing, "--"+name)
		}
	}
	if len(missing) > 0 {
		return response.Fail(http.StatusBadRequest, "Missing Required options: "+strings.Join(missing, ", "))
	}
	for _, name := range schema.Required {
		var s string
		if json.Unmarshal(args[name], &s) == nil && strings.TrimSpace(s) == "" {
			return response.Fail(http.StatusBadRequest, "Option '--"+name+
				"' was configured to require non-empty, non-whitespace values but one or more empty or whitespace values were provided.")
		}
	}
	return nil
}

func (r *Registrar) include(t *mcp.Tool) bool {
	if len(r.opts.Namespaces) > 0 && !slices.Contains(r.opts.Namespaces, r.area) {
		return false
	}
	if len(r.opts.Tools) > 0 && !slices.Contains(r.opts.Tools, t.Name) {
		return false
	}
	if r.opts.ReadOnly && (t.Annotations == nil || !t.Annotations.ReadOnlyHint) {
		return false
	}
	return true
}

// New returns a server exposing the given areas per opts.Mode. ctx bounds
// the lifetime of the in-memory connection namespace and single mode use to
// route calls to a hidden, fully-populated inner server (see proxy.go); it's
// typically the process's root context, so that connection is torn down
// together with the outer transport when ctx is canceled.
func New(ctx context.Context, opts Options, areas ...Area) (*mcp.Server, error) {
	if len(opts.Tools) > 0 && len(opts.Namespaces) > 0 {
		return nil, fmt.Errorf("--tool can't be used together with --namespace")
	}

	mode := opts.Mode
	if len(opts.Tools) > 0 {
		// Upstream's PostBindOptions: --tool always runs in "all" mode.
		mode = ModeAll
	}
	if mode == "" {
		mode = DefaultMode
	}
	switch mode {
	case ModeAll, ModeNamespace, ModeSingle, ModeConsolidated:
	default:
		return nil, fmt.Errorf("invalid mode %q; valid modes are %q, %q, %q, %q",
			mode, ModeNamespace, ModeSingle, ModeAll, ModeConsolidated)
	}
	opts.Mode = mode

	switch mode {
	case ModeAll:
		s, _ := buildInnerServer(opts, areas)
		return s, nil
	case ModeConsolidated:
		// Upstream builds this mode from consolidated command groups, and
		// Fabric declares none (see internal/server/testdata/mode-consolidated.json),
		// so it always exposes zero tools regardless of areas or filters.
		return mcp.NewServer(implementation(), nil), nil
	case ModeNamespace:
		return newNamespaceServer(ctx, opts, areas)
	case ModeSingle:
		return newSingleServer(ctx, opts, areas)
	default:
		panic("unreachable")
	}
}

func implementation() *mcp.Implementation {
	return &mcp.Implementation{Name: "fabric-mcp-go", Title: "Fabric MCP Server", Version: Version}
}

// buildInnerServer registers every area's tools directly on a fresh server,
// applying the Options filters - the behavior of mode "all", and also the
// hidden server that namespace and single mode proxy through.
func buildInnerServer(opts Options, areas []Area) (*mcp.Server, []string) {
	s := mcp.NewServer(implementation(), nil)
	var order []string
	for _, a := range areas {
		a.Register(&Registrar{server: s, opts: opts, area: a.Name(), order: &order})
	}
	return s, order
}

// describe returns the description and title an area's namespace/single mode
// proxy tool should carry, via Describer if the area implements it.
func describe(a Area) (description, title string) {
	title = a.Name()
	d, ok := a.(Describer)
	if !ok {
		return "", title
	}
	description = d.Description()
	if t := d.Title(); t != "" {
		title = t
	}
	return description, title
}
