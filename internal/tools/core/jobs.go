package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/microsoft/fabric-sdk-go/fabric"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/response"
	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

// Jobs is the "jobs" tool namespace, porting FabricJobsSetup. Upstream
// keeps it in the Core project and shares its service, as this port shares
// the core area's plumbing.
type Jobs struct{ a *Area }

// NewJobs returns the jobs area backed by client.
func NewJobs(client *fabric.Client) *Jobs { return &Jobs{New(client)} }

// Name implements server.Area.
func (*Jobs) Name() string { return "jobs" }

// Title implements server.Describer; the CommandGroup has no title.
func (*Jobs) Title() string { return "" }

// Description implements server.Describer, porting FabricJobsSetup's
// CommandGroup description verbatim.
func (*Jobs) Description() string {
	return "Microsoft Fabric Jobs - Submit, request cancellation of, read, or list item jobs, and get, list, create, update, or delete schedules using explicit context.\n" +
		"Run and Cancel require execution permission and consent. Run may consume compute or change data; Cancel does not promise rollback.\n" +
		"Run returns submission acceptance; Cancel returns cancellation requested, not a Cancelled job state. Neither automatically retries.\n" +
		"Use the known job-instance ID in a separate Get call after waiting at least the returned Retry-After guidance.\n" +
		"Get Job Instance returns status, timing, safe failure metadata, and retry guidance. No tool polls, waits, or retrieves results.\n" +
		"List Job Instances reads one page only; call again explicitly with the same workspace/item IDs and unchanged token for another page.\n" +
		"For most items the API reports up to 100 recently completed instances, not a 100-row page cap; active instances have no corresponding limit.\n" +
		"Listing does not identify an earlier ambiguous Run submission or authorize cancellation. HTTP success does not mean jobs succeeded.\n" +
		"List Schedules reads one page for workspace/item IDs and an explicit job type; continuation requires the same context and unchanged token.\n" +
		"Get Schedule reads one snapshot for explicit workspace/item/schedule IDs and job type, without listing or enrichment. Read permission and disclosure consent are required, even in read-only mode.\n" +
		"A schedule snapshot is not job execution status or proof of an ambiguous mutation outcome, and reading never authorizes a mutation.\n" +
		"Create Schedule sends one POST with explicit enabled and configuration, returning creation, not job execution or success.\n" +
		"Update Schedule sends one PATCH for an explicit schedule ID with enabled and full configuration; no hidden Get or merge.\n" +
		"Updating is not job execution; disabling is not cancellation. Omitted execution data has no documented preservation or clearing guarantee.\n" +
		"Before consenting, confirm timing: an enabled schedule with a past start may execute immediately, consuming compute or changing data.\n" +
		"Creation and updates require Fabric permissions and delegated Item.Execute.All plus Item.ReadWrite.All. No preflight or automatic retry is performed.\n" +
		"Delete Schedule sends one DELETE without a body for explicit workspace/item/schedule IDs and job type. HTTP 200 returns a deletion receipt, not a schedule snapshot.\n" +
		"Deletion requires consent, Fabric permissions, and delegated Item.ReadWrite.All or the item-specific ReadWrite.All scope. It does not cancel existing jobs or promise rollback.\n" +
		"Deletion never retries, verifies with a second request, or treats 404 as success. Inspect uncertain outcomes separately before considering another deletion.\n" +
		"Schedule settings, owner, and execution data may be sensitive. Cron intervals are minutes, not cron expressions. No timezone conversion is performed.\n" +
		"A schedule's owner identifies its creator or last modifier, separately from item ownership. Confirm the calling identity before updates.\n" +
		"Schedule IDs, job-instance IDs, and operation IDs are distinct."
}

// Register implements server.Area, in FabricJobsSetup's order.
func (j *Jobs) Register(r *server.Registrar) {
	a := j.a
	server.AddTool(r, cancelItemJobInstanceTool(), a.cancelItemJobInstance)
	server.AddTool(r, getItemJobInstanceTool(), a.getItemJobInstance)
	server.AddTool(r, listItemJobInstancesTool(), a.listItemJobInstances)
	server.AddTool(r, createItemScheduleTool(), a.createItemSchedule)
	server.AddTool(r, deleteItemScheduleTool(), a.deleteItemSchedule)
	server.AddTool(r, getItemScheduleTool(), a.getItemSchedule)
	server.AddTool(r, listItemSchedulesTool(), a.listItemSchedules)
	server.AddTool(r, updateItemScheduleTool(), a.updateItemSchedule)
	server.AddTool(r, runOnDemandItemJobTool(), a.runOnDemandItemJob)
}

var jobsBindFallback = map[string]string{
	"jobs_run-on-demand-item-job":   "Invalid Fabric job submission. Provide workspace and item UUIDs and job-type; check execution-data and job-parameters JSON options.",
	"jobs_get-item-job-instance":    "Invalid Fabric Jobs request. Provide workspace, item, and job-instance UUIDs.",
	"jobs_list-item-job-instances":  "Invalid Fabric Jobs list request. Provide workspace and item UUIDs and an optional unchanged continuation token.",
	"jobs_cancel-item-job-instance": "Invalid Fabric job cancellation request. Provide workspace, item, and job-instance UUIDs.",
	"jobs_create-item-schedule":     "Invalid Fabric schedule creation. Provide workspace/item UUIDs, job-type, explicit enabled true or false, configuration JSON, and optional execution-data.",
	"jobs_get-item-schedule":        "Invalid Fabric schedule lookup request. Provide workspace, item, and schedule UUIDs and an explicit job type.",
	"jobs_list-item-schedules":      "Invalid Fabric schedule listing request. Provide workspace and item UUIDs, a job type, and an optional unchanged continuation token.",
	"jobs_update-item-schedule":     "Invalid Fabric schedule update. Provide workspace/item/schedule UUIDs, job-type, explicit enabled true or false, full configuration JSON, and optional execution-data.",
	"jobs_delete-item-schedule":     "Invalid Fabric schedule deletion. Provide workspace/item/schedule UUIDs and an explicit job type.",
}

// BindMessage implements server.BindMessager like the core area's.
func (*Jobs) BindMessage(tool string, missing []string, _ string) string {
	msg := jobsBindFallback[tool]
	if len(missing) > 0 {
		msg = "Missing Required options: " + strings.Join(missing, ", ")
	}
	if tool == "jobs_delete-item-schedule" {
		msg += " No DELETE request was sent."
	}
	return msg
}

// --- shared input ---

