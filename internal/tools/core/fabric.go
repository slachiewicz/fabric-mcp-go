package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/streaming"
	"github.com/google/uuid"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/auth"
	"github.com/slachiewicz/fabric-mcp-go/internal/response"
)

// This file ports the HTTP plumbing upstream's FabricCoreService uses for
// the management tools: one request per call, no automatic retry, exact
// expected status codes, bounded JSON bodies and Retry-After guidance.

// maxJSONBytes is FabricResponseBodyReader.DefaultMaxJsonBytes.
const maxJSONBytes = 1024 * 1024

// kind is the .NET exception family a failure stands for; upstream's
// commands map status codes and messages from it.
type kind int

const (
	kOther       kind = iota // Exception
	kArg                     // ArgumentException
	kInput                   // FabricItemInputException
	kHTTP                    // HttpRequestException and its subclasses
	kJSON                    // JsonException
	kInvalidData             // InvalidDataException
	kInvalidOp               // InvalidOperationException
	kCanceled                // OperationCanceledException
	kTimeout                 // TimeoutException
	kAuth                    // AuthenticationFailedException
)

// failure is one of upstream's exceptions with the fields its commands
// branch on.
type failure struct {
	kind   kind
	status int    // HttpRequestException.StatusCode; 0 when unset
	msg    string // Exception.Message
	// cls names the HttpRequestException subclass when a command tests
	// for it (for example "throttled" for FabricThrottledException).
	cls             string
	retry           *retryAfter
	code            string // Fabric errorCode, or FabricOperationException.Code
	metaUnavailable bool
	notSent         bool
	respStatus      int  // the response status the request saw; 0 when none
	confirmed       bool // the response confirmed the mutation
	// op is the receipt of FabricOperationCanceledException.
	op *receipt
}

func (f *failure) Error() string { return f.msg }

func jsonError(msg string) *failure { return &failure{kind: kJSON, msg: msg} }
func httpError(status int, msg string) *failure {
	return &failure{kind: kHTTP, status: status, msg: msg}
}

// asFailure converts any error to the failure upstream would have raised.
func asFailure(err error) *failure {
	var f *failure
	if errors.As(err, &f) {
		return f
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &failure{kind: kCanceled, msg: "The operation was canceled."}
	}
	var cred *auth.CredentialError
	if errors.As(err, &cred) {
		return &failure{kind: kAuth, msg: err.Error()}
	}
	return &failure{kind: kHTTP, msg: err.Error()}
}

// baseStatus ports AuthenticatedCommand.GetStatusCode.
func baseStatus(f *failure) int {
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
	case kInvalidOp:
		return http.StatusUnprocessableEntity
	case kTimeout, kCanceled:
		return http.StatusGatewayTimeout
	}
	return http.StatusInternalServerError
}

// fail is upstream's FabricCoreCommand.HandleException: the command's
// status and message plus the troubleshooting link, and no results.
func fail(status int, msg string) *mcp.CallToolResult {
	return response.Fail(status, msg+response.Troubleshooting)
}

// invalid is a ValidateOptions failure: the errors joined by newlines.
func invalid(errs []string) *mcp.CallToolResult {
	return response.Fail(http.StatusBadRequest, strings.Join(errs, "\n"))
}

// --- Retry-After ---

// retryAfter ports RetryConditionHeaderValue: a delay or a date.
type retryAfter struct {
	delta *time.Duration
	date  *time.Time
}

