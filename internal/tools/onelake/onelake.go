// Package onelake ports the upstream Fabric.Mcp.Tools.OneLake area:
// workspaces and items, files, tables, data access roles, shortcuts and
// settings over the OneLake data plane and the Fabric REST API.
//
// Each tool group lives in its own file and registers itself from Register.
package onelake

import (
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// Area is the "onelake" tool namespace.
type Area struct {
	c     *client
	items itemCache
}

// New returns the OneLake area authenticating with cred.
func New(cred azcore.TokenCredential) *Area {
	return &Area{c: &client{
		cred:     cred,
		http:     &http.Client{Timeout: 100 * time.Second}, // .NET HttpClient's default
		ep:       defaultEndpoints,
		lroDelay: 5 * time.Second,
	}}
}

// Description implements server.Describer, porting FabricOneLakeSetup's
// CommandGroup description.
func (*Area) Description() string {
	return "Microsoft Fabric OneLake Operations - Manage and interact with OneLake data lake storage. " +
		"OneLake is Microsoft Fabric's built-in data lake that provides unified storage for all " +
		"analytics workloads. Use this tool when you need to:\n" +
		"- Manage OneLake folders and files\n" +
		"- Configure data access and permissions\n" +
		"- Monitor OneLake storage usage and performance\n" +
		"- Integrate with other Fabric workloads through OneLake\n" +
		"This tool provides operations for working with OneLake resources within your Fabric tenant."
}

// Title implements server.Describer; the upstream group has no title.
func (*Area) Title() string { return "" }

// Name implements server.Area.
func (*Area) Name() string { return "onelake" }

// Register implements server.Area.
func (a *Area) Register(r *server.Registrar) {
	a.registerWorkspaces(r)
	a.registerFiles(r)
	a.registerTables(r)
	a.registerSecurity(r)
	a.registerShortcuts(r)
	a.registerSettings(r)
}

func boolPtr(b bool) *bool { return &b }

// readOnly and write are the annotation sets upstream's OneLake commands
// use; every command is closed-world.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, ReadOnlyHint: true, IdempotentHint: true,
		DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false)}
}

func write(title string, destructive, idempotent bool) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{Title: title, IdempotentHint: idempotent,
		DestructiveHint: boolPtr(destructive), OpenWorldHint: boolPtr(false)}
}

// workspaceOf returns --workspace-id, else --workspace, as upstream's
// commands do.
func workspaceOf(id, nameOrID string) string {
	if id != "" {
		return id
	}
	return nameOrID
}

// Validation messages upstream's commands share.
const (
	errWorkspaceRequired = "Workspace identifier is required. Provide --workspace or --workspace-id."
	errItemRequired      = "Item identifier is required. Provide --item or --item-id."
)

// nonEmptyPtr returns nil for "", else &s.
func nonEmptyPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
