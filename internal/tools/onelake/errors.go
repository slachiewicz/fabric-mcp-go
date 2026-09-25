package onelake

import (
	"errors"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// argError mirrors .NET's ArgumentException.
type argError struct{ msg string }

func (e *argError) Error() string { return e.msg }

// opError mirrors .NET's InvalidOperationException.
type opError struct{ msg string }

func (e *opError) Error() string { return e.msg }

// errorResult ports OneLakeCommandValidators, which most OneLake commands
// use in place of the default message and status mapping. Commands that
// don't override them upstream use response.Error instead.
func errorResult(err error) *mcp.CallToolResult {
	var (
		arg     *argError
		op      *opError
		httpErr *response.HTTPError
	)
	switch {
	case errors.As(err, &arg):
		return response.Exc(http.StatusBadRequest, "ArgumentException", "Invalid argument: "+arg.msg, arg.msg)
	case errors.As(err, &op):
		return response.Exc(http.StatusInternalServerError, "InvalidOperationException", "Operation failed: "+op.msg, op.msg)
	case errors.As(err, &httpErr):
		status := httpErr.Status
		switch msg := httpErr.Msg; {
		case strings.Contains(msg, "404"):
			status = http.StatusNotFound
		case strings.Contains(msg, "403"):
			status = http.StatusForbidden
		case strings.Contains(msg, "401"):
			status = http.StatusUnauthorized
		case status == 0:
			status = http.StatusServiceUnavailable
		}
		return response.Exc(status, "HttpRequestException", "HTTP request failed: "+httpErr.Msg, httpErr.Msg)
	}
	return response.Error(err)
}
