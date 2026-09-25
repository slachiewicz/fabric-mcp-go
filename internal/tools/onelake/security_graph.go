package onelake

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// graphScope is the Microsoft Graph OAuth scope used to resolve non-GUID
// data access role members, ported from OneLakeService's inline constant.
const graphScope = "https://graph.microsoft.com/.default"

// graphBaseURL is a var, not a const, so unit tests can point it at a fake
// server; production code never reassigns it.
var graphBaseURL = "https://graph.microsoft.com/v1.0"

// fabricDelete sends a Fabric REST DELETE request without polling for a
// long-running operation, ported from SendFabricApiDeleteRequestAsync
// (unlike client.fabric, which polls a 202's Location header).
func (c *client) fabricDelete(ctx context.Context, u string) error {
	req, err := c.newRequest(ctx, http.MethodDelete, u, auth.FabricScope, nil)
	if err != nil {
		return err
	}
	resp, err := c.send(req)
	if err != nil {
		return err
	}
	return resp.Body.Close()
}

// graphGet sends a Graph-scoped GET request and returns the body, ported
// from the inline HttpClient calls in TryResolveUserAsync /
// TryResolveGroupByMailAsync.
func (c *client) graphGet(ctx context.Context, u string) ([]byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, u, graphScope, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.send(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

// tenantIDFromToken ports GetTenantIdFromTokenAsync: best-effort extraction
// of the "tid" claim from the Fabric access token's JWT payload. Failures
// are swallowed, as upstream does, since the caller falls back to leaving
// tenantId unset.
func (c *client) tenantIDFromToken(ctx context.Context) string {
	tok, err := c.token(ctx, auth.FabricScope)
	if err != nil {
		return ""
	}
	parts := strings.Split(tok, ".")
	if len(parts) < 2 {
		return ""
	}
	b, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var claims struct {
		Tid string `json:"tid"`
	}
	if err := json.Unmarshal(b, &claims); err != nil {
		return ""
	}
	return claims.Tid
}

// resolvePrincipals ports ResolvePrincipalsAsync: resolves non-GUID
// objectId values (email/UPN) to Entra object IDs via Microsoft Graph,
// trying /users first, then /groups by mail. GUID and empty objectIds are
// left untouched.
func (a *Area) resolvePrincipals(ctx context.Context, members []microsoftEntraMember) error {
	var errs []string
	for i := range members {
		m := &members[i]
		if m.ObjectID == nil || strings.TrimSpace(*m.ObjectID) == "" || isGUID(*m.ObjectID) {
			continue
		}
		principal := strings.TrimSpace(*m.ObjectID)
		id, typ, err := a.resolveGraphUser(ctx, principal)
		if err != nil {
			return err
		}
		if id == "" {
			id, typ, err = a.resolveGraphGroupByMail(ctx, principal)
			if err != nil {
				return err
			}
		}
		if id == "" {
			errs = append(errs, fmt.Sprintf(
				"No Entra principal matched '%s'. Ensure the email/UPN is correct and you have User.Read.All and GroupMember.Read.All permissions.",
				principal))
			continue
		}
		m.ObjectID = &id
		if m.ObjectType == nil {
			m.ObjectType = &typ
		}
	}
	if len(errs) > 0 {
		return &secArgError{msg: "Failed to resolve one or more principals:\n" + strings.Join(errs, "\n")}
	}
	return nil
}

// resolveGraphUser ports TryResolveUserAsync. Any HTTP-level failure
// (including 404) is treated as "no match", matching upstream's blanket
// catch(HttpRequestException); only a decode failure propagates.
func (a *Area) resolveGraphUser(ctx context.Context, principal string) (id, typ string, err error) {
	u := graphBaseURL + "/users/" + url.PathEscape(principal) + "?$select=id"
	b, err := a.c.graphGet(ctx, u)
	if err != nil {
		var httpErr *response.HTTPError
		if errors.As(err, &httpErr) {
			return "", "", nil
		}
		return "", "", err
	}
	var doc struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", "", err
	}
	if doc.ID == "" {
		return "", "", nil
	}
	return doc.ID, "User", nil
}

// resolveGraphGroupByMail ports TryResolveGroupByMailAsync.
func (a *Area) resolveGraphGroupByMail(ctx context.Context, mail string) (id, typ string, err error) {
	filter := url.QueryEscape(fmt.Sprintf("mail eq '%s'", mail))
	u := graphBaseURL + "/groups?$filter=" + filter + "&$select=id,displayName&$top=1"
	b, err := a.c.graphGet(ctx, u)
	if err != nil {
		var httpErr *response.HTTPError
		if errors.As(err, &httpErr) {
			return "", "", nil
		}
		return "", "", err
	}
	var doc struct {
		Value []struct {
			ID string `json:"id"`
		} `json:"value"`
	}
	if err := json.Unmarshal(b, &doc); err != nil {
		return "", "", err
	}
	if len(doc.Value) == 0 || doc.Value[0].ID == "" {
		return "", "", nil
	}
	return doc.Value[0].ID, "Group", nil
}