// jobContextErrors ports ItemJobRunInput.ValidateContext.
func jobContextErrors(workspaceID, itemID, jobType string) (ws, item uuid.UUID, errs []string) {
	var ok bool
	if ws, ok = parseID(workspaceID); !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	if item, ok = parseID(itemID); !ok {
		errs = append(errs, "--item-id must be a nonempty UUID.")
	}
	bad := isBlank(jobType) || jobType == "." || jobType == ".." || utf16Len(jobType) > 1024 || len(jobType) > 1024 || !utf8.ValidString(jobType)
	for _, r := range jobType {
		bad = bad || unicode.IsControl(r)
	}
	if bad {
		errs = append(errs, "--job-type must be nonempty and not whitespace, valid Unicode without control characters or dot segments, within the 1,024 UTF-8 byte tool budget.")
	}
	return ws, item, errs
}

func jobPath(ws, item uuid.UUID, rest string) string {
	return "/workspaces/" + ws.String() + "/items/" + item.String() + "/jobs" + rest
}

// firstReadGuidance is the Retry-After sentence of the job receipts.
func firstReadGuidance(r *retryAfter) string {
	switch {
	case r != nil && r.delta != nil:
		return fmt.Sprintf(" Wait at least %d seconds before the first status read.", int64(r.delta.Seconds()))
	case r != nil:
		return " Wait until at least " + r.date.UTC().Format("2006-01-02 15:04:05") + " UTC before the first status read."
	}
	return " No valid Retry-After guidance was supplied; the first-read delay is unknown."
}

func retryIssue(r *retryAfter) *string {
	if r == nil {
		s := "MissingOrInvalidRetryAfter"
		return &s
	}
	return nil
}

// --- job instances ---

// jobInstance ports FabricItemJobInstance.
type jobInstance struct {
	ID             uuid.UUID  `json:"id"`
	ItemID         uuid.UUID  `json:"itemId"`
	JobType        *string    `json:"jobType"`
	InvokeType     *string    `json:"invokeType"`
	Status         *string    `json:"status"`
	RootActivityID *uuid.UUID `json:"rootActivityId,omitempty"`
	StartTimeUtc   *string    `json:"startTimeUtc,omitempty"`
	EndTimeUtc     *string    `json:"endTimeUtc,omitempty"`
	FailureReason  *struct {
		ErrorCode   *string    `json:"errorCode,omitempty"`
		RequestID   *uuid.UUID `json:"requestId,omitempty"`
		IsRetriable *bool      `json:"isRetriable,omitempty"`
	} `json:"failureReason,omitempty"`
}

// validJobInstance ports ValidateItemJobInstance.
func validJobInstance(in *jobInstance, item uuid.UUID, id *uuid.UUID) bool {
	if in == nil || in.ID == uuid.Nil || id != nil && in.ID != *id || in.ItemID != item ||
		in.JobType == nil || isBlank(*in.JobType) || in.InvokeType == nil || isBlank(*in.InvokeType) ||
		in.Status == nil || isBlank(*in.Status) {
		return false
	}
	if f := in.FailureReason; f != nil {
		if f.ErrorCode != nil && !safeErrorCode(*f.ErrorCode) {
			f.ErrorCode = nil
		}
		if f.RequestID != nil && *f.RequestID == uuid.Nil {
			f.RequestID = nil
		}
	}
	return true
}

func jobInstanceIDs(workspaceID, itemID, jobID string) (ws, item, job uuid.UUID, errs []string) {
	var ok bool
	if ws, ok = parseID(workspaceID); !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	if item, ok = parseID(itemID); !ok {
		errs = append(errs, "--item-id must be a nonempty UUID.")
	}
	if job, ok = parseID(jobID); !ok {
		errs = append(errs, "--job-instance-id must be a nonempty UUID.")
	}
	return
}

func (a *Area) getItemJobInstance(ctx context.Context, _ *mcp.CallToolRequest, in getItemJobInstanceInput) (*mcp.CallToolResult, any, error) {
	ws, item, job, errs := jobInstanceIDs(in.WorkspaceID, in.ItemID, in.JobInstanceID)
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	type result struct {
		JobInstance       *jobInstance `json:"jobInstance"`
		RetryAfterSeconds *int64       `json:"retryAfterSeconds,omitempty"`
		RetryAfterUtc     *netTime     `json:"retryAfterUtc,omitempty"`
	}
	res, err := func() (*result, error) {
		resp, _, err := a.send(ctx, http.MethodGet, jobPath(ws, item, "/instances/"+job.String()), nil)
		if err != nil {
			return nil, err
		}
		r := parseRetryAfter(resp.Header)
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			f := httpError(nonSuccess(resp.StatusCode), "Unable to retrieve the Fabric item job instance.")
			f.cls, f.retry = "jobRequest", r
			return nil, f
		}
		if !isJSON(resp) {
			discard(resp)
			return nil, jsonError("Fabric returned a non-JSON job-instance response.")
		}
		b, err := readBody(resp, maxJSONBytes, func() error {
			return jsonError("The Fabric job-instance response exceeds the 1 MiB (1,048,576 byte) limit.")
		})
		if err != nil {
			return nil, err
		}
		var inst *jobInstance
		if json.Unmarshal(b, &inst) != nil || !validJobInstance(inst, item, &job) {
			return nil, jsonError("Fabric returned invalid job-instance metadata.")
		}
		return &result{inst, r.seconds(), r.utc()}, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		msg := messages{
			http.StatusBadRequest:      "Provide valid workspace, item, and job-instance UUIDs",
			http.StatusUnauthorized:    "Authentication failed. Sign in with an identity that can read the Fabric item",
			http.StatusForbidden:       "Access denied. The caller needs read permission for this Fabric item",
			http.StatusNotFound:        "The Fabric item or job instance was not found. Check the workspace, item, and job-instance IDs",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric job-instance request was canceled or timed out",
			http.StatusBadGateway:      "Fabric returned an invalid job-instance response. Expected matching job metadata as JSON up to 1 MiB",
			0:                          "Unable to retrieve the Fabric item job instance. Retry the request or check service availability",
		}.get(status)
		if f.cls == "jobRequest" && f.retry != nil {
			msg += f.retry.retryGuidance()
		}
		return fail(status, msg), nil, nil
	}
	return response.Success(res), nil, nil
}

const invalidJobPage = "Fabric returned invalid job-instance page metadata. No partial page was returned."

