// Package docs ports the upstream Fabric.Mcp.Tools.Docs area: API specs,
// item definitions, best practices and examples served from embedded files.
package docs

import "github.com/slachiewicz/fabric-mcp-go/internal/server"

// Area is the "docs" tool namespace.
type Area struct{}

// New returns the docs area.
func New() *Area { return &Area{} }

// Name implements server.Area.
func (*Area) Name() string { return "docs" }

// Register implements server.Area.
func (*Area) Register(r *server.Registrar) {
	// TODO(phase 1): list-item-types, item-api-spec, platform-api-spec,
	// item-definitions, best-practices, api-examples.
}
