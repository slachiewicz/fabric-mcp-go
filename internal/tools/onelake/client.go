package onelake

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// endpoints are the base URLs the area calls; tests point them at a fake.
type endpoints struct {
	fabric string // Fabric REST API, e.g. https://api.fabric.microsoft.com/v1
	api    string // OneLake data plane (blob flavour)
	dfs    string // OneLake DFS
	blob   string // OneLake blob
	table  string // OneLake table API
}

var defaultEndpoints = endpoints{
	fabric: "https://api.fabric.microsoft.com/v1",
	api:    "https://api.onelake.fabric.microsoft.com",
	dfs:    "https://onelake.dfs.fabric.microsoft.com",
	blob:   "https://onelake.blob.fabric.microsoft.com",
	table:  "https://onelake.table.fabric.microsoft.com",
}

const (
	userAgent      = "OneLake MCP"
	storageVersion = "2023-11-03"
)

// client sends the three kinds of request upstream's OneLakeService makes:
// Fabric REST (Fabric scope, long-running operations polled), OneLake API
// and data plane (storage scope, x-ms-version).
type client struct {
	cred azcore.TokenCredential
	http *http.Client
	ep   endpoints
	// lroDelay is the first polling delay; upstream waits 5s.
	lroDelay time.Duration
}

func (c *client) token(ctx context.Context, scope string) (string, error) {
	tok, err := c.cred.GetToken(ctx, policy.TokenRequestOptions{Scopes: []string{scope}})
	return tok.Token, err
}

func (c *client) newRequest(ctx context.Context, method, url, scope string, body []byte) (*http.Request, error) {
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, r)
	if err != nil {
		return nil, err
	}
	tok, err := c.token(ctx, scope)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("User-Agent", userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}
	return req, nil
}

// fabric sends a Fabric REST request and returns the response body. A 202
// with a Location header is polled to completion, like upstream's
// SendFabricApiRequestAsync; a 202 without one yields an empty body.
func (c *client) fabric(ctx context.Context, method, url string, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	req, err := c.newRequest(ctx, method, url, auth.FabricScope, payload)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusAccepted {
		if loc := resp.Header.Get("Location"); loc != "" {
			return c.pollLRO(ctx, loc)
		}
		return nil, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, &response.HTTPError{
			Status: resp.StatusCode,
			Msg: fmt.Sprintf("Response status code does not indicate success: %d (%s). Error details: %s",
				resp.StatusCode, reason(resp), b),
		}
	}
	return b, nil
}

// pollLRO ports PollFabricLroAsync: poll every Retry-After seconds (5s
// first), up to 120 times, then GET the result URL on success.
func (c *client) pollLRO(ctx context.Context, url string) ([]byte, error) {
	delay := c.lroDelay
	for range 120 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
		req, err := c.newRequest(ctx, http.MethodGet, url, auth.FabricScope, nil)
		if err != nil {
			return nil, err
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return nil, err
		}
		b, err := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if err != nil {
			return nil, err
		}
		if resp.StatusCode < 200 || resp.StatusCode > 299 {
			return nil, &response.HTTPError{
				Status: resp.StatusCode,
				Msg:    fmt.Sprintf("LRO polling failed: %d (%s). %s", resp.StatusCode, reason(resp), b),
			}
		}
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil {
			delay = time.Duration(max(1, s)) * time.Second
		}
		var state struct {
			Status string `json:"status"`
			Error  *struct {
				ErrorCode string `json:"errorCode"`
				Message   string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &state)
		switch state.Status {
		case "Succeeded":
			if loc := resp.Header.Get("Location"); loc != "" {
				return c.lroResult(ctx, loc)
			}
			return nil, nil
		case "Failed":
			code, msg := "Unknown", "Long running operation failed."
			if state.Error != nil {
				code, msg = cmp.Or(state.Error.ErrorCode, code), cmp.Or(state.Error.Message, msg)
			}
			return nil, &response.HTTPError{
				Status: http.StatusInternalServerError,
				Msg:    fmt.Sprintf("Long running operation failed (%s): %s", code, msg),
			}
		}
	}
	return nil, &response.HTTPError{Status: http.StatusRequestTimeout, Msg: "Long running operation timed out after maximum polling attempts."}
}

// lroResult GETs a finished operation's result; upstream ignores failures here.
func (c *client) lroResult(ctx context.Context, url string) ([]byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, url, auth.FabricScope, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, nil
	}
	return io.ReadAll(resp.Body)
}

// oneLake sends a storage-scoped request to the OneLake API and returns the
// body, ported from SendOneLakeApiRequestAsync.
func (c *client) oneLake(ctx context.Context, method, url string, body any) ([]byte, error) {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return nil, err
		}
	}
	req, err := c.newRequest(ctx, method, url, auth.StorageScope, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-ms-version", storageVersion)
	resp, err := c.send(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	return io.ReadAll(resp.Body)
}

// dataPlane sends a storage-scoped data plane request, ported from
// SendDataPlaneRequestAsync. The caller closes the response body.
//
//nolint:unused // used by the files tools
func (c *client) dataPlane(ctx context.Context, method, url string, body io.Reader, header http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	tok, err := c.token(ctx, auth.StorageScope)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("x-ms-version", storageVersion)
	req.Header.Set("x-ms-date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("User-Agent", userAgent)
	return c.send(req)
}

// send does req and turns a non-2xx status into the error .NET's
// EnsureSuccessStatusCode raises.
func (c *client) send(req *http.Request) (*http.Response, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, &response.HTTPError{Msg: err.Error()}
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		_ = resp.Body.Close()
		return nil, &response.HTTPError{
			Status: resp.StatusCode,
			Msg:    fmt.Sprintf("Response status code does not indicate success: %d (%s).", resp.StatusCode, reason(resp)),
		}
	}
	return resp, nil
}

// reason returns the response's reason phrase, e.g. "Not Found" or a
// storage service's own text.
func reason(resp *http.Response) string {
	if r := strings.TrimSpace(strings.TrimPrefix(resp.Status, strconv.Itoa(resp.StatusCode))); r != "" {
		return r
	}
	return http.StatusText(resp.StatusCode)
}
