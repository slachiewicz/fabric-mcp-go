// Package httpserver serves the MCP server over streamable HTTP, following
// upstream's ServerStartCommand: Entra ID bearer-token authentication with
// OAuth protected resource metadata, or no incoming authentication on a
// loopback address when explicitly requested.
package httpserver

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
)

// Scope and app permission a caller needs, as upstream's "McpAccess" policy.
const (
	requiredScope      = "Mcp.Tools.ReadWrite"
	requiredPermission = "Mcp.Tools.ReadWrite.All"
)

// Config is the Entra ID application the server authenticates callers
// against, read from the AzureAd__* variables upstream binds.
type Config struct {
	Instance string // default https://login.microsoftonline.com
	TenantID string
	ClientID string
}

// ConfigFromEnv reads AzureAd__Instance, AzureAd__TenantId and AzureAd__ClientId.
func ConfigFromEnv() Config {
	return Config{
		Instance: strings.TrimRight(cmp.Or(os.Getenv("AzureAd__Instance"), "https://login.microsoftonline.com"), "/"),
		TenantID: os.Getenv("AzureAd__TenantId"),
		ClientID: os.Getenv("AzureAd__ClientId"),
	}
}

func (c Config) issuer() string { return c.Instance + "/" + c.TenantID + "/v2.0" }

// Address returns the host:port to listen on from ASPNETCORE_URLS. Without
// incoming authentication only loopback, or a wildcard with
// ALLOW_INSECURE_EXTERNAL_BINDING=true, is accepted, as upstream enforces.
func Address(incomingAuth bool) (string, error) {
	raw := os.Getenv("ASPNETCORE_URLS")
	if raw == "" {
		raw = "http://localhost:5000" // ASP.NET Core's default
		if !incomingAuth {
			raw = "http://127.0.0.1:5001"
		}
	}
	if strings.Contains(raw, ";") {
		return "", errors.New("multiple endpoints in ASPNETCORE_URLS are not supported; provide a single URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid URL %q in ASPNETCORE_URLS", raw)
	}
	if u.Scheme != "http" {
		return "", fmt.Errorf("unsupported scheme %q in URL %q", u.Scheme, raw)
	}
	host, port := u.Hostname(), u.Port()
	if port == "" {
		port = "80"
	}
	wildcard := host == "*" || host == "+" || host == "0.0.0.0" || host == "::"
	if wildcard {
		host = ""
	}
	if !incomingAuth {
		ip := net.ParseIP(host)
		loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
		switch {
		case !loopback && !wildcard:
			return "", fmt.Errorf("explicit external binding is not supported for %q", raw)
		case wildcard:
			if ok, _ := strconv.ParseBool(os.Getenv("ALLOW_INSECURE_EXTERNAL_BINDING")); !ok {
				return "", fmt.Errorf("external binding blocked for %q; set ALLOW_INSECURE_EXTERNAL_BINDING=true if you intentionally want to bind beyond loopback", raw)
			}
		}
	}
	return net.JoinHostPort(host, port), nil
}

// Handler returns the HTTP handler for s. With a nil cfg, incoming
// authentication is disabled; otherwise verify checks bearer tokens (see
// Verifier).
func Handler(s *mcp.Server, cfg *Config, verify sdkauth.TokenVerifier) (http.Handler, error) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("Healthy")) })
	mcpHandler := http.Handler(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return s }, nil))
	if cfg == nil {
		mux.Handle("/", mcpHandler)
		return mux, nil
	}
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", func(w http.ResponseWriter, r *http.Request) {
		sdkauth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
			Resource:               baseURL(r),
			AuthorizationServers:   []string{cfg.issuer()},
			ScopesSupported:        []string{cfg.ClientID + "/" + requiredScope},
			BearerMethodsSupported: []string{"header"},
			ResourceDocumentation:  "https://github.com/Microsoft/mcp",
		}).ServeHTTP(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		cw := &challengeWriter{ResponseWriter: w, r: r}
		sdkauth.RequireBearerToken(verify, &sdkauth.RequireBearerTokenOptions{
			Scopes: []string{requiredScope},
		})(mcpHandler).ServeHTTP(cw, r)
	})
	return mux, nil
}

