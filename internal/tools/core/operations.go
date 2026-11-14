package core

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// This file ports upstream's long-running operation support:
// FabricCoreService.Operations, FabricOperationHttp, FabricOperationRunner
// and FabricOperationErrors.

const (
	defaultMaxWait = 120.0 // FabricOperationWaitOptions.DefaultMaxWaitSeconds
	maximumWait    = 600.0 // FabricOperationWaitOptions.MaximumWaitSeconds
	fallbackPoll   = 20.0  // FabricOperationHttp.FallbackPollSeconds
	earlyPoll      = 3.0   // FabricOperationHttp.EarlyPollSeconds
)

type opError struct {
	ErrorCode *string    `json:"errorCode,omitempty"`
	RequestID *uuid.UUID `json:"requestId,omitempty"`
}

type opState struct {
	Status             string   `json:"status"`
	CreatedTimeUtc     *netTime `json:"createdTimeUtc,omitempty"`
	LastUpdatedTimeUtc *netTime `json:"lastUpdatedTimeUtc,omitempty"`
	PercentComplete    *int     `json:"percentComplete,omitempty"`
	Error              *opError `json:"error,omitempty"`
}

// stateResult ports OperationStateResult.
type stateResult struct {
	OperationID                 uuid.UUID `json:"operationId"`
	State                       opState   `json:"state"`
	ServerRetryAfterSeconds     *float64  `json:"serverRetryAfterSeconds,omitempty"`
	RecommendedPollAfterSeconds float64   `json:"recommendedPollAfterSeconds"`
}

// opResult ports OperationResult; Value is omitted when there's no body.
type opResult struct {
	OperationID *uuid.UUID      `json:"operationId,omitempty"`
	HasBody     bool            `json:"hasBody"`
	Value       json.RawMessage `json:"value,omitempty"`
}

func f64(v float64) *float64 { return &v }

// opFailure ports FabricOperationException (502 unless given).
func opFailure(code, msg string, status int, r *retryAfter) *failure {
	if status == 0 {
		status = http.StatusBadGateway
	}
	return &failure{kind: kHTTP, status: status, msg: msg, cls: "operation", code: code, retry: r}
}

// retryAfterSeconds ports FabricOperationHttp.GetRetryAfterSeconds.
func retryAfterSeconds(r *retryAfter) *float64 {
	switch {
	case r == nil:
		return nil
	case r.delta != nil:
		return f64(r.delta.Seconds())
	default:
		return f64(math.Max(0, time.Until(*r.date).Seconds()))
	}
}

// pollDelay ports FabricOperationHttp.GetPollDelay.
func pollDelay(server *float64, early bool) float64 {
	d := fallbackPoll
	if server != nil {
		d = *server
	}
	d = math.Max(1, d)
	if early {
		return math.Min(earlyPoll, d)
	}
	return d
}

// operationID ports FabricOperationHttp.GetOperationId.
func operationID(h http.Header) *uuid.UUID {
	v := h.Values("x-ms-operation-id")
	if len(v) != 1 {
		return nil
	}
	id, ok := parseID(v[0])
	if !ok {
		return nil
	}
	return &id
}

// unexpected ports FabricOperationHttp.ThrowUnexpectedResponse.
func unexpected(resp *http.Response) *failure {
	discard(resp)
	r := parseRetryAfter(resp.Header)
	if resp.StatusCode == http.StatusTooManyRequests && r != nil {
		return throttled("throttled", "request", r)
	}
	status := resp.StatusCode
	if status >= 200 && status < 300 {
		status = http.StatusBadGateway
	}
	return opFailure("UnexpectedResponse",
		"Fabric did not return the documented response. Check the operation or whether the mutation completed before submitting another mutation.",
		status, r)
}

// readOpJSON ports FabricOperationHttp.ReadJsonAsync.
func readOpJSON(resp *http.Response, id *uuid.UUID, max int64) (*opResult, error) {
	mt := resp.Header.Get("Content-Type")
	if mt != "" && !isJSON(resp) {
		discard(resp)
		return nil, opFailure("UnsupportedResultMediaType",
			"Only JSON and empty Fabric operation results are supported. Binary and other media types are not downloaded.", 0, nil)
	}
	body, err := readBody(resp, max, func() error {
		if max == maxJSONBytes {
			return opFailure("ResultTooLarge", "The Fabric operation JSON response exceeds the 1 MiB (1,048,576 byte) limit. No result was truncated.", 0, nil)
		}
		return opFailure("ResultTooLarge", "The Fabric operation JSON response exceeds the "+groupDigits(max)+" byte limit for this result. No result was truncated.", 0, nil)
	})
	if err != nil {
		return nil, err
	}
	if len(body) == 0 {
		return &opResult{OperationID: id}, nil
	}
	if mt == "" {
		return nil, opFailure("UnsupportedResultMediaType", "A nonempty Fabric operation result must have application/json content type.", 0, nil)
	}
	if !json.Valid(body) {
		return nil, opFailure("InvalidJson", "Fabric returned invalid JSON for the operation. No result was fabricated or truncated.", 0, nil)
	}
	return &opResult{OperationID: id, HasBody: true, Value: json.RawMessage(strings.TrimSpace(string(body)))}, nil
}

