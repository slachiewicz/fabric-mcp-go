//go:build live

package httpserver_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/httpserver"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/core"
	"github.com/slachiewicz/fabric-mcp-go/internal/tools/onelake"
)

type bearer string

func (b bearer) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+string(b))
	return http.DefaultTransport.RoundTrip(r)
}

// TestLiveHTTPOnBehalfOf serves the core and onelake areas over
// authenticated HTTP with on-behalf-of outgoing auth, and calls them with a
// caller token for the Entra app in AzureAd__TenantId/ClientId/ClientSecret,
// taken from the signed-in Azure CLI (which the app must pre-authorize):
//
//	set -a; . ~/.config/fabric-mcp-go/e2e-http.env; set +a
//	go test -tags live ./internal/httpserver/ -run Live -v
func TestLiveHTTPOnBehalfOf(t *testing.T) {
	cfg := httpserver.ConfigFromEnv()
	secret := os.Getenv("AzureAd__ClientSecret")
	if cfg.TenantID == "" || cfg.ClientID == "" || secret == "" {
		t.Skip("AzureAd__TenantId, AzureAd__ClientId and AzureAd__ClientSecret not set")
	}
	ctx := context.Background()
	cli, err := azidentity.NewAzureCLICredential(&azidentity.AzureCLICredentialOptions{TenantID: cfg.TenantID})
	if err != nil {
		t.Fatal(err)
	}
	callerToken, err := cli.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{"api://" + cfg.ClientID + "/.default"}})
	if err != nil {
		t.Fatalf("caller token: %v", err)
	}

	obo := &auth.OnBehalfOf{TenantID: cfg.TenantID, ClientID: cfg.ClientID, ClientSecret: secret}
	client, err := fabric.NewClient(obo, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	s, err := server.New(ctx, server.Options{Mode: server.ModeAll}, core.New(client), onelake.New(obo))
	if err != nil {
		t.Fatal(err)
	}
	verify, err := httpserver.Verifier(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	h, err := httpserver.Handler(s, &cfg, verify)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(h)
	defer srv.Close()

	// Another resource's token is rejected.
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "live"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer("not-a-token")}}, nil)
	if err == nil {
		_ = cs.Close()
		t.Error("connect with an invalid token succeeded")
	}

	cs, err = mcp.NewClient(&mcp.Implementation{Name: "live"}, nil).Connect(ctx,
		&mcp.StreamableClientTransport{Endpoint: srv.URL, HTTPClient: &http.Client{Transport: bearer(callerToken.Token)}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cs.Close() }()
	// One call per outgoing scope: Fabric REST and OneLake storage.
	for _, tool := range []string{"core_search-catalog", "onelake_list-workspaces"} {
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: map[string]any{}})
		if err != nil {
			t.Fatalf("%s: %v", tool, err)
		}
		text := res.Content[0].(*mcp.TextContent).Text
		if res.IsError || !strings.HasPrefix(text, `{"status":200`) {
			t.Errorf("%s: %.300s", tool, text)
		}
	}
}