// challengeWriter sets the WWW-Authenticate header upstream's JWT bearer
// handler sends: realm and resource metadata on 401, plus
// error="invalid_token" when a token was presented, and no challenge on a
// 403 for a missing scope.
type challengeWriter struct {
	http.ResponseWriter
	r *http.Request
}

func (c *challengeWriter) WriteHeader(code int) {
	h := c.Header()
	switch code {
	case http.StatusUnauthorized:
		v := fmt.Sprintf(`Bearer realm="%s", resource_metadata="%s"`, c.r.Host, baseURL(c.r)+"/.well-known/oauth-protected-resource")
		if c.r.Header.Get("Authorization") != "" {
			v += `, error="invalid_token"`
		}
		h.Set("WWW-Authenticate", v)
	case http.StatusForbidden:
		h.Del("WWW-Authenticate")
	}
	c.ResponseWriter.WriteHeader(code)
}

// Flush and Unwrap keep streaming responses working through the wrapper.
func (c *challengeWriter) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *challengeWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// baseURL is the scheme and host the caller used. X-Forwarded-Proto is
// honoured only with AZURE_MCP_DANGEROUSLY_ENABLE_FORWARDED_HEADERS=true.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	if ok, _ := strconv.ParseBool(os.Getenv("AZURE_MCP_DANGEROUSLY_ENABLE_FORWARDED_HEADERS")); ok {
		first, _, _ := strings.Cut(r.Header.Get("X-Forwarded-Proto"), ",")
		if p := strings.ToLower(strings.TrimSpace(first)); p == "http" || p == "https" {
			scheme = p
		}
	}
	return scheme + "://" + r.Host
}

// Verifier validates Entra ID v2.0 access tokens issued for cfg.ClientID.
// A token passes the scope check when it carries the Mcp.Tools.ReadWrite
// delegated scope or the Mcp.Tools.ReadWrite.All app permission; the raw
// token is kept in TokenInfo.Extra["token"] for on-behalf-of exchange.
func Verifier(ctx context.Context, cfg Config) (sdkauth.TokenVerifier, error) {
	if cfg.TenantID == "" || cfg.ClientID == "" {
		return nil, errors.New("authenticated HTTP transport needs AzureAd__TenantId and AzureAd__ClientId; " +
			"use --dangerously-disable-http-incoming-auth to run without incoming authentication")
	}
	provider, err := oidc.NewProvider(ctx, cfg.issuer())
	if err != nil {
		return nil, fmt.Errorf("discover %s: %w", cfg.issuer(), err)
	}
	return verifierFor(provider.VerifierContext(ctx, &oidc.Config{SkipClientIDCheck: true}), cfg.ClientID), nil
}

func verifierFor(v *oidc.IDTokenVerifier, clientID string) sdkauth.TokenVerifier {
	return func(ctx context.Context, token string, _ *http.Request) (*sdkauth.TokenInfo, error) {
		tok, err := v.Verify(ctx, token)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", sdkauth.ErrInvalidToken, err)
		}
		if !slices.Contains(tok.Audience, clientID) && !slices.Contains(tok.Audience, "api://"+clientID) {
			return nil, fmt.Errorf("%w: audience %v", sdkauth.ErrInvalidToken, tok.Audience)
		}
		var claims struct {
			Scp   string   `json:"scp"`
			Roles []string `json:"roles"`
			OID   string   `json:"oid"`
		}
		if err := tok.Claims(&claims); err != nil {
			return nil, fmt.Errorf("%w: %v", sdkauth.ErrInvalidToken, err)
		}
		info := &sdkauth.TokenInfo{
			Expiration: tok.Expiry,
			UserID:     cmp.Or(claims.OID, tok.Subject),
			Extra:      map[string]any{"token": token},
		}
		if slices.Contains(strings.Fields(claims.Scp), requiredScope) || slices.Contains(claims.Roles, requiredPermission) {
			info.Scopes = []string{requiredScope}
		}
		return info, nil
	}
}

// Serve listens on addr until ctx is done.
func Serve(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdown)
	}()
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