func (a *Area) listItemJobInstances(ctx context.Context, _ *mcp.CallToolRequest, in listItemJobInstancesInput) (*mcp.CallToolResult, any, error) {
	var errs []string
	ws, ok := parseID(in.WorkspaceID)
	if !ok {
		errs = append(errs, "--workspace-id must be a nonempty UUID.")
	}
	item, ok := parseID(in.ItemID)
	if !ok {
		errs = append(errs, "--item-id must be a nonempty UUID.")
	}
	if !validToken(in.ContinuationToken) {
		errs = append(errs, invalidTokenMessage)
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	type result struct {
		JobInstances      []*jobInstance `json:"jobInstances"`
		ContinuationToken *string        `json:"continuationToken,omitempty"`
		RetryAfterSeconds *int64         `json:"retryAfterSeconds,omitempty"`
		RetryAfterUtc     *netTime       `json:"retryAfterUtc,omitempty"`
	}
	path := jobPath(ws, item, "/instances")
	res, err := func() (*result, error) {
		resp, _, err := a.send(ctx, http.MethodGet, path+query("continuationToken", optToken(in.ContinuationToken)), nil)
		if err != nil {
			return nil, err
		}
		r := parseRetryAfter(resp.Header)
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			f := httpError(nonSuccess(resp.StatusCode), "Unable to retrieve the Fabric item job instance.")
			f.cls, f.retry = "jobRequest", r
			return nil, f
		}
		if !isJSON(resp) {
			discard(resp)
			return nil, jsonError("Fabric returned a non-JSON job-instance page.")
		}
		b, err := readBody(resp, maxJSONBytes, func() error {
			return jsonError("The Fabric job-instance page exceeds the 1 MiB (1,048,576 byte) limit. No partial page was returned.")
		})
		if err != nil {
			return nil, err
		}
		var page map[string]json.RawMessage
		if validateMetadataJSON(b) != nil || json.Unmarshal(b, &page) != nil {
			return nil, jsonError(invalidJobPage)
		}
		var values []json.RawMessage
		if raw, ok := page["value"]; !ok || json.Unmarshal(raw, &values) != nil || values == nil {
			return nil, jsonError(invalidJobPage)
		}
		collection, _ := url.Parse(a.base + path)
		tok, err := jobPageToken(page, collection, true, invalidJobPage)
		if err != nil {
			return nil, err
		}
		out := &result{JobInstances: []*jobInstance{}, ContinuationToken: tok, RetryAfterSeconds: r.seconds(), RetryAfterUtc: r.utc()}
		for _, v := range values {
			var inst *jobInstance
			if json.Unmarshal(v, &inst) != nil || !validJobInstance(inst, item, nil) {
				return nil, jsonError(invalidJobPage)
			}
			out.JobInstances = append(out.JobInstances, inst)
		}
		return out, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		msg := messages{
			http.StatusBadRequest:      "Provide valid workspace and item UUIDs and an optional unchanged continuation token",
			http.StatusUnauthorized:    "Authentication failed. Sign in with an identity that can read the Fabric item",
			http.StatusForbidden:       "Access denied. The caller needs read permission for this Fabric item",
			http.StatusNotFound:        "The Fabric item was not found. Check the workspace and item IDs",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric job listing request was canceled or timed out",
			http.StatusBadGateway:      "Fabric returned an invalid job-instance page. Expected matching job metadata and consistent continuation information as JSON up to 1 MiB and 64 nesting levels. No partial page was returned",
			0:                          "Unable to list Fabric item job instances. Retry the request or check service availability",
		}.get(status)
		if f.cls == "jobRequest" && f.retry != nil {
			msg += f.retry.retryGuidance()
		}
		return fail(status, msg), nil, nil
	}
	return response.Success(res), nil, nil
}

func (a *Area) cancelItemJobInstance(ctx context.Context, _ *mcp.CallToolRequest, in cancelItemJobInstanceInput) (*mcp.CallToolResult, any, error) {
	ws, item, job, errs := jobInstanceIDs(in.WorkspaceID, in.ItemID, in.JobInstanceID)
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	resp, _, err := a.send(ctx, http.MethodPost, jobPath(ws, item, "/instances/"+job.String()+"/cancel"), nil)
	var f *failure
	if err != nil {
		f = asFailure(err)
	} else {
		r := parseRetryAfter(resp.Header)
		switch {
		case resp.StatusCode == http.StatusAccepted:
			discard(resp)
			guidance := "Fabric accepted the cancellation request, not confirmation that the job is Cancelled. " +
				"Use these IDs with jobs_get-item-job-instance in a separate call to check its status; do not automatically resubmit. " +
				"Cancellation does not guarantee rollback of work already performed." + firstReadGuidance(r)
			return response.SuccessMessage(http.StatusAccepted, guidance, struct {
				WorkspaceID        uuid.UUID `json:"workspaceId"`
				ItemID             uuid.UUID `json:"itemId"`
				JobInstanceID      uuid.UUID `json:"jobInstanceId"`
				RetryAfterSeconds  *int64    `json:"retryAfterSeconds,omitempty"`
				RetryAfterUtc      *netTime  `json:"retryAfterUtc,omitempty"`
				RetryAfterIssue    *string   `json:"retryAfterIssue,omitempty"`
				CancellationStatus string    `json:"cancellationStatus"`
				Guidance           string    `json:"guidance"`
			}{ws, item, job, r.seconds(), r.utc(), retryIssue(r), "Requested", guidance}), nil, nil
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			discard(resp)
			f = &failure{kind: kHTTP, status: http.StatusBadGateway, cls: "cancelRequest", retry: r}
		default:
			f = &failure{kind: kHTTP, status: resp.StatusCode, cls: "cancelRequest", retry: r}
			f.code, f.metaUnavailable = readErrorCode(resp)
		}
	}
	status := baseStatus(f)
	if f.kind == kCanceled {
		status = http.StatusRequestTimeout
	}
	const permission = "Access denied. The caller needs item execution permission and delegated Item.Execute.All or the item-specific Execute.All scope"
	var msg string
	switch {
	case f.cls == "cancelRequest" && f.code == "JobAlreadyCompleted":
		msg = "Fabric returned JobAlreadyCompleted. Cancellation was not accepted because the job has already completed; this does not establish its final success or failure status"
	case f.cls == "cancelRequest" && f.code == "InsufficientPrivileges":
		msg = permission
	case f.cls == "cancelRequest" && f.code == "ItemNotFound":
		msg = "The Fabric item was not found or is not accessible to the caller"
	default:
		msg = messages{
			http.StatusBadRequest:      "Fabric rejected the cancellation request. Check the workspace, item, and job-instance UUIDs",
			http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
			http.StatusForbidden:       permission,
			http.StatusNotFound:        "The Fabric item or job instance was not found or is not accessible to the caller",
			http.StatusTooManyRequests: "Fabric throttled this cancellation request",
			http.StatusRequestTimeout:  "The cancellation request was interrupted or timed out; its outcome may be uncertain",
			http.StatusGatewayTimeout:  "The cancellation request was interrupted or timed out; its outcome may be uncertain",
			0:                          "Fabric job cancellation was not confirmed; its outcome may be uncertain",
		}.get(status)
	}
	msg += ". Check the known job with jobs_get-item-job-instance in a separate call before considering another cancellation request. No automatic retry was attempted. Canceling the MCP request itself does not cancel the Fabric job"
	if f.cls == "cancelRequest" {
		if f.metaUnavailable {
			msg += ". Additional Fabric error metadata was unavailable"
		}
		if f.retry != nil {
			msg += f.retry.requestGuidance()
		}
	}
	return fail(status, msg), nil, nil
}

// --- run-on-demand-item-job ---

