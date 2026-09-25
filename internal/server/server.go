// Package server builds the Fabric MCP server and selects which tools it exposes.
package server

import (
	"fmt"
	"slices"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is set at build time with -ldflags "-X .../internal/server.Version=...".
var Version = "dev"

// Options mirrors the upstream "server start" options that affect tool selection.
type Options struct {
	Mode       string   // only "all" is implemented so far
	Namespaces []string // expose only these areas, e.g. "docs"
	Tools      []string // expose only these tools, e.g. "docs_list-item-types"
	ReadOnly   bool     // expose only tools annotated read-only
}

// Area is one tool namespace, e.g. docs or onelake.
type Area interface {
	Name() string
	Register(r *Registrar)
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

// New returns a server exposing the given areas.
func New(opts Options, areas ...Area) (*mcp.Server, error) {
	switch opts.Mode {
	case "", "all":
	default:
		return nil, fmt.Errorf("mode %q is not implemented yet; use \"all\"", opts.Mode)
	}
	if len(opts.Tools) > 0 && len(opts.Namespaces) > 0 {
		return nil, fmt.Errorf("--tool can't be used together with --namespace")
	}
	s := mcp.NewServer(&mcp.Implementation{Name: "fabric-mcp-go", Title: "Fabric MCP Server", Version: Version}, nil)
	for _, a := range areas {
		a.Register(&Registrar{server: s, opts: opts, area: a.Name()})
	}
	return s, nil
}
