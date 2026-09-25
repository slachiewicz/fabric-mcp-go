// Package server builds the Fabric MCP server and selects which tools it exposes.
package server

import (
	"context"
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
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
}

// Area returns the namespace the registrar is currently registering for.
func (r *Registrar) Area() string { return r.area }

// AddTool registers a typed tool named "<area>_<name>" unless the Options filter it out.
func AddTool[In, Out any](r *Registrar, t *mcp.Tool, h mcp.ToolHandlerFor[In, Out]) {
	t.Name = r.area + "_" + t.Name
	if !r.include(t) {
		return
	}
	mcp.AddTool(r.server, t, h)
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
		return buildInnerServer(opts, areas), nil
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
func buildInnerServer(opts Options, areas []Area) *mcp.Server {
	s := mcp.NewServer(implementation(), nil)
	for _, a := range areas {
		a.Register(&Registrar{server: s, opts: opts, area: a.Name()})
	}
	return s
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
