// Package response builds tool results in upstream's command response
// envelope: a single JSON text block
// {"status":<code>,"message":<text>,"results":<value>,"duration":<ms>}
// and no structuredContent.
package response

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	fabcore "github.com/microsoft/fabric-sdk-go/fabric/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
)

const troubleshooting = ". To mitigate this issue, please refer to the troubleshooting guidelines here at https://aka.ms/azmcp/troubleshooting."

type envelope struct {
	Status   int    `json:"status"`
	Message  string `json:"message"`
	Results  any    `json:"results,omitempty"`
	Duration int    `json:"duration"`
}

// Success returns a 200 result carrying results.
func Success(results any) *mcp.CallToolResult {
	return result(false, envelope{Status: http.StatusOK, Message: "Success", Results: results})
}

// Fail returns an error result with no results, the shape upstream uses for
// validation failures and errors a command catches itself.
func Fail(status int, message string) *mcp.CallToolResult {
	return result(true, envelope{Status: status, Message: message})
}

// Exception is an error that knows the status and .NET exception type name
// upstream reports for it.
type Exception interface {
	error
	Status() int
	Type() string
}

// HTTPError mirrors .NET's HttpRequestException. Status is 0 when the
// exception carries no status code.
type HTTPError struct {
	Status int
	Msg    string
}

func (e *HTTPError) Error() string { return e.Msg }

// Exc returns the envelope upstream's HandleException builds: message is
// the command's error message, raw the exception's own message and typ its
// .NET type name.
func Exc(status int, typ, message, raw string) *mcp.CallToolResult {
	return result(true, envelope{
		Status:  status,
		Message: message + troubleshooting,
		Results: map[string]string{"message": raw, "type": typ},
	})
}

// Error returns the result upstream's HandleException produces for err.
func Error(err error) *mcp.CallToolResult {
	status, typ, msg := http.StatusInternalServerError, "Exception", err.Error()
	var (
		exc     Exception
		credErr *auth.CredentialError
		apiErr  *fabcore.ResponseError
		httpErr *HTTPError
	)
	switch {
	case errors.As(err, &exc):
		status, typ = exc.Status(), exc.Type()
	case errors.As(err, &credErr) && credErr.Unavailable:
		status, typ = http.StatusUnauthorized, "CredentialUnavailableException"
		msg = "Azure credentials not found or unavailable. Please run 'az login' to authenticate, then try again. " +
			"For the complete list of supported credentials, see: https://aka.ms/azmcp/auth. Details: " + credErr.Error()
	case errors.As(err, &credErr):
		status, typ = http.StatusUnauthorized, "AuthenticationFailedException"
		msg = "Authentication failed. Please run 'az login' to sign in to Azure. Details: " + credErr.Error()
	case errors.As(err, &apiErr):
		// Upstream raises HttpRequestException without a status code, so
		// every Fabric API failure reports 503.
		status, typ = http.StatusServiceUnavailable, "HttpRequestException"
		msg = apiErrorMessage(apiErr)
		return result(true, envelope{
			Status:  status,
			Message: "Service unavailable or network connectivity issues. Details: " + msg + troubleshooting,
			Results: map[string]string{"message": msg, "type": typ},
		})
	case errors.As(err, &httpErr):
		status, typ = httpErr.Status, "HttpRequestException"
		if status == 0 {
			status = http.StatusServiceUnavailable
		}
		msg = "Service unavailable or network connectivity issues. Details: " + err.Error()
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		status, typ = http.StatusGatewayTimeout, "TaskCanceledException"
		msg = "The operation timed out or was canceled. Details: " + strings.TrimSuffix(err.Error(), ".")
	}
	return Exc(status, typ, msg, err.Error())
}

// apiErrorMessage formats a Fabric API error the way upstream's
// EnsureSuccessAsync does: "Fabric API request failed with status 400 (BadRequest): <body>".
func apiErrorMessage(e *fabcore.ResponseError) string {
	body := "{}"
	if e.ErrorResponse != nil {
		if b, err := e.ErrorResponse.MarshalJSON(); err == nil {
			body = string(b)
		}
	}
	reason := strings.ReplaceAll(http.StatusText(e.StatusCode), " ", "")
	return fmt.Sprintf("Fabric API request failed with status %d (%s): %s", e.StatusCode, reason, body)
}

func result(isError bool, e envelope) *mcp.CallToolResult {
	b, err := json.Marshal(e)
	if err != nil {
		b, _ = json.Marshal(envelope{Status: http.StatusInternalServerError, Message: "marshal result: " + err.Error()})
		isError = true
	}
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: string(b)}}, IsError: isError}
}
