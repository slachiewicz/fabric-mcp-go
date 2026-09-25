package httpserver

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	testIssuer = "https://login.example/tenant/v2.0"
	testClient = "11111111-2222-3333-4444-555555555555"
)

func signer(t *testing.T) (*rsa.PrivateKey, func(claims map[string]any) string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	sig, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: key}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return key, func(claims map[string]any) string {
		c := map[string]any{"iss": testIssuer, "aud": testClient, "exp": time.Now().Add(time.Hour).Unix(), "oid": "user-1"}
		for k, v := range claims {
			c[k] = v
		}
		b, _ := json.Marshal(c)
		jws, err := sig.Sign(b)
		if err != nil {
			t.Fatal(err)
		}
		s, _ := jws.CompactSerialize()
		return s
	}
}

// whoami reports whether the tool handler's context carries the caller's token.
func testServer() *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{Name: "t"}, nil)
	mcp.AddTool(s, &mcp.Tool{Name: "whoami"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		info := sdkauth.TokenInfoFromContext(ctx)
		tok, _ := info.Extra["token"].(string)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: info.UserID + " " + tok[:10]}}}, nil, nil
	})
	return s
}

type bearer struct{ token string }

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func TestAuthenticated(t *testing.T) {
	key, sign := signer(t)
	v := oidc.NewVerifier(testIssuer, &oidc.StaticKeySet{PublicKeys: []crypto.PublicKey{&key.PublicKey}}, &oidc.Config{SkipClientIDCheck: true})
	h, err := Handler(testServer(), &Config{Instance: "https://login.example", TenantID: "tenant", ClientID: testClient}, verifierFor(v, testClient))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// No token: 401 pointing at the protected resource metadata.
	resp, err := http.Post(srv.URL+"/", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), srv.URL+"/.well-known/oauth-protected-resource") {
		t.Errorf("no token: %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}

	resp, err = http.Get(srv.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	var meta map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&meta)
	_ = resp.Body.Close()
	if meta["resource"] != srv.URL || jsonOf(meta["authorization_servers"]) != `["https://login.example/tenant/v2.0"]` ||
		jsonOf(meta["scopes_supported"]) != `["`+testClient+`/Mcp.Tools.ReadWrite"]` {
		t.Errorf("metadata = %v", meta)
	}

	// A valid token without the scope or app permission is forbidden.
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer "+sign(map[string]any{"scp": "Other.Scope"}))
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("missing scope: status %d", resp.StatusCode)
	}

	// With the app permission the tool runs and sees the caller's token.
	for _, claims := range []map[string]any{{"scp": "Mcp.Tools.ReadWrite"}, {"roles": []string{"Mcp.Tools.ReadWrite.All"}, "aud": "api://" + testClient}} {
		tok := sign(claims)
		c := mcp.NewClient(&mcp.Implementation{Name: "c"}, nil)
		cs, err := c.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/", HTTPClient: &http.Client{Transport: bearer{tok}}}, nil)
		if err != nil {
			t.Fatalf("connect with %v: %v", claims, err)
		}
		res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: "whoami"})
		if err != nil {
			t.Fatal(err)
		}
		if got := res.Content[0].(*mcp.TextContent).Text; got != "user-1 "+tok[:10] {
			t.Errorf("whoami = %q", got)
		}
		_ = cs.Close()
	}
}

func TestUnauthenticatedAndHealth(t *testing.T) {
	h, err := Handler(mcp.NewServer(&mcp.Implementation{Name: "t"}, nil), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health: %d", resp.StatusCode)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "c"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: srv.URL + "/"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = cs.Close()
}

func TestAddress(t *testing.T) {
	tests := []struct {
		urls, insecure string
		auth           bool
		want, err      string
	}{
		{"", "", false, "127.0.0.1:5001", ""},
		{"", "", true, "localhost:5000", ""},
		{"http://localhost:8080", "", false, "localhost:8080", ""},
		{"http://0.0.0.0:8080", "", false, "", "external binding blocked"},
		{"http://0.0.0.0:8080", "true", false, ":8080", ""},
		{"http://10.0.0.5:8080", "", false, "", "explicit external binding is not supported"},
		{"http://10.0.0.5:8080", "", true, "10.0.0.5:8080", ""},
		{"http://a:1;http://b:2", "", true, "", "multiple endpoints"},
		{"https://localhost:8443", "", false, "", "unsupported scheme"},
	}
	for _, tt := range tests {
		t.Setenv("ASPNETCORE_URLS", tt.urls)
		t.Setenv("ALLOW_INSECURE_EXTERNAL_BINDING", tt.insecure)
		got, err := Address(tt.auth)
		if got != tt.want || (err == nil) != (tt.err == "") || (err != nil && !strings.Contains(err.Error(), tt.err)) {
			t.Errorf("Address(%q, auth=%v) = %q, %v; want %q, %q", tt.urls, tt.auth, got, err, tt.want, tt.err)
		}
	}
}

func jsonOf(v any) string { b, _ := json.Marshal(v); return string(b) }
