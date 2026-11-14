package core

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
)

// opStatus values port the FabricOperationStatus enum.
const (
	opAccepted                   = "Accepted"
	opAcceptedWithoutOperationID = "AcceptedWithoutOperationId"
	opPending                    = "Pending"
	opSucceeded                  = "Succeeded"
	opFailed                     = "Failed"
	opTrackingStopped            = "TrackingStopped"
	opResultUnavailable          = "ResultUnavailable"
)

type opIssue struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	HTTPStatus *int   `json:"httpStatus,omitempty"`
}

// receipt ports FabricOperationReceipt.
type receipt struct {
	Status                      string     `json:"status"`
	OperationID                 *uuid.UUID `json:"operationId,omitempty"`
	LastState                   *opState   `json:"lastState,omitempty"`
	ServerRetryAfterSeconds     *float64   `json:"serverRetryAfterSeconds,omitempty"`
	RecommendedPollAfterSeconds *float64   `json:"recommendedPollAfterSeconds,omitempty"`
	Issue                       *opIssue   `json:"issue,omitempty"`
}

func intPtr(v int) *int { return &v }

// toIssue ports FabricOperationErrors.ToIssue.
func toIssue(f *failure) *opIssue {
	code := "ReadFailed"
	if f.cls == "operation" {
		code = f.code
	}
	return &opIssue{Code: code, Message: opErrMessage(f), HTTPStatus: intPtr(opErrStatus(f))}
}

// --- runner ---

// waitOptions ports FabricOperationWaitOptions.
type waitOptions struct {
	sync    bool
	maxWait float64
	early   bool
}

func validBudget(s float64) bool {
	return !math.IsInf(s, 0) && !math.IsNaN(s) && s > 0 && s <= maximumWait
}

// opContract ports FabricOperationContract: the synchronous success
// statuses and the reader of a required JSON result (nil when the result
// is documented empty).
type opContract[T any] struct {
	completed map[int]bool
	read      func(json.RawMessage) (T, error)
	max       int64
}

// runOperation ports FabricOperationRunner.HandleAsync. The result is nil
// unless the operation completed and its result was read.
func runOperation[T any](ctx context.Context, a *Area, resp *http.Response, c opContract[T], w waitOptions) (*T, *receipt, error) {
	if c.max == 0 {
		c.max = maxJSONBytes
	}
	if resp.StatusCode != http.StatusAccepted {
		if !c.completed[resp.StatusCode] {
			return nil, nil, unexpected(resp)
		}
		completed := receipt{Status: opSucceeded, OperationID: operationID(resp.Header)}
		body, err := readOpJSON(resp, completed.OperationID, c.max)
		if err == nil {
			var res *T
			if res, err = readOpResult(body, c); err == nil {
				return res, nil, nil
			}
		}
		if ctx.Err() != nil {
			return nil, nil, canceledWait(completed)
		}
		f := asFailure(err)
		return nil, readStopped(completed, toIssue(f)), nil
	}
	discard(resp)

	started := time.Now()
	id := operationID(resp.Header)
	server := retryAfterSeconds(parseRetryAfter(resp.Header))
	next := pollDelay(server, w.early)
	rc := receipt{Status: opAccepted, OperationID: id, ServerRetryAfterSeconds: server, RecommendedPollAfterSeconds: f64(next)}
	if id == nil {
		rc.Status = opAcceptedWithoutOperationID
		rc.RecommendedPollAfterSeconds = nil
		rc.Issue = &opIssue{Code: "MissingOperationId",
			Message: "Fabric accepted the mutation but returned no usable operation ID. Completion is unconfirmed. Check whether the mutation completed; do not automatically resubmit."}
		return nil, &rc, nil
	}
	if !w.sync {
		return nil, &rc, nil
	}

	remaining := func() float64 { return w.maxWait - time.Since(started).Seconds() }
	fetchResult := false
	for {
		if ctx.Err() != nil {
			return nil, nil, canceledWait(rc)
		}
		if !delayWithinBudget(ctx, next, remaining) {
			if ctx.Err() != nil {
				return nil, nil, canceledWait(rc)
			}
			return nil, budgetExpired(rc), nil
		}
		left := remaining()
		if left <= 0 {
			return nil, budgetExpired(rc), nil
		}
		rctx, cancel := context.WithTimeout(ctx, time.Duration(left*float64(time.Second)))
		var err error
		if fetchResult {
			var res *opResult
			if res, err = a.getOperationResult(rctx, *id, c.max); err == nil {
				var out *T
				if out, err = readOpResult(res, c); err == nil {
					cancel()
					rc.Status = opSucceeded
					rc.RecommendedPollAfterSeconds = nil
					rc.Issue = nil
					return out, &rc, nil
				}
			}
		} else {
			var st *stateResult
			if st, err = a.getOperationState(rctx, *id); err == nil {
				rc.LastState = &st.State
				rc.ServerRetryAfterSeconds = st.ServerRetryAfterSeconds
				rc.RecommendedPollAfterSeconds = f64(st.RecommendedPollAfterSeconds)
				rc.Issue = nil
				switch st.State.Status {
				case "Failed":
					cancel()
					rc.Status = opFailed
					rc.RecommendedPollAfterSeconds = nil
					rc.Issue = &opIssue{Code: "OperationFailed",
						Message:    "Fabric reports that the operation failed. Inspect the safe error code and request ID; the mutation was not resubmitted.",
						HTTPStatus: intPtr(http.StatusBadGateway)}
					return nil, &rc, nil
				case "Succeeded":
					rc.Status = opSucceeded
					if c.read == nil {
						cancel()
						rc.RecommendedPollAfterSeconds = nil
						return nil, &rc, nil
					}
					fetchResult = true
					next = 0
					if st.ServerRetryAfterSeconds != nil {
						next = pollDelay(st.ServerRetryAfterSeconds, false)
					}
				default:
					// Undefined and future statuses are not evidence of completion.
					rc.Status = opPending
					next = st.RecommendedPollAfterSeconds
				}
			}
		}
		cancel()
		if err == nil {
			continue
		}
		if ctx.Err() != nil {
			return nil, nil, canceledWait(rc)
		}
		f := asFailure(err)
		switch {
		case f.cls == "throttled" || f.cls == "operation" && (f.status == http.StatusTooManyRequests || f.status == http.StatusServiceUnavailable):
			server = retryAfterSeconds(f.retry)
			next = pollDelay(server, false)
			rc.ServerRetryAfterSeconds = server
			rc.RecommendedPollAfterSeconds = f64(next)
			rc.Issue = toIssue(f)
		case f.kind == kCanceled:
			if remaining() <= 0 {
				return nil, budgetExpired(rc), nil
			}
			return nil, readStopped(rc, &opIssue{Code: "ReadTimedOut",
				Message:    "The operation read timed out before the wait budget expired. Resume using the operation ID; the Fabric operation was not canceled.",
				HTTPStatus: intPtr(http.StatusGatewayTimeout)}), nil
		default:
			return nil, readStopped(rc, toIssue(f)), nil
		}
	}
}