// jobRunBody ports ItemJobRunInput.CreateBody.
func jobRunBody(executionData, jobParameters *string) ([]byte, error) {
	if executionData == nil && jobParameters == nil {
		return nil, nil
	}
	exec, err := parseJSONObject(executionData, "--execution-data", maxJSONBytes)
	if err != nil {
		return nil, err
	}
	params, err := parseJSONArray(jobParameters, "--job-parameters", maxJSONBytes)
	if err != nil {
		return nil, err
	}
	if params != nil {
		var list []json.RawMessage
		_ = json.Unmarshal(params, &list)
		names := map[string]bool{}
		for _, p := range list {
			var obj map[string]json.RawMessage
			var name, typ string
			if json.Unmarshal(p, &obj) != nil || obj == nil || json.Unmarshal(obj["name"], &name) != nil ||
				json.Unmarshal(obj["type"], &typ) != nil || obj["value"] == nil {
				return nil, itemInputError("--job-parameters requires objects with string name and type fields and a value field.")
			}
			if utf16Len(name) > 256 || names[strings.ToLower(name)] {
				return nil, itemInputError("--job-parameters names must be case-insensitively unique and at most 256 characters.")
			}
			names[strings.ToLower(name)] = true
		}
	}
	body := map[string]json.RawMessage{}
	if exec != nil {
		body["executionData"] = exec
	}
	if params != nil {
		body["parameters"] = params
	}
	b, _ := json.Marshal(body)
	if len(b) > maxJSONBytes {
		return nil, itemInputError("The serialized job request must not exceed the 1 MiB (1,048,576 byte) tool budget.")
	}
	return b, nil
}

// submittedJobInstance ports GetSubmittedJobInstanceId.
func (a *Area) submittedJobInstance(h http.Header, ws, item uuid.UUID) (*uuid.UUID, *string) {
	issue := func(s string) (*uuid.UUID, *string) { return nil, &s }
	values := h.Values("Location")
	if len(values) == 0 {
		return issue("MissingLocation")
	}
	if len(values) != 1 {
		return issue("MultipleLocations")
	}
	v := values[0]
	if v == "" || len(v) > 4096 || strings.ContainsAny(v, `\%`) || strings.Contains(v, "/./") ||
		strings.Contains(v, "/../") || strings.HasPrefix(v, "//") {
		return issue("InvalidLocation")
	}
	for _, r := range v {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return issue("InvalidLocation")
		}
	}
	origin, _ := url.Parse(a.base)
	var loc *url.URL
	var err error
	if strings.HasPrefix(v, "/") {
		loc, err = origin.Parse(v)
	} else {
		loc, err = url.Parse(v)
	}
	if err != nil || !loc.IsAbs() || loc.Scheme != origin.Scheme || !strings.EqualFold(loc.Hostname(), origin.Hostname()) ||
		port(loc) != port(origin) || loc.User != nil || loc.Fragment != "" {
		return issue("InvalidLocation")
	}
	collection := origin.Path + "/workspaces/" + ws.String() + "/items/" + item.String() + "/jobs/instances"
	if strings.EqualFold(loc.Path, collection) {
		return issue("CollectionLocation")
	}
	if loc.RawQuery != "" || len(loc.Path) <= len(collection)+1 || !strings.EqualFold(loc.Path[:len(collection)+1], collection+"/") {
		return issue("InvalidLocation")
	}
	rest := loc.Path[len(collection)+1:]
	id, ok := parseID(rest)
	if !ok || len(rest) != 36 {
		return issue("InvalidLocation")
	}
	return &id, nil
}

func (a *Area) runOnDemandItemJob(ctx context.Context, _ *mcp.CallToolRequest, in runOnDemandItemJobInput) (*mcp.CallToolResult, any, error) {
	ws, item, errs := jobContextErrors(in.WorkspaceID, in.ItemID, in.JobType)
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	body, err := jobRunBody(in.ExecutionData, in.JobParameters)
	var resp *http.Response
	if err == nil {
		var payload any
		if body != nil {
			payload = body
		}
		resp, _, err = a.send(ctx, http.MethodPost, jobPath(ws, item, "/"+escapeData(in.JobType)+"/instances"), payload)
	}
	if err == nil {
		discard(resp)
		r := parseRetryAfter(resp.Header)
		if resp.StatusCode == http.StatusAccepted {
			id, issue := a.submittedJobInstance(resp.Header, ws, item)
			guidance := "Fabric accepted the submission, not its completion. Use the returned IDs with jobs_get-item-job-instance in a separate call; do not resubmit."
			if id == nil {
				guidance = "Fabric accepted the submission but did not return a trustworthy job-instance ID. Inspect the item's job history in Fabric; do not resubmit to recover an ID."
			}
			guidance += firstReadGuidance(r)
			return response.SuccessMessage(http.StatusAccepted, guidance, struct {
				WorkspaceID       uuid.UUID  `json:"workspaceId"`
				ItemID            uuid.UUID  `json:"itemId"`
				JobType           string     `json:"jobType"`
				JobInstanceID     *uuid.UUID `json:"jobInstanceId,omitempty"`
				RetryAfterSeconds *int64     `json:"retryAfterSeconds,omitempty"`
				RetryAfterUtc     *netTime   `json:"retryAfterUtc,omitempty"`
				TrackingIssue     *string    `json:"trackingIssue,omitempty"`
				RetryAfterIssue   *string    `json:"retryAfterIssue,omitempty"`
				SubmissionStatus  string     `json:"submissionStatus"`
				Guidance          string     `json:"guidance"`
			}{ws, item, in.JobType, id, r.seconds(), r.utc(), issue, retryIssue(r), "Accepted", guidance}), nil, nil
		}
		e := httpError(nonSuccess(resp.StatusCode), "Fabric job submission was not confirmed.")
		e.cls, e.retry = "runRequest", r
		err = e
	}
	f := asFailure(err)
	if f.kind == kInput {
		return fail(http.StatusBadRequest, f.msg), nil, nil
	}
	status := baseStatus(f)
	if f.kind == kCanceled {
		status = http.StatusRequestTimeout
	}
	msg := messages{
		http.StatusBadRequest:      "Fabric rejected the job request. Check the job type and this item's execution-data and parameter support",
		http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
		http.StatusForbidden:       "Access denied. The caller needs item execution permission and delegated Item.Execute.All or the item-specific Execute.All scope",
		http.StatusNotFound:        "The Fabric workspace or item was not found, or the caller does not have access",
		http.StatusTooManyRequests: "Fabric throttled this job submission",
		http.StatusRequestTimeout:  "The job submission was canceled or timed out",
		http.StatusGatewayTimeout:  "The job submission was canceled or timed out",
		0:                          "Fabric job submission was not confirmed",
	}.get(status)
	msg += ". A job may have started; verify the item's job history before any new submission. Do not retry blindly. No automatic retry or parameter removal was attempted"
	if f.cls == "runRequest" && f.retry != nil {
		msg += f.retry.requestGuidance()
	}
	return fail(status, msg), nil, nil
}