// parseRetryAfter ports FabricCoreHttpHelpers.GetRetryAfter: exactly one
// valid header value.
func parseRetryAfter(h http.Header) *retryAfter {
	values := h.Values("Retry-After")
	if len(values) != 1 {
		return nil
	}
	v := strings.TrimSpace(values[0])
	if v == "" {
		return nil
	}
	if isDigits(v) {
		n, err := strconv.ParseInt(v, 10, 32)
		if err != nil {
			return nil
		}
		d := time.Duration(n) * time.Second
		return &retryAfter{delta: &d}
	}
	t, err := http.ParseTime(v)
	if err != nil {
		return nil
	}
	t = t.UTC()
	return &retryAfter{date: &t}
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// String ports RetryConditionHeaderValue.ToString.
func (r *retryAfter) String() string {
	if r.delta != nil {
		return strconv.FormatInt(int64(r.delta.Seconds()), 10)
	}
	return r.date.UTC().Format(http.TimeFormat)
}

// seconds is the delay in whole seconds, or nil for a date.
func (r *retryAfter) seconds() *int64 {
	if r == nil || r.delta == nil {
		return nil
	}
	s := int64(r.delta.Seconds())
	return &s
}

// utc is the date, or nil for a delay.
func (r *retryAfter) utc() *netTime {
	if r == nil || r.date == nil {
		return nil
	}
	t := netTime(*r.date)
	return &t
}

// guidance formats ". Wait at least N seconds before <what>" or the date
// form upstream appends to its messages.
func (r *retryAfter) guidance(what, dateLead string) string {
	if r.delta != nil {
		return fmt.Sprintf(". Wait at least %d seconds before %s", int64(r.delta.Seconds()), what)
	}
	return fmt.Sprintf(". %s %s UTC%s", dateLead, r.date.UTC().Format("2006-01-02 15:04:05"), dateSuffix(what, dateLead))
}

func dateSuffix(what, lead string) string {
	if lead == "Retry after" {
		return ""
	}
	return " before " + what
}

// retryGuidance is the ". Wait at least N seconds before retrying" or
// ". Retry after <date> UTC" suffix.
func (r *retryAfter) retryGuidance() string { return r.guidance("retrying", "Retry after") }

// requestGuidance is the ". Wait at least N seconds before another request"
// or ". Wait until at least <date> UTC before another request" suffix.
func (r *retryAfter) requestGuidance() string {
	return r.guidance("another request", "Wait until at least")
}

// throttled ports FabricThrottledException and its siblings: subject is
// "request", "deletion request" or "update".
func throttled(cls, subject string, r *retryAfter) *failure {
	var msg string
	if r.delta != nil {
		msg = fmt.Sprintf("Fabric throttled the %s. Wait at least %d seconds before retrying", subject, int64(r.delta.Seconds()))
	} else {
		msg = fmt.Sprintf("Fabric throttled the %s. Retry after %s UTC", subject, r.date.UTC().Format("2006-01-02 15:04:05"))
	}
	return &failure{kind: kHTTP, status: http.StatusTooManyRequests, msg: msg, cls: cls, retry: r}
}

// --- .NET value formatting ---

// netTime marshals like System.Text.Json's DateTimeOffset: ISO 8601 with
// up to seven trimmed fraction digits and a numeric offset.
type netTime time.Time

func (t netTime) MarshalJSON() ([]byte, error) {
	return json.Marshal(time.Time(t).Format("2006-01-02T15:04:05.9999999-07:00"))
}

func (t *netTime) UnmarshalJSON(b []byte) error {
	var tt time.Time
	if err := json.Unmarshal(b, &tt); err != nil {
		return err
	}
	*t = netTime(tt)
	return nil
}

// parseID ports Guid.TryParse plus the nonempty check.
func parseID(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(s))
	return id, err == nil && id != uuid.Nil
}

// parseOptID parses an optional ID; ok is false only for a supplied,
// invalid one.
func parseOptID(s *string) (*uuid.UUID, bool) {
	if s == nil {
		return nil, true
	}
	id, ok := parseID(*s)
	if !ok {
		return nil, false
	}
	return &id, true
}

func isBlank(s string) bool { return strings.TrimSpace(s) == "" }

// validToken ports FabricContinuationToken.IsValidContinuationToken.
func validToken(tok *string) bool {
	if tok == nil {
		return true
	}
	t := *tok
	if isBlank(t) || !utf8.ValidString(t) || len(t) > 8192 {
		return false
	}
	for _, r := range t {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

const invalidTokenMessage = "Continuation token must be valid Unicode, not blank, without control characters and at most 8,192 UTF-8 bytes."

// encodeToken ports FabricCoreHttpHelpers.EncodeContinuationToken: escape
// the raw spans of a token, keeping existing %XX escapes.
func encodeToken(tok string) string {
	var b strings.Builder
	start := 0
	for i := 0; i+2 < len(tok); i++ {
		if tok[i] == '%' && isHex(tok[i+1]) && isHex(tok[i+2]) {
			b.WriteString(escapeData(tok[start:i]))
			b.WriteString(tok[i : i+3])
			i += 2
			start = i + 1
		}
	}
	b.WriteString(escapeData(tok[start:]))
	return b.String()
}

func isHex(c byte) bool {
	return '0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F'
}

// escapeData ports Uri.EscapeDataString: every byte outside RFC 3986
// unreserved characters is percent-encoded.
func escapeData(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if 'A' <= c && c <= 'Z' || 'a' <= c && c <= 'z' || '0' <= c && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)
		} else {
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

// --- requests ---

// send makes one Fabric REST call through the SDK pipeline with retries
// disabled, like upstream's SendFabricHttpRequestAsync. The body is left
// unread. sent reports whether the request got past authentication, which
// upstream tracks to tell "not sent" from an uncertain outcome.
func (a *Area) send(ctx context.Context, method, path string, body any) (resp *http.Response, sent bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	ctx = policy.WithRetryOptions(ctx, policy.RetryOptions{MaxRetries: -1})
	req, err := runtime.NewRequest(ctx, method, a.base+path)
	if err != nil {
		return nil, false, err
	}
	runtime.SkipBodyDownload(req)
	if body != nil {
		var b []byte
		if raw, ok := body.([]byte); ok {
			b = raw
		} else if b, err = json.Marshal(body); err != nil {
			return nil, false, err
		}
		if err := req.SetBody(streaming.NopCloser(bytes.NewReader(b)), "application/json; charset=utf-8"); err != nil {
			return nil, false, err
		}
	}
	resp, err = a.pipeline.Do(req)
	if err != nil {
		var cred *auth.CredentialError
		return nil, !errors.As(err, &cred), err
	}
	return resp, true, nil
}

// readBody ports FabricResponseBodyReader.ReadAsync: at most max bytes.
func readBody(resp *http.Response, max int64, tooLarge func() error) ([]byte, error) {
	defer func() { _ = resp.Body.Close() }()
	if resp.ContentLength > max {
		return nil, tooLarge()
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, tooLarge()
	}
	return b, nil
}

func discard(resp *http.Response) {
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxJSONBytes))
	_ = resp.Body.Close()
}