func groupDigits(n int64) string {
	d := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, c := range d {
		if i > 0 && (len(d)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(c)
	}
	return b.String()
}

func validateOperationID(h http.Header, id uuid.UUID) error {
	if len(h.Values("x-ms-operation-id")) > 0 {
		if got := operationID(h); got == nil || *got != id {
			return opFailure("InvalidOperationId", "Fabric returned inconsistent operation identification. Resume only with the original operation ID.", 0, nil)
		}
	}
	return nil
}

func invalidState() *failure {
	return opFailure("InvalidOperationState", "Fabric returned an invalid operation state. Completion could not be determined from this response.", 0, nil)
}

// getOperationState ports FabricCoreService.GetOperationStateAsync.
func (a *Area) getOperationState(ctx context.Context, id uuid.UUID) (*stateResult, error) {
	resp, _, err := a.send(ctx, http.MethodGet, "/operations/"+id.String(), nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, unexpected(resp)
	}
	if err := validateOperationID(resp.Header, id); err != nil {
		discard(resp)
		return nil, err
	}
	body, err := readOpJSON(resp, &id, maxJSONBytes)
	if err != nil {
		return nil, err
	}
	if !body.HasBody || !strings.HasPrefix(string(body.Value), "{") {
		return nil, invalidState()
	}
	var st opState
	if json.Unmarshal(body.Value, &st) != nil || isBlank(st.Status) ||
		st.PercentComplete != nil && (*st.PercentComplete < 0 || *st.PercentComplete > 100) {
		return nil, invalidState()
	}
	if st.Error != nil {
		if st.Error.ErrorCode != nil && !safeErrorCode(*st.Error.ErrorCode) {
			st.Error.ErrorCode = nil
		}
		if st.Error.RequestID != nil && *st.Error.RequestID == uuid.Nil {
			st.Error.RequestID = nil
		}
	}
	ra := retryAfterSeconds(parseRetryAfter(resp.Header))
	return &stateResult{OperationID: id, State: st, ServerRetryAfterSeconds: ra, RecommendedPollAfterSeconds: pollDelay(ra, false)}, nil
}

// getOperationResult ports FabricCoreService.GetOperationResultAsync.
func (a *Area) getOperationResult(ctx context.Context, id uuid.UUID, max int64) (*opResult, error) {
	resp, _, err := a.send(ctx, http.MethodGet, "/operations/"+id.String()+"/result", nil)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, unexpected(resp)
	}
	if err := validateOperationID(resp.Header, id); err != nil {
		discard(resp)
		return nil, err
	}
	return readOpJSON(resp, &id, max)
}

// opErrStatus ports FabricOperationErrors.GetStatusCode.
func opErrStatus(f *failure) int {
	switch f.kind {
	case kArg:
		return http.StatusBadRequest
	case kAuth:
		return http.StatusUnauthorized
	case kHTTP:
		if f.status != 0 {
			return f.status
		}
		return http.StatusServiceUnavailable
	case kCanceled:
		return http.StatusRequestTimeout
	case kTimeout:
		return http.StatusGatewayTimeout
	case kInvalidOp:
		return http.StatusUnprocessableEntity
	}
	return http.StatusInternalServerError
}

// opErrMessage ports FabricOperationErrors.GetMessage.
func opErrMessage(f *failure) string {
	if f.cls == "operation" && f.status == http.StatusBadGateway || f.cls == "throttled" {
		return f.msg
	}
	switch opErrStatus(f) {
	case http.StatusBadRequest:
		return "Provide a nonempty operation UUID"
	case http.StatusUnauthorized:
		return "Authentication failed. Use the identity authorized for the initiating Fabric API"
	case http.StatusForbidden:
		return "Access denied. Reading an operation requires the same resource permissions and delegated scopes as the initiating Fabric API"
	case http.StatusNotFound:
		return "The Fabric operation or result was not found, or is not accessible. This does not prove successful completion without a result"
	case http.StatusTooManyRequests:
		return "Fabric throttled the operation read. Wait before retrying the read"
	case http.StatusRequestTimeout:
		return "The operation read was canceled. The Fabric operation was not canceled"
	case http.StatusGatewayTimeout:
		return "The operation read timed out. The Fabric operation was not canceled"
	}
	return "Unable to read the Fabric operation. Check service availability and resume using the operation ID; do not resubmit the original mutation"
}

// --- tools ---

func (a *Area) getOperationStateTool(ctx context.Context, _ *mcp.CallToolRequest, in getOperationStateInput) (*mcp.CallToolResult, any, error) {
	return a.operationRead(ctx, in.OperationID, false)
}

func (a *Area) getOperationResultTool(ctx context.Context, _ *mcp.CallToolRequest, in getOperationResultInput) (*mcp.CallToolResult, any, error) {
	return a.operationRead(ctx, in.OperationID, true)
}

func (a *Area) operationRead(ctx context.Context, raw string, result bool) (*mcp.CallToolResult, any, error) {
	id, ok := parseID(raw)
	if !ok {
		return invalid([]string{"--operation-id must be a nonempty UUID."}), nil, nil
	}
	var res any
	var err error
	if result {
		res, err = a.getOperationResult(ctx, id, maxJSONBytes)
	} else {
		res, err = a.getOperationState(ctx, id)
	}
	if err != nil {
		f := asFailure(err)
		return fail(opErrStatus(f), opErrMessage(f)), nil, nil
	}
	return response.Success(res), nil, nil
}