// --- schedules ---

var timestampRE = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d{1,7})?(Z|[+-]\d{2}:\d{2})?$`)

// readTimestamp ports FabricItemScheduleJson.TryReadTimestamp: offset-less
// values are UTC.
func readTimestamp(s string) (time.Time, bool) {
	if !timestampRE.MatchString(s) {
		return time.Time{}, false
	}
	layout := "2006-01-02T15:04:05.9999999"
	if strings.HasSuffix(s, "Z") || strings.LastIndexAny(s, "+-") > 10 {
		layout += "Z07:00"
	}
	t, err := time.Parse(layout, s)
	return t, err == nil
}

const invalidScheduleMetadata = "Fabric returned invalid schedule metadata."

// scheduleObject ports FabricItemScheduleJson.ValidateObject: an object
// with no property names that differ only in case.
func scheduleObject(raw json.RawMessage) (map[string]json.RawMessage, bool) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil || noCaseDuplicates(raw) != nil {
		return nil, false
	}
	return obj, true
}

// noCaseDuplicates rejects top-level property names that differ only in
// case.
func noCaseDuplicates(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return fmt.Errorf("not an object")
	}
	seen := map[string]bool{}
	for dec.More() {
		k, err := dec.Token()
		if err != nil {
			return err
		}
		key := strings.ToLower(k.(string))
		if seen[key] {
			return fmt.Errorf("duplicate")
		}
		seen[key] = true
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return err
		}
	}
	return nil
}

func scheduleString(obj map[string]json.RawMessage, name string) (string, bool) {
	var s string
	if json.Unmarshal(obj[name], &s) != nil || isBlank(s) {
		return "", false
	}
	return s, true
}

func scheduleInt(obj map[string]json.RawMessage, name string, min, max int64) bool {
	raw, ok := obj[name]
	if !ok {
		return false
	}
	n, err := strconv.ParseInt(string(raw), 10, 32)
	return err == nil && n >= min && n <= max
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func scheduleStrings(obj map[string]json.RawMessage, name string, max int, times bool) bool {
	var list []json.RawMessage
	if json.Unmarshal(obj[name], &list) != nil || len(list) == 0 || len(list) > max {
		return false
	}
	for _, e := range list {
		var s string
		if json.Unmarshal(e, &s) != nil || isBlank(s) || times && !hhmm.MatchString(s) {
			return false
		}
	}
	return true
}

// validScheduleConfiguration ports FabricItemScheduleJson.ValidateConfiguration.
func validScheduleConfiguration(raw json.RawMessage) bool {
	c, ok := scheduleObject(raw)
	if !ok {
		return false
	}
	typ, ok := scheduleString(c, "type")
	if !ok {
		return false
	}
	if _, ok := scheduleString(c, "localTimeZoneId"); !ok {
		return false
	}
	for _, k := range []string{"startDateTime", "endDateTime"} {
		s, ok := scheduleString(c, k)
		if !ok {
			return false
		}
		if _, ok := readTimestamp(s); !ok {
			return false
		}
	}
	switch typ {
	case "Cron":
		return scheduleInt(c, "interval", 1, 5270400)
	case "Daily":
		return scheduleStrings(c, "times", 100, true)
	case "Weekly":
		return scheduleStrings(c, "times", 100, true) && scheduleStrings(c, "weekdays", 7, false)
	case "Monthly":
		if !scheduleStrings(c, "times", 100, true) || !scheduleInt(c, "recurrence", 1, 12) {
			return false
		}
		o, ok := scheduleObject(c["occurrence"])
		if !ok {
			return false
		}
		ot, ok := scheduleString(o, "occurrenceType")
		if !ok {
			return false
		}
		switch ot {
		case "DayOfMonth":
			return scheduleInt(o, "dayOfMonth", 1, 31)
		case "OrdinalWeekday":
			_, ok1 := scheduleString(o, "weekIndex")
			_, ok2 := scheduleString(o, "weekday")
			return ok1 && ok2
		}
	}
	return true
}

// schedule ports FabricItemSchedule; JSON values are kept as returned.
type schedule struct {
	ID              uuid.UUID       `json:"id"`
	Enabled         bool            `json:"enabled"`
	CreatedDateTime *string         `json:"createdDateTime,omitempty"`
	Configuration   json.RawMessage `json:"configuration,omitempty"`
	Owner           json.RawMessage `json:"owner,omitempty"`
	ExecutionData   json.RawMessage `json:"executionData,omitempty"`
}

func nonNull(raw json.RawMessage) json.RawMessage {
	if raw == nil || string(raw) == "null" {
		return nil
	}
	return raw
}

// readSchedule ports FabricItemScheduleJson.Read.
func readSchedule(raw json.RawMessage) (*schedule, bool) {
	obj, ok := scheduleObject(raw)
	if !ok {
		return nil, false
	}
	if _, has := obj["id"]; !has {
		return nil, false
	}
	if _, has := obj["enabled"]; !has {
		return nil, false
	}
	s := &schedule{}
	if json.Unmarshal(obj["id"], &s.ID) != nil || s.ID == uuid.Nil || json.Unmarshal(obj["enabled"], &s.Enabled) != nil {
		return nil, false
	}
	if c := nonNull(obj["createdDateTime"]); c != nil {
		var created string
		if json.Unmarshal(c, &created) != nil {
			return nil, false
		}
		if _, ok := readTimestamp(created); !ok {
			return nil, false
		}
		s.CreatedDateTime = &created
	}
	s.Configuration = compactJSON(nonNull(obj["configuration"]))
	s.Owner = compactJSON(nonNull(obj["owner"]))
	s.ExecutionData = compactJSON(nonNull(obj["executionData"]))
	if s.ExecutionData != nil && !strings.HasPrefix(string(s.ExecutionData), "{") {
		return nil, false
	}
	if s.Configuration != nil && !validScheduleConfiguration(s.Configuration) {
		return nil, false
	}
	if s.Owner != nil {
		o, ok := scheduleObject(s.Owner)
		if !ok {
			return nil, false
		}
		var id string
		if json.Unmarshal(o["id"], &id) != nil || len(id) != 36 {
			return nil, false
		}
		if oid, ok := parseID(id); !ok || oid == uuid.Nil {
			return nil, false
		}
		if _, ok := scheduleString(o, "type"); !ok {
			return nil, false
		}
	}
	return s, true
}

func compactJSON(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	var b bytes.Buffer
	_ = json.Compact(&b, raw)
	return b.Bytes()
}

// readScheduleResponse ports FabricItemScheduleJson.ReadResponse.
func readScheduleResponse(b []byte) (*schedule, error) {
	if noDuplicateKeys(b) != nil || !depthOK(b, 64) {
		return nil, jsonError(invalidScheduleMetadata)
	}
	s, ok := readSchedule(b)
	if !ok || jsonDepth(s) > 60 {
		return nil, jsonError(invalidScheduleMetadata)
	}
	return s, nil
}

// scheduleBody ports ItemScheduleInput.CreateBody.
func scheduleBody(enabled bool, configuration string, executionData *string) ([]byte, error) {
	cfg, err := parseJSONObject(&configuration, "--configuration", maxJSONBytes)
	if err != nil {
		return nil, err
	}
	invalidJSON := itemInputError("Invalid schedule JSON. Supply valid Unicode and configuration with type, UTC start/end, localTimeZoneId, and the documented fields and limits for that type. Cron interval is in minutes, not a cron expression.")
	if !validScheduleConfiguration(cfg) {
		return nil, invalidJSON
	}
	var c map[string]json.RawMessage
	_ = json.Unmarshal(cfg, &c)
	start, _ := scheduleString(c, "startDateTime")
	end, _ := scheduleString(c, "endDateTime")
	st, _ := readTimestamp(start)
	et, _ := readTimestamp(end)
	_, so := st.Zone()
	_, eo := et.Zone()
	if so != 0 || eo != 0 || !et.After(st) {
		return nil, itemInputError("--configuration requires UTC startDateTime and endDateTime, with end later than start. Offset-less API examples are interpreted as UTC. No times are rewritten.")
	}
	exec, err := parseJSONObject(executionData, "--execution-data", maxJSONBytes)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"enabled": enabled, "configuration": cfg}
	if exec != nil {
		body["executionData"] = exec
	}
	b, _ := json.Marshal(body)
	if len(b) > maxJSONBytes {
		return nil, itemInputError("The serialized schedule request must not exceed the 1 MiB (1,048,576 byte) tool budget.")
	}
	return b, nil
}

type scheduleResult struct {
	WorkspaceID       uuid.UUID `json:"workspaceId"`
	ItemID            uuid.UUID `json:"itemId"`
	JobType           string    `json:"jobType"`
	Schedule          *schedule `json:"schedule"`
	RetryAfterSeconds *int64    `json:"retryAfterSeconds,omitempty"`
	RetryAfterUtc     *netTime  `json:"retryAfterUtc,omitempty"`
}

// scheduleMutation ports the request flow of schedule creation and update:
// failures before a success response propagate; a success whose body can't
// be read is "confirmed".
func (a *Area) scheduleMutation(ctx context.Context, method, path string, body []byte, okStatus int, excMsg string, id *uuid.UUID) (*schedule, *retryAfter, error) {
	resp, _, err := a.send(ctx, method, path, body)
	if err != nil {
		return nil, nil, err
	}
	r := parseRetryAfter(resp.Header)
	exc := func(status int) *failure {
		return &failure{kind: kHTTP, status: status, msg: excMsg, cls: "scheduleMutation", retry: r}
	}
	if resp.StatusCode != okStatus {
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			discard(resp)
			return nil, nil, exc(http.StatusBadGateway)
		}
		e := exc(resp.StatusCode)
		e.code, e.metaUnavailable = readErrorCode(resp)
		return nil, nil, e
	}
	confirmed := func(status int) *failure { e := exc(status); e.confirmed = true; return e }
	if !isJSON(resp) {
		discard(resp)
		return nil, nil, confirmed(http.StatusBadGateway)
	}
	b, err := readBody(resp, maxJSONBytes, func() error { return jsonError("The Fabric schedule exceeds the 1 MiB response limit.") })
	if err != nil {
		if ctx.Err() != nil {
			return nil, nil, confirmed(http.StatusRequestTimeout)
		}
		return nil, nil, confirmed(http.StatusBadGateway)
	}
	s, err := readScheduleResponse(b)
	if err != nil || id != nil && s.ID != *id {
		return nil, nil, confirmed(http.StatusBadGateway)
	}
	return s, r, nil
}

// scheduleMutationFail ports the GetErrorMessage of schedule creation and
// update.
func scheduleMutationFail(f *failure, create bool, confirmed, maybe, tail string, table messages) *mcp.CallToolResult {
	status := baseStatus(f)
	switch f.kind {
	case kInput:
		return fail(http.StatusBadRequest, f.msg)
	case kCanceled:
		status = http.StatusRequestTimeout
	}
	msg := table.get(status)
	if f.confirmed {
		msg = confirmed
	} else {
		msg += maybe
	}
	msg += tail
	if f.cls == "scheduleMutation" {
		if f.code != "" {
			msg += ". Fabric error code: " + f.code
			if create && f.code == "ScheduleExceedsLimit" {
				msg += ". Fabric enforces the schedule limit; no schedule was automatically deleted"
			}
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

func (a *Area) createItemSchedule(ctx context.Context, _ *mcp.CallToolRequest, in createItemScheduleInput) (*mcp.CallToolResult, any, error) {
	ws, item, errs := jobContextErrors(in.WorkspaceID, in.ItemID, in.JobType)
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	s, r, err := func() (*schedule, *retryAfter, error) {
		body, err := scheduleBody(*in.Enabled, in.Configuration, in.ExecutionData)
		if err != nil {
			return nil, nil, err
		}
		return a.scheduleMutation(ctx, http.MethodPost, jobPath(ws, item, "/"+escapeData(in.JobType)+"/schedules"), body,
			http.StatusCreated, "Fabric schedule creation did not return usable schedule metadata.", nil)
	}()
	if err != nil {
		return scheduleMutationFail(asFailure(err), true,
			"Fabric confirmed schedule creation (HTTP 201), but the returned schedule metadata could not be read or validated. Do not resubmit this creation; inspect existing schedules separately",
			". A schedule may have been created; verify existing schedules separately before any new creation. Do not retry blindly",
			". No automatic retry or parameter removal was attempted",
			messages{
				http.StatusBadRequest:      "Fabric rejected the schedule request. Check the job type, configuration, and execution-data support",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. Check Fabric permissions and delegated Item.Execute.All and Item.ReadWrite.All scopes",
				http.StatusNotFound:        "The Fabric workspace or item was not found, or the caller does not have access",
				http.StatusTooManyRequests: "Fabric throttled this schedule creation",
				http.StatusRequestTimeout:  "The schedule request was canceled or timed out",
				http.StatusGatewayTimeout:  "The schedule request was canceled or timed out",
				0:                          "Fabric schedule creation was not confirmed",
			}), nil, nil
	}
	const guidance = "Fabric confirmed schedule creation, not job execution or success. An enabled schedule with a past start time may trigger a job immediately. " +
		"Schedule IDs are not job-instance or operation IDs. No polling, result retrieval, or automatic retry was performed. Do not resubmit this creation."
	return response.SuccessMessage(http.StatusCreated, guidance, struct {
		scheduleResult
		CreationStatus string `json:"creationStatus"`
		Guidance       string `json:"guidance"`
	}{scheduleResult{ws, item, in.JobType, s, r.seconds(), r.utc()}, "Created", guidance}), nil, nil
}

func scheduleContextErrors(workspaceID, itemID, jobType, scheduleID string) (ws, item, id uuid.UUID, errs []string) {
	ws, item, errs = jobContextErrors(workspaceID, itemID, jobType)
	var ok bool
	if id, ok = parseID(scheduleID); !ok {
		errs = append(errs, "--schedule-id must be a nonempty UUID.")
	}
	return
}

func (a *Area) updateItemSchedule(ctx context.Context, _ *mcp.CallToolRequest, in updateItemScheduleInput) (*mcp.CallToolResult, any, error) {
	ws, item, id, errs := scheduleContextErrors(in.WorkspaceID, in.ItemID, in.JobType, in.ScheduleID)
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	s, r, err := func() (*schedule, *retryAfter, error) {
		body, err := scheduleBody(*in.Enabled, in.Configuration, in.ExecutionData)
		if err != nil {
			return nil, nil, err
		}
		return a.scheduleMutation(ctx, http.MethodPatch, jobPath(ws, item, "/"+escapeData(in.JobType)+"/schedules/"+id.String()), body,
			http.StatusOK, "Fabric schedule update did not return usable schedule metadata.", &id)
	}()
	if err != nil {
		return scheduleMutationFail(asFailure(err), false,
			"Fabric confirmed the schedule update (HTTP 200), but the returned schedule metadata could not be read or validated. Do not resubmit this update blindly; inspect the target schedule separately",
			". The schedule may have been updated; inspect the target schedule separately before another update. Do not retry blindly",
			". No automatic retry or parameter removal was attempted. Updating is not job execution; disabling does not cancel existing jobs",
			messages{
				http.StatusBadRequest:      "Fabric rejected the schedule update. Check the job type, full configuration, and execution-data support",
				http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
				http.StatusForbidden:       "Access denied. Check Fabric permissions and delegated Item.Execute.All and Item.ReadWrite.All scopes",
				http.StatusNotFound:        "The Fabric workspace, item, or schedule was not found, or the caller does not have access",
				http.StatusTooManyRequests: "Fabric throttled this schedule update",
				http.StatusRequestTimeout:  "The schedule update was canceled or timed out",
				http.StatusGatewayTimeout:  "The schedule update was canceled or timed out",
				0:                          "Fabric schedule update was not confirmed",
			}), nil, nil
	}
	const guidance = "Fabric confirmed the schedule update, not job execution or success. An enabled schedule with a past start time may trigger a job immediately. " +
		"Disabling a schedule does not cancel existing job instances. The schedule owner identifies its creator or last modifier, separately from item ownership. " +
		"Schedule IDs are not job-instance or operation IDs. No polling, result retrieval, or automatic retry was performed. Do not resubmit this update blindly."
	return response.SuccessMessage(http.StatusOK, guidance, struct {
		scheduleResult
		UpdateStatus string `json:"updateStatus"`
		Guidance     string `json:"guidance"`
	}{scheduleResult{ws, item, in.JobType, s, r.seconds(), r.utc()}, "Updated", guidance}), nil, nil
}

func (a *Area) getItemSchedule(ctx context.Context, _ *mcp.CallToolRequest, in getItemScheduleInput) (*mcp.CallToolResult, any, error) {
	ws, item, id, errs := scheduleContextErrors(in.WorkspaceID, in.ItemID, in.JobType, in.ScheduleID)
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	res, err := func() (*scheduleResult, error) {
		resp, _, err := a.send(ctx, http.MethodGet, jobPath(ws, item, "/"+escapeData(in.JobType)+"/schedules/"+id.String()), nil)
		if err != nil {
			return nil, err
		}
		r := parseRetryAfter(resp.Header)
		exc := func(status int) *failure {
			return &failure{kind: kHTTP, status: status, msg: "Unable to retrieve a usable Fabric item schedule snapshot.", cls: "scheduleGet", retry: r}
		}
		if resp.StatusCode != http.StatusOK {
			if resp.StatusCode >= 200 && resp.StatusCode < 300 {
				discard(resp)
				return nil, exc(http.StatusBadGateway)
			}
			e := exc(resp.StatusCode)
			e.code, e.metaUnavailable = readErrorCode(resp)
			return nil, e
		}
		if !isJSON(resp) {
			discard(resp)
			return nil, exc(http.StatusBadGateway)
		}
		b, err := readBody(resp, maxJSONBytes, func() error { return jsonError("too large") })
		if err != nil {
			if ctx.Err() != nil {
				return nil, exc(http.StatusRequestTimeout)
			}
			return nil, exc(http.StatusBadGateway)
		}
		s, err := readScheduleResponse(b)
		if err != nil || s.ID != id {
			return nil, exc(http.StatusBadGateway)
		}
		return &scheduleResult{ws, item, in.JobType, s, r.seconds(), r.utc()}, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		msg := messages{
			http.StatusBadRequest:      "Provide valid workspace, item, and schedule UUIDs and an explicit job type",
			http.StatusUnauthorized:    "Authentication failed. Sign in with an identity that can read the Fabric item",
			http.StatusForbidden:       "Access denied. The caller needs read permission for this Fabric item",
			http.StatusNotFound:        "The Fabric item, job type, or schedule was not found or is inaccessible. Check the explicit workspace, item, job type, and schedule",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric schedule lookup was canceled or timed out",
			http.StatusBadGateway:      "Fabric returned an unusable schedule snapshot. Expected matching schedule metadata as JSON up to 1 MiB, 64 raw nesting levels, and 60 retained levels to leave room for output envelopes",
			0:                          "Unable to retrieve the Fabric item schedule. Retry the read or check service availability",
		}.get(status)
		if f.cls == "scheduleGet" {
			if safeErrorCode(f.code) {
				msg += ". Fabric error code: " + f.code
			}
			if f.metaUnavailable {
				msg += ". Optional Fabric error metadata was unavailable"
			}
			if f.retry != nil {
				msg += f.retry.retryGuidance()
			}
		}
		return fail(status, msg), nil, nil
	}
	return response.Success(res), nil, nil
}

const invalidSchedulePage = "Fabric returned invalid schedule page metadata. No partial page was returned."

func (a *Area) listItemSchedules(ctx context.Context, _ *mcp.CallToolRequest, in listItemSchedulesInput) (*mcp.CallToolResult, any, error) {
	ws, item, errs := jobContextErrors(in.WorkspaceID, in.ItemID, in.JobType)
	if !validToken(in.ContinuationToken) {
		errs = append(errs, invalidTokenMessage)
	}
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	type result struct {
		Schedules         []*schedule `json:"schedules"`
		ContinuationToken *string     `json:"continuationToken,omitempty"`
		RetryAfterSeconds *int64      `json:"retryAfterSeconds,omitempty"`
		RetryAfterUtc     *netTime    `json:"retryAfterUtc,omitempty"`
	}
	path := jobPath(ws, item, "/"+escapeData(in.JobType)+"/schedules")
	res, err := func() (*result, error) {
		resp, _, err := a.send(ctx, http.MethodGet, path+query("continuationToken", optToken(in.ContinuationToken)), nil)
		if err != nil {
			return nil, err
		}
		r := parseRetryAfter(resp.Header)
		if resp.StatusCode != http.StatusOK {
			discard(resp)
			f := httpError(nonSuccess(resp.StatusCode), "Unable to retrieve the Fabric item schedules.")
			f.cls, f.retry = "scheduleList", r
			return nil, f
		}
		if !isJSON(resp) {
			discard(resp)
			return nil, jsonError("Fabric returned a non-JSON schedule page.")
		}
		b, err := readBody(resp, maxJSONBytes, func() error {
			return jsonError("The Fabric schedule page exceeds the 1 MiB (1,048,576 byte) limit. No partial page was returned.")
		})
		if err != nil {
			return nil, err
		}
		if noDuplicateKeys(b) != nil || !depthOK(b, 64) {
			return nil, jsonError(invalidSchedulePage)
		}
		page, ok := scheduleObject(b)
		if !ok {
			return nil, jsonError(invalidSchedulePage)
		}
		var values []json.RawMessage
		if raw, ok := page["value"]; !ok || json.Unmarshal(raw, &values) != nil || values == nil {
			return nil, jsonError(invalidSchedulePage)
		}
		collection, _ := url.Parse(a.base + path)
		tok, err := jobPageToken(page, collection, false, invalidSchedulePage)
		if err != nil {
			return nil, jsonError(invalidSchedulePage)
		}
		out := &result{Schedules: []*schedule{}, ContinuationToken: tok, RetryAfterSeconds: r.seconds(), RetryAfterUtc: r.utc()}
		for _, v := range values {
			s, ok := readSchedule(v)
			if !ok {
				return nil, jsonError(invalidSchedulePage)
			}
			out.Schedules = append(out.Schedules, s)
		}
		return out, nil
	}()
	if err != nil {
		f := asFailure(err)
		status := standardStatus(f)
		msg := messages{
			http.StatusBadRequest:      "Provide valid workspace and item UUIDs, an explicit job type, and an optional unchanged continuation token",
			http.StatusUnauthorized:    "Authentication failed. Sign in with an identity that can read the Fabric item",
			http.StatusForbidden:       "Access denied. The caller needs read permission for this Fabric item",
			http.StatusNotFound:        "The Fabric item or job type was not found. Check the workspace, item, and job type",
			http.StatusTooManyRequests: "Fabric throttled the request. Wait before retrying",
			http.StatusRequestTimeout:  "The Fabric schedule listing request was canceled or timed out",
			http.StatusBadGateway:      "Fabric returned an invalid schedule page. Expected schedule metadata and consistent continuation information as JSON up to 1 MiB and 64 nesting levels. No partial page was returned",
			0:                          "Unable to list Fabric item schedules. Retry the request or check service availability",
		}.get(status)
		if f.cls == "scheduleList" && f.retry != nil {
			msg += f.retry.retryGuidance()
		}
		return fail(status, msg), nil, nil
	}
	return response.Success(res), nil, nil
}

func (a *Area) deleteItemSchedule(ctx context.Context, _ *mcp.CallToolRequest, in deleteItemScheduleInput) (*mcp.CallToolResult, any, error) {
	ws, item, id, errs := scheduleContextErrors(in.WorkspaceID, in.ItemID, in.JobType, in.ScheduleID)
	if len(errs) > 0 {
		return invalid(errs), nil, nil
	}
	resp, sent, err := a.send(ctx, http.MethodDelete, jobPath(ws, item, "/"+escapeData(in.JobType)+"/schedules/"+id.String()), nil)
	var f *failure
	switch {
	case err != nil:
		e := asFailure(err)
		if e.kind == kAuth {
			return fail(http.StatusUnauthorized, "Authentication failed. Check the configured Fabric identity. No DELETE request was sent."), nil, nil
		}
		f = &failure{kind: kHTTP, cls: "scheduleDelete", notSent: !sent}
		switch e.kind {
		case kCanceled:
			f.status = http.StatusRequestTimeout
		case kTimeout:
			f.status = http.StatusGatewayTimeout
		default:
			f.status = baseStatus(e)
		}
	case resp.StatusCode == http.StatusOK:
		discard(resp)
		const guidance = "Fabric confirmed deletion of the requested schedule (HTTP 200). No schedule snapshot was returned. " +
			"Deleting a schedule does not cancel already-running jobs or roll back completed work. " +
			"Job cancellation requires a separate tool and separate authorization. " +
			"No follow-up request, polling, or automatic retry was performed. Do not resubmit this confirmed deletion."
		return response.SuccessMessage(http.StatusOK, guidance, map[string]any{
			"workspaceId": ws, "itemId": item, "jobType": in.JobType, "scheduleId": id, "deletionStatus": "Deleted", "guidance": guidance,
		}), nil, nil
	default:
		f = &failure{kind: kHTTP, cls: "scheduleDelete", status: nonSuccess(resp.StatusCode), retry: parseRetryAfter(resp.Header)}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			discard(resp)
		} else {
			f.code, f.metaUnavailable = readErrorCode(resp)
		}
	}
	var msg string
	if f.notSent {
		msg = "The schedule deletion request failed or was canceled before sending. No DELETE request was sent"
	} else {
		msg = messages{
			http.StatusBadRequest:      "Fabric rejected the schedule deletion. Check the explicit workspace, item, job type, and schedule",
			http.StatusUnauthorized:    "Authentication failed. Check the configured Fabric identity",
			http.StatusForbidden:       "Access denied. Check Fabric permissions and delegated Item.ReadWrite.All or the corresponding item-type ReadWrite.All scope",
			http.StatusNotFound:        "The Fabric workspace, item, job type, or schedule was not found or is inaccessible. This is not confirmed deletion",
			http.StatusTooManyRequests: "Fabric throttled this schedule deletion",
			0:                          "Schedule deletion was not confirmed; the outcome may be uncertain. Inspect the target with jobs_get-item-schedule or jobs_list-item-schedules separately before considering another deletion. Do not retry blindly",
		}.get(f.status)
	}
	msg += ". No automatic retry was attempted. Deleting a schedule does not cancel existing jobs or roll back completed work"
	if safeErrorCode(f.code) {
		msg += ". Fabric error code: " + f.code
	}
	if f.metaUnavailable {
		msg += ". Optional Fabric error metadata was unavailable"
	}
	if f.retry != nil {
		msg += f.retry.requestGuidance()
	}
	return fail(f.status, msg), nil, nil
}
