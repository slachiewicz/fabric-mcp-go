package auth

import (
	"context"
	"errors"
	"sync"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	sdkauth "github.com/modelcontextprotocol/go-sdk/auth"
)

// OnBehalfOf is upstream's UseOnBehalfOf outgoing strategy: each request's
// incoming bearer token, which the HTTP transport's verifier stores in
// TokenInfo.Extra["token"], is exchanged for a downstream token by the
// server's Entra ID application.
type OnBehalfOf struct {
	TenantID, ClientID, ClientSecret string

	mu    sync.Mutex
	creds map[string]*azidentity.OnBehalfOfCredential // by incoming token
}

// GetToken implements azcore.TokenCredential.
func (o *OnBehalfOf) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	info := sdkauth.TokenInfoFromContext(ctx)
	assertion, _ := func() (string, bool) {
		if info == nil {
			return "", false
		}
		s, ok := info.Extra["token"].(string)
		return s, ok
	}()
	if assertion == "" {
		return azcore.AccessToken{}, &CredentialError{
			Err:         errors.New("on-behalf-of authentication needs an authenticated incoming HTTP request"),
			Unavailable: true,
		}
	}
	cred, err := o.credential(assertion)
	if err != nil {
		return azcore.AccessToken{}, &CredentialError{Err: err}
	}
	tok, err := cred.GetToken(ctx, opts)
	if err != nil {
		return tok, &CredentialError{Err: err}
	}
	return tok, nil
}

func (o *OnBehalfOf) credential(assertion string) (*azidentity.OnBehalfOfCredential, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c, ok := o.creds[assertion]; ok {
		return c, nil
	}
	c, err := azidentity.NewOnBehalfOfCredentialWithSecret(o.TenantID, o.ClientID, assertion, o.ClientSecret, nil)
	if err != nil {
		return nil, err
	}
	if o.creds == nil || len(o.creds) > 1000 {
		o.creds = map[string]*azidentity.OnBehalfOfCredential{} // bound memory; tokens expire anyway
	}
	o.creds[assertion] = c
	return c, nil
}
