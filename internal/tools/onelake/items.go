package onelake

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/google/uuid"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// itemCache maps workspace -> lower-cased name, ID or artifact ID -> item
// identifier, like upstream's _itemIdentifierCache.
type itemCache struct {
	mu sync.Mutex
	m  map[string]map[string]string
}

func (c *itemCache) get(ws, key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[strings.ToLower(ws)][strings.ToLower(key)]
	return v, ok
}

func (c *itemCache) add(ws, key, id string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]map[string]string{}
	}
	w := c.m[strings.ToLower(ws)]
	if w == nil {
		w = map[string]string{}
		c.m[strings.ToLower(ws)] = w
	}
	if _, ok := w[strings.ToLower(key)]; !ok {
		w[strings.ToLower(key)] = id
	}
}

func isGUID(s string) bool { _, err := uuid.Parse(s); return err == nil && len(s) >= 32 }

func normalizeWorkspace(ws string) (string, error) {
	if strings.TrimSpace(ws) == "" {
		return "", &argError{msg: "Workspace identifier is required. (Parameter 'workspaceId')"}
	}
	return strings.TrimSpace(ws), nil
}

func normalizeItem(item string) (string, error) {
	if strings.TrimSpace(item) == "" {
		return "", &argError{msg: "Item identifier is required. (Parameter 'itemIdentifier')"}
	}
	return strings.TrimRight(strings.TrimSpace(item), "/"), nil
}

// workspaceAndItem normalizes a workspace and resolves an item name to its
// identifier, ported from GetNormalizedIdentifiersAsync.
func (a *Area) workspaceAndItem(ctx context.Context, ws, item string) (string, string, error) {
	ws, err := normalizeWorkspace(ws)
	if err != nil {
		return "", "", err
	}
	id, err := a.resolveItem(ctx, ws, item)
	return ws, id, err
}

// resolveItem ports ResolveItemIdentifierAsync: GUIDs and "Name.Type"
// identifiers pass through; bare names are looked up in the workspace's
// item listing.
func (a *Area) resolveItem(ctx context.Context, ws, item string) (string, error) {
	ws, err := normalizeWorkspace(ws)
	if err != nil {
		return "", err
	}
	in, err := normalizeItem(item)
	if err != nil {
		return "", err
	}
	if isGUID(in) {
		return in, nil
	}
	if strings.Contains(in, ".") {
		return strings.TrimRight(in, "/"), nil
	}
	if id, ok := a.items.get(ws, in); ok {
		return id, nil
	}
	items, err := a.listItems(ctx, ws, "")
	if err != nil {
		return "", err
	}
	for _, it := range items {
		if strings.TrimSpace(it.ID) == "" {
			continue
		}
		id := strings.TrimRight(strings.TrimSpace(it.ID), "/")
		a.items.add(ws, id, id)
		if n := strings.TrimSpace(it.DisplayName); n != "" {
			a.items.add(ws, n, id)
		}
		if it.ArtifactID != "" {
			a.items.add(ws, strings.TrimSpace(it.ArtifactID), id)
		}
	}
	if id, ok := a.items.get(ws, in); ok {
		return id, nil
	}
	return "", &opError{msg: "Unable to resolve item '" + item + "' in workspace '" + ws +
		"'. Provide the full item identifier including its suffix, for example 'ItemName.Lakehouse'."}
}

// listedItem is the part of upstream's OneLakeItem that resolution uses.
type listedItem struct {
	ID, DisplayName, ArtifactID string
}

// listItems ports the parsing half of ListOneLakeItemsAsync: the container
// listing's BlobPrefix entries ("Name.Type/"), falling back to Blob entries.
func (a *Area) listItems(ctx context.Context, ws, continuation string) ([]listedItem, error) {
	body, err := a.listItemsXML(ctx, ws, continuation)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Prefixes []struct {
			Name     string `xml:"Name"`
			Metadata struct {
				ArtifactID string `xml:"ArtifactId"`
			} `xml:"Metadata"`
		} `xml:"Blobs>BlobPrefix"`
		Blobs []struct {
			Name string `xml:"Name"`
		} `xml:"Blobs>Blob"`
	}
	if err := xml.Unmarshal([]byte(body), &doc); err != nil {
		return nil, &opError{msg: "Failed to parse OneLake items list response: " + err.Error()}
	}
	var items []listedItem
	for _, p := range doc.Prefixes {
		name := strings.TrimRight(p.Name, "/")
		items = append(items, listedItem{ID: name, DisplayName: itemDisplayName(name), ArtifactID: p.Metadata.ArtifactID})
	}
	if len(items) == 0 {
		for _, b := range doc.Blobs {
			items = append(items, listedItem{ID: b.Name, DisplayName: b.Name})
		}
	}
	return items, nil
}

// itemDisplayName strips the type suffix from "sales.Lakehouse".
func itemDisplayName(full string) string {
	if i := strings.LastIndex(full, "."); i > 0 && i < len(full)-1 {
		return full[:i]
	}
	return full
}

// listItemsXML returns the raw container listing of a workspace, ported
// from ListOneLakeItemsXmlAsync.
func (a *Area) listItemsXML(ctx context.Context, ws, continuation string) (string, error) {
	return withWorkspaceFallback(ctx, a, ws, func(id string) (string, error) {
		u := a.c.ep.api + "/" + id + "?delimiter=/&restype=container&comp=list"
		if continuation != "" {
			u += "&continuationToken=" + url.QueryEscape(continuation)
		}
		b, err := a.c.oneLake(ctx, http.MethodGet, u, nil)
		return string(b), err
	})
}

// withWorkspaceFallback ports ExecuteWithWorkspaceFallbackAsync: when a
// workspace GUID is rejected with 404 or 400, retry with the workspace's
// display name.
func withWorkspaceFallback[T any](ctx context.Context, a *Area, ws string, op func(string) (T, error)) (T, error) {
	res, err := op(ws)
	if err == nil || !isGUID(ws) {
		return res, err
	}
	var httpErr *response.HTTPError
	if !errors.As(err, &httpErr) || (httpErr.Status != http.StatusNotFound && httpErr.Status != http.StatusBadRequest) {
		return res, err
	}
	b, werr := a.c.fabric(ctx, http.MethodGet, a.c.ep.fabric+"/workspaces/"+ws, nil)
	if werr != nil {
		return res, werr
	}
	var w struct {
		DisplayName string `json:"displayName"`
	}
	_ = json.Unmarshal(b, &w)
	if strings.TrimSpace(w.DisplayName) == "" || strings.EqualFold(w.DisplayName, ws) {
		return res, err
	}
	return op(w.DisplayName)
}