// isJSON reports whether the response media type is application/json.
func isJSON(resp *http.Response) bool {
	mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	return err == nil && strings.EqualFold(mt, "application/json")
}

// readErrorCode ports FabricCoreHttpHelpers.ReadErrorCodeAsync: one safe
// top-level errorCode, or unavailable.
func readErrorCode(resp *http.Response) (code string, unavailable bool) {
	if !isJSON(resp) {
		discard(resp)
		return "", true
	}
	b, err := readBody(resp, maxJSONBytes, func() error { return errors.New("too large") })
	if err != nil {
		return "", true
	}
	var codes []json.RawMessage
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil || tok != json.Delim('{') {
		return "", true
	}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return "", true
		}
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return "", true
		}
		if k == "errorCode" {
			codes = append(codes, v)
		}
	}
	if len(codes) != 1 {
		return "", true
	}
	var s string
	if json.Unmarshal(codes[0], &s) != nil || !safeErrorCode(s) {
		return "", true
	}
	return s, false
}

// safeErrorCode ports FabricCoreHttpHelpers.IsSafeErrorCode.
func safeErrorCode(code string) bool {
	if len(code) == 0 || len(code) > 128 {
		return false
	}
	for _, c := range code {
		if (c >= 128 || !unicode.IsLetter(c) && !unicode.IsDigit(c)) && c != '_' && c != '-' && c != '.' {
			return false
		}
	}
	return true
}

// validateMetadataJSON ports JsonDocument parsing with MaxDepth 64 and no
// duplicate properties, plus FabricCoreHttpHelpers.ValidateMetadataJson,
// which also rejects property names that differ only in case.
func validateMetadataJSON(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	if err := walkJSON(dec, 0); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing data")
	}
	return nil
}

func walkJSON(dec *json.Decoder, depth int) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	if depth+1 > 64 {
		return errors.New("too deep")
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			k, err := dec.Token()
			if err != nil {
				return err
			}
			name := strings.ToLower(k.(string))
			if seen[name] {
				return errors.New("ambiguous JSON metadata")
			}
			seen[name] = true
			if err := walkJSON(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := walkJSON(dec, depth+1); err != nil {
				return err
			}
		}
	}
	_, err = dec.Token()
	return err
}

// matchingCollectionURI ports FabricCoreHttpHelpers.GetMatchingCollectionUri:
// an absolute URI on the collection's scheme, host, port and exact path,
// with a query.
func matchingCollectionURI(value string, collection *url.URL, foldPath bool) *url.URL {
	if value == "" || strings.ContainsAny(value, "\\") {
		return nil
	}
	for _, r := range value {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return nil
		}
	}
	q := strings.IndexByte(value, '?')
	if q < 0 {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || !u.IsAbs() || u.Scheme != collection.Scheme || !strings.EqualFold(u.Hostname(), collection.Hostname()) ||
		port(u) != port(collection) || u.User != nil || u.Fragment != "" {
		return nil
	}
	eq := func(a, b string) bool {
		if foldPath {
			return strings.EqualFold(a, b)
		}
		return a == b
	}
	schemeEnd := strings.Index(value, "://") + 3
	ps := strings.IndexByte(value[schemeEnd:], '/')
	if ps < 0 {
		return nil
	}
	ps += schemeEnd
	if ps >= q || strings.Contains(value[:ps], "%") || !eq(value[ps:q], collection.EscapedPath()) {
		return nil
	}
	return u
}

