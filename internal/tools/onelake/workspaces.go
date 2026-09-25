package onelake

import (
	"context"
	"encoding/xml"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

func (a *Area) registerWorkspaces(r *server.Registrar) {
	server.AddTool(r, &mcp.Tool{
		Name: "list-workspaces",
		Description: "Lists all Fabric workspaces accessible via OneLake data plane API. Use this when the user needs " +
			"to view available workspaces or select a workspace for data operations. Returns workspace names and IDs.",
		Annotations: readOnly("List OneLake Workspaces"),
	}, a.listWorkspaces)

	server.AddTool(r, &mcp.Tool{
		Name: "list-items",
		Description: "Lists OneLake items in a Fabric workspace using the high-level OneLake API. Use this when the " +
			"user needs to see what items exist in a workspace. Returns item names, types, and metadata.",
		Annotations: readOnly("List OneLake Items"),
	}, a.listItemsTool)

	server.AddTool(r, &mcp.Tool{
		Name:        "list-items-dfs",
		Description: "List OneLake items in a workspace using the OneLake DFS (Data Lake File System) data API.",
		Annotations: readOnly("List OneLake Items (Data API)"),
	}, a.listItemsDFS)
}

type listWorkspacesInput struct {
	ContinuationToken string `json:"continuation-token,omitempty" jsonschema:"Token for retrieving the next page of results."`
	Format            string `json:"format,omitempty" jsonschema:"Output format for OneLake API responses. Use 'json' for parsed objects, 'xml' for raw XML API response, or 'raw' for unprocessed API response. Supported values: 'json' (default), 'xml', 'raw'."`
}

// workspace ports upstream's Workspace model as the list-workspaces
// parser fills it. OneLake's JSON context writes nulls, so no field here
// is omitempty.
type workspace struct {
	ID                          string               `json:"id"`
	DisplayName                 string               `json:"displayName"`
	Description                 *string              `json:"description"`
	Type                        string               `json:"type"`
	CapacityID                  *string              `json:"capacityId"`
	DefaultDatasetStorageFormat *string              `json:"defaultDatasetStorageFormat"`
	Properties                  *workspaceProperties `json:"properties"`
	Metadata                    *workspaceMetadata   `json:"metadata"`
}

type workspaceProperties struct {
	LastModified *dotnetTime `json:"lastModified"`
}

type workspaceMetadata struct {
	RegionalServiceEndpoint *string `json:"regionalServiceEndpoint"`
	WorkspaceObjectID       *string `json:"workspaceObjectId"`
	WorkspacePortalURL      *string `json:"workspacePortalUrl"`
}

// listWorkspaces ports OneLakeWorkspaceListCommand, which keeps the
// default error mapping.
func (a *Area) listWorkspaces(ctx context.Context, _ *mcp.CallToolRequest, in listWorkspacesInput) (*mcp.CallToolResult, any, error) {
	u := a.c.ep.api + "/?comp=list"
	if in.ContinuationToken != "" {
		u += "&continuationToken=" + url.QueryEscape(in.ContinuationToken)
	}
	b, err := a.c.oneLake(ctx, http.MethodGet, u, nil)
	if err != nil {
		return response.Error(err), nil, nil
	}
	if strings.EqualFold(in.Format, "xml") {
		return response.Success(map[string]any{"workspaces": nil, "xmlResponse": string(b)}), nil, nil
	}
	var doc struct {
		Containers []struct {
			Name       string `xml:"Name"`
			Properties *struct {
				LastModified string `xml:"Last-Modified"`
			} `xml:"Properties"`
			Metadata *struct {
				RegionalServiceEndpoint *string `xml:"RegionalServiceEndpoint"`
				WorkspaceObjectID       *string `xml:"WorkspaceObjectId"`
				WorkspacePortalURL      *string `xml:"WorkspacePortalUrl"`
			} `xml:"Metadata"`
		} `xml:"Containers>Container"`
	}
	if err := xml.Unmarshal(b, &doc); err != nil {
		return response.Error(&opError{msg: "Failed to parse OneLake workspace list response."}), nil, nil
	}
	list := []workspace{}
	for _, c := range doc.Containers {
		w := workspace{ID: c.Name, DisplayName: c.Name, Type: "Workspace"}
		if c.Properties != nil {
			w.Properties = &workspaceProperties{LastModified: parseHTTPTime(c.Properties.LastModified)}
		}
		if m := c.Metadata; m != nil {
			w.Metadata = &workspaceMetadata{m.RegionalServiceEndpoint, m.WorkspaceObjectID, m.WorkspacePortalURL}
		}
		list = append(list, w)
	}
	return response.Success(map[string]any{"workspaces": list, "xmlResponse": nil}), nil, nil
}

type listItemsInput struct {
	WorkspaceID       string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace         string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	ContinuationToken string `json:"continuation-token,omitempty" jsonschema:"Token for retrieving the next page of results."`
}

// listItemsTool ports OneLakeItemListCommand: the raw container listing XML.
func (a *Area) listItemsTool(ctx context.Context, _ *mcp.CallToolRequest, in listItemsInput) (*mcp.CallToolResult, any, error) {
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	if strings.TrimSpace(ws) == "" {
		return response.Fail(http.StatusBadRequest, errWorkspaceRequired), nil, nil
	}
	x, err := a.listItemsXML(ctx, ws, in.ContinuationToken)
	if err != nil {
		return errorResult(err), nil, nil
	}
	return response.Success(map[string]any{"xmlResponse": x}), nil, nil
}

type listItemsDFSInput struct {
	WorkspaceID       string `json:"workspace-id,omitempty" jsonschema:"The ID of the Microsoft Fabric workspace."`
	Workspace         string `json:"workspace,omitempty" jsonschema:"The name or ID of the Microsoft Fabric workspace."`
	Recursive         bool   `json:"recursive,omitempty" jsonschema:"Whether to perform the operation recursively."`
	ContinuationToken string `json:"continuation-token,omitempty" jsonschema:"Token for retrieving the next page of results."`
}

// listItemsDFS ports OneLakeItemDataListCommand: the raw DFS filesystem
// listing JSON.
func (a *Area) listItemsDFS(ctx context.Context, _ *mcp.CallToolRequest, in listItemsDFSInput) (*mcp.CallToolResult, any, error) {
	ws := workspaceOf(in.WorkspaceID, in.Workspace)
	if strings.TrimSpace(ws) == "" {
		return response.Fail(http.StatusBadRequest, errWorkspaceRequired), nil, nil
	}
	j, err := withWorkspaceFallback(ctx, a, ws, func(id string) (string, error) {
		u := a.c.ep.dfs + "/" + id + "?resource=filesystem&recursive=" + strconv.FormatBool(in.Recursive)
		if in.ContinuationToken != "" {
			u += "&continuationToken=" + url.QueryEscape(in.ContinuationToken)
		}
		b, err := a.c.oneLake(ctx, http.MethodGet, u, nil)
		return string(b), err
	})
	if err != nil {
		return errorResult(err), nil, nil
	}
	return response.Success(map[string]any{"jsonResponse": j}), nil, nil
}

// dotnetTime marshals like a .NET DateTime that DateTime.TryParse produced
// from an HTTP date: converted to the machine's local time, with offset.
type dotnetTime time.Time

func (t dotnetTime) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(time.Time(t).Local().Format("2006-01-02T15:04:05.9999999Z07:00"))), nil
}

// parseHTTPTime parses an RFC 1123 date as upstream's DateTime.TryParse
// does, returning nil when it doesn't parse.
func parseHTTPTime(s string) *dotnetTime {
	t, err := http.ParseTime(s)
	if err != nil {
		return nil
	}
	d := dotnetTime(t)
	return &d
}