func readOpResult[T any](r *opResult, c opContract[T]) (*T, error) {
	if c.read != nil {
		if !r.HasBody {
			return nil, opFailure("MissingResult", "The operation completed but Fabric did not return its required JSON result.", 0, nil)
		}
		v, err := c.read(r.Value)
		if err != nil {
			return nil, err
		}
		return &v, nil
	}
	if r.HasBody {
		return nil, opFailure("UnexpectedResult", "Fabric returned a body for an operation whose documented synchronous result is empty.", 0, nil)
	}
	return nil, nil
}

func delayWithinBudget(ctx context.Context, delay float64, remaining func() float64) bool {
	for delay > 0 {
		left := remaining()
		if left <= 0 {
			return false
		}
		d := math.Min(delay, left)
		t := time.NewTimer(time.Duration(d * float64(time.Second)))
		select {
		case <-ctx.Done():
			t.Stop()
			return false
		case <-t.C:
		}
		delay -= d
	}
	return remaining() > 0
}

func readStopped(r receipt, issue *opIssue) *receipt {
	if r.Status == opSucceeded {
		r.Status = opResultUnavailable
		i := *issue
		i.Message = "The operation completed, but its result was not retrieved. " + strings.TrimRight(issue.Message, ".") + ". Do not resubmit the original mutation."
		r.Issue = &i
	} else {
		r.Status = opTrackingStopped
		r.Issue = issue
	}
	return &r
}

func budgetExpired(r receipt) *receipt {
	if r.Status == opSucceeded {
		r.Status = opResultUnavailable
		r.Issue = &opIssue{Code: "WaitBudgetExpired", Message: "The operation succeeded, but its result was not retrieved within the local wait budget. Resume with get-operation-result; do not resubmit the mutation."}
	} else {
		r.Status = opPending
		r.Issue = &opIssue{Code: "WaitBudgetExpired", Message: "The local wait budget expired. Completion remains unconfirmed. Resume with get-operation-state; stopping this wait did not cancel Fabric's operation."}
	}
	return &r
}

// canceledWait ports FabricOperationCanceledException.
func canceledWait(r receipt) *failure {
	return &failure{kind: kCanceled, msg: "Waiting was canceled by the caller. The Fabric operation was not canceled.",
		op: readStopped(r, &opIssue{Code: "CallerCanceled",
			Message:    "The caller canceled the local wait, not the Fabric operation. Resume using the operation ID; do not resubmit the mutation.",
			HTTPStatus: intPtr(http.StatusRequestTimeout)})}
}
