// Package auth builds the Azure credential the Fabric tools use, following
// upstream's CustomChainedCredential: the default Azure credential chain,
// then interactive browser sign-in unless AZURE_TOKEN_CREDENTIALS pins a
// specific credential.
package auth

import (
	"context"
	"errors"
	"os"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
)

// OAuth scopes for the Fabric REST API and OneLake storage endpoints.
const (
	FabricScope  = "https://api.fabric.microsoft.com/.default"
	StorageScope = "https://storage.azure.com/.default"
)

// NewCredential returns the credential chain. Tokens are acquired lazily,
// so this succeeds without a signed-in user.
func NewCredential() (azcore.TokenCredential, error) {
	tenant := os.Getenv("AZURE_TENANT_ID")
	mode := strings.ToLower(os.Getenv("AZURE_TOKEN_CREDENTIALS"))

	browser, err := azidentity.NewInteractiveBrowserCredential(&azidentity.InteractiveBrowserCredentialOptions{TenantID: tenant})
	if err != nil {
		return nil, err
	}
	if mode == "interactivebrowsercredential" {
		return tagged{browser}, nil
	}
	def, err := azidentity.NewDefaultAzureCredential(&azidentity.DefaultAzureCredentialOptions{TenantID: tenant})
	if err != nil {
		return nil, err
	}
	if pinned := mode != "" && mode != "dev"; pinned {
		return tagged{def}, nil
	}
	chain, err := azidentity.NewChainedTokenCredential([]azcore.TokenCredential{def, browser}, nil)
	if err != nil {
		return nil, err
	}
	return tagged{chain}, nil
}

// CredentialError is a failure to acquire a token. Unavailable is true when
// no credential in the chain could even attempt it, e.g. nobody is signed in.
type CredentialError struct {
	Err         error
	Unavailable bool
}

func (e *CredentialError) Error() string { return e.Err.Error() }
func (e *CredentialError) Unwrap() error { return e.Err }

// tagged wraps token errors in *CredentialError so callers can tell them
// apart from API errors.
type tagged struct{ azcore.TokenCredential }

func (t tagged) GetToken(ctx context.Context, opts policy.TokenRequestOptions) (azcore.AccessToken, error) {
	tok, err := t.TokenCredential.GetToken(ctx, opts)
	if err != nil {
		var failed *azidentity.AuthenticationFailedError
		err = &CredentialError{Err: err, Unavailable: !errors.As(err, &failed)}
	}
	return tok, err
}