func port(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

// --- confirmed mutations ---

// mutated is the confirmed result of a mutation plus the response's
// Retry-After guidance.
type mutated[T any] struct {
	value T
	retry *retryAfter
}

// mutate ports the request flow upstream's folder create/update/move and
// item move share: one request, the documented status (200 unless ok is
// given) confirms the mutation, and every failure becomes a request
// exception that records whether the request was sent and whether the
// mutation was confirmed.
func mutate[T any](ctx context.Context, a *Area, method, path string, body any, excMsg string,
	read func([]byte) (T, error), nonJSON, tooLarge string, ok ...int) (*mutated[T], error) {
	okStatus := http.StatusOK
	if len(ok) > 0 {
		okStatus = ok[0]
	}
	exc := func(status int) *failure {
		return &failure{kind: kHTTP, status: status, msg: excMsg, cls: "mutation"}
	}
	resp, sent, err := a.send(ctx, method, path, body)
	if err != nil {
		f := asFailure(err)
		e := exc(0)
		switch f.kind {
		case kCanceled:
			e.status = http.StatusRequestTimeout
		case kTimeout:
			e.status = http.StatusGatewayTimeout
		case kAuth:
			e.status = http.StatusUnauthorized
		default:
			e.status = baseStatus(f)
		}
		e.notSent = !sent
		return nil, e
	}
	r := parseRetryAfter(resp.Header)
	if resp.StatusCode != okStatus {
		e := exc(nonSuccess(resp.StatusCode))
		e.retry, e.respStatus = r, resp.StatusCode
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			discard(resp)
		} else {
			e.code, e.metaUnavailable = readErrorCode(resp)
		}
		return nil, e
	}
	confirmed := func(status int) *failure {
		e := exc(status)
		e.retry, e.respStatus, e.confirmed = r, okStatus, true
		return e
	}
	if !isJSON(resp) {
		discard(resp)
		return nil, confirmed(http.StatusBadGateway)
	}
	b, err := readBody(resp, maxJSONBytes, func() error { return jsonError(tooLarge) })
	if err != nil {
		if ctx.Err() != nil {
			return nil, confirmed(http.StatusRequestTimeout)
		}
		return nil, confirmed(http.StatusBadGateway)
	}
	v, err := read(b)
	if err != nil {
		return nil, confirmed(http.StatusBadGateway)
	}
	if ctx.Err() != nil {
		return nil, confirmed(http.StatusRequestTimeout)
	}
	return &mutated[T]{v, r}, nil
}

// mutationText holds one command's messages for mutationFail.
type mutationText struct {
	confirmed   string // the mutation was confirmed but its result unusable
	notSent     string // appended when the request was not sent
	maybe       string // appended when the outcome is uncertain
	inputSuffix string // appended to a FabricItemInputException message
	// maybeOn4xx keeps the uncertain-outcome note on a 4xx response, as
	// folder creation does.
	maybeOn4xx bool
	table      messages
}

// mutationFail ports the GetErrorMessage of the commands that use mutate.
func mutationFail(f *failure, t mutationText) *mcp.CallToolResult {
	status := baseStatus(f)
	switch f.kind {
	case kInput:
		return fail(http.StatusBadRequest, f.msg+t.inputSuffix)
	case kCanceled:
		status = http.StatusRequestTimeout
	}
	msg := t.table.get(status)
	if f.confirmed {
		msg = t.confirmed
	}
	certain4xx := f.respStatus >= 400 && f.respStatus < 500 && f.respStatus != http.StatusRequestTimeout
	switch {
	case f.notSent || f.kind == kAuth || f.kind == kArg:
		msg += t.notSent
	case !f.confirmed && (t.maybeOn4xx || !certain4xx):
		msg += t.maybe
	}
	msg += ". No automatic retry was attempted"
	if f.cls == "mutation" {
		if f.code != "" {
			msg += ". Fabric error code: " + f.code
		}
		if f.metaUnavailable {
			msg += ". Additional error metadata was unavailable"
		}
		if f.retry != nil {
			msg += f.retry.requestGuidance()
		}
	}
	return fail(status, msg)
}

// credentialUnavailable reports a CredentialUnavailableException.
func credentialUnavailable(err error) bool {
	var cred *auth.CredentialError
	return errors.As(err, &cred) && cred.Unavailable
}
