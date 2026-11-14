package core_test

import (
	"testing"
)

const (
	job1  = "88888888-8888-8888-8888-888888888888"
	sched = "99999999-9999-9999-9999-999999999999"
)

func TestJobInstances(t *testing.T) {
	jobs := "/v1/workspaces/" + ws1 + "/items/" + item1 + "/jobs"
	inst := `{"id":"` + job1 + `","itemId":"` + item1 + `","jobType":"RunNotebook","invokeType":"Manual","status":"Failed",` +
		`"startTimeUtc":"2024-01-01T00:00:00","failureReason":{"errorCode":"Bad Code","requestId":"` + op1 + `","isRetriable":false}}`
	instOut := `{"id":"` + job1 + `","itemId":"` + item1 + `","jobType":"RunNotebook","invokeType":"Manual","status":"Failed",` +
		`"startTimeUtc":"2024-01-01T00:00:00","failureReason":{"requestId":"` + op1 + `","isRetriable":false}}`
	last, h := fake(t, map[string]reply{
		"POST " + jobs + "/Run Notebook/instances":        {202, map[string]string{"Location": "https://HOST/v1/workspaces/" + ws1 + "/items/" + item1 + "/jobs/instances/" + job1, "Retry-After": "60"}, ""},
		"POST " + jobs + "/Other/instances":               {202, map[string]string{"Location": "/v1/workspaces/" + ws1 + "/items/" + item1 + "/jobs/instances/" + job1}, ""},
		"GET " + jobs + "/instances/" + job1:              {200, nil, inst},
		"GET " + jobs + "/instances":                      {200, nil, `{"value":[` + inst + `]}`},
		"POST " + jobs + "/instances/" + job1 + "/cancel": {400, nil, `{"errorCode":"JobAlreadyCompleted"}`},
	})
	cs := session(t, staticCred{}, h)

	env, _ := call(t, cs, "jobs_run-on-demand-item-job", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "Run Notebook",
		"execution-data": `{"parameters":{"a":1}}`, "job-parameters": `[{"name":"p","type":"Text","value":"v"}]`})
	// The fake's host differs from the API base, so the Location is not trusted.
	expect(t, env, 202, "Fabric accepted the submission but did not return a trustworthy job-instance ID. Inspect the item's job history in Fabric; do not resubmit to recover an ID. Wait at least 60 seconds before the first status read.")
	res := env["results"].(map[string]any)
	if res["trackingIssue"] != "InvalidLocation" || res["retryAfterSeconds"] != 60.0 || res["submissionStatus"] != "Accepted" {
		t.Errorf("results = %s", jsonOf(res))
	}
	if !sameJSON(last().body, `{"executionData":{"parameters":{"a":1}},"parameters":[{"name":"p","type":"Text","value":"v"}]}`) {
		t.Errorf("body = %s", jsonOf(last().body))
	}
	env, _ = call(t, cs, "jobs_run-on-demand-item-job", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "Other"})
	expect(t, env, 202, "Fabric accepted the submission, not its completion. Use the returned IDs with jobs_get-item-job-instance in a separate call; do not resubmit. No valid Retry-After guidance was supplied; the first-read delay is unknown.")
	if res := env["results"].(map[string]any); res["jobInstanceId"] != job1 || res["retryAfterIssue"] != "MissingOrInvalidRetryAfter" {
		t.Errorf("results = %s", jsonOf(res))
	}
	if last().body != nil {
		t.Errorf("body = %s, want none", jsonOf(last().body))
	}
	env, _ = call(t, cs, "jobs_run-on-demand-item-job", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "..",
		"job-parameters": `[{"name":"p","type":"Text","value":1},{"name":"P","type":"Text","value":2}]`})
	expect(t, env, 400, "--job-type must be nonempty and not whitespace, valid Unicode without control characters or dot segments, within the 1,024 UTF-8 byte tool budget.")
	env, _ = call(t, cs, "jobs_run-on-demand-item-job", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "x",
		"job-parameters": `[{"name":"p","type":"Text","value":1},{"name":"P","type":"Text","value":2}]`})
	expect(t, env, 400, "--job-parameters names must be case-insensitively unique and at most 256 characters."+help)

	env, _ = call(t, cs, "jobs_get-item-job-instance", map[string]any{"workspace-id": ws1, "item-id": item1, "job-instance-id": job1})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"jobInstance":`+instOut+`}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "jobs_list-item-job-instances", map[string]any{"workspace-id": ws1, "item-id": item1})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"jobInstances":[`+instOut+`]}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "jobs_cancel-item-job-instance", map[string]any{"workspace-id": ws1, "item-id": item1, "job-instance-id": job1})
	expect(t, env, 400, "Fabric returned JobAlreadyCompleted. Cancellation was not accepted because the job has already completed; this does not establish its final success or failure status. "+
		"Check the known job with jobs_get-item-job-instance in a separate call before considering another cancellation request. No automatic retry was attempted. Canceling the MCP request itself does not cancel the Fabric job"+help)
	env, _ = call(t, cs, "jobs_get-item-job-instance", map[string]any{"workspace-id": ws1, "item-id": item1})
	expect(t, env, 400, "Missing Required options: --job-instance-id")
}

func TestSchedules(t *testing.T) {
	schedules := "/v1/workspaces/" + ws1 + "/items/" + item1 + "/jobs/RunNotebook/schedules"
	cfg := `{"type":"Cron","startDateTime":"2030-01-01T00:00:00","endDateTime":"2030-02-01T00:00:00Z","localTimeZoneId":"UTC","interval":60}`
	s := `{"id":"` + sched + `","enabled":false,"createdDateTime":"2026-11-14T10:00:00.123Z","configuration":` + cfg + `,"owner":{"id":"` + op1 + `","type":"User"}}`
	last, h := fake(t, map[string]reply{
		"POST " + schedules:                 {201, nil, s},
		"GET " + schedules + "/" + sched:    {200, nil, s},
		"GET " + schedules:                  {200, nil, `{"value":[` + s + `]}`},
		"PATCH " + schedules + "/" + sched:  {400, nil, `{"errorCode":"InvalidSchedule"}`},
		"DELETE " + schedules + "/" + sched: {200, nil, ""},
	})
	cs := session(t, staticCred{}, h)

	env, _ := call(t, cs, "jobs_create-item-schedule", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "RunNotebook", "enabled": false, "configuration": cfg})
	expect(t, env, 201, "Fabric confirmed schedule creation, not job execution or success. An enabled schedule with a past start time may trigger a job immediately. "+
		"Schedule IDs are not job-instance or operation IDs. No polling, result retrieval, or automatic retry was performed. Do not resubmit this creation.")
	if !sameJSON(last().body, `{"enabled":false,"configuration":`+cfg+`}`) {
		t.Errorf("body = %s", jsonOf(last().body))
	}
	if res := env["results"].(map[string]any); !sameJSON(res["schedule"], s) || res["jobType"] != "RunNotebook" {
		t.Errorf("results = %s", jsonOf(res))
	}
	bad := `{"type":"Cron","startDateTime":"2030-01-01T00:00:00+01:00","endDateTime":"2030-02-01T00:00:00Z","localTimeZoneId":"UTC","interval":60}`
	env, _ = call(t, cs, "jobs_create-item-schedule", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "RunNotebook", "enabled": true, "configuration": bad})
	expect(t, env, 400, "--configuration requires UTC startDateTime and endDateTime, with end later than start. Offset-less API examples are interpreted as UTC. No times are rewritten."+help)
	env, _ = call(t, cs, "jobs_create-item-schedule", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "RunNotebook", "configuration": cfg})
	expect(t, env, 400, "Missing Required options: --enabled")

	env, _ = call(t, cs, "jobs_get-item-schedule", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "RunNotebook", "schedule-id": sched})
	expect(t, env, 200, "Success")
	env, _ = call(t, cs, "jobs_list-item-schedules", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "RunNotebook"})
	expect(t, env, 200, "Success")
	if !sameJSON(env["results"], `{"schedules":[`+s+`]}`) {
		t.Errorf("results = %s", jsonOf(env["results"]))
	}
	env, _ = call(t, cs, "jobs_update-item-schedule", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "RunNotebook", "schedule-id": sched, "enabled": true, "configuration": cfg})
	expect(t, env, 400, "Fabric rejected the schedule update. Check the job type, full configuration, and execution-data support. "+
		"The schedule may have been updated; inspect the target schedule separately before another update. Do not retry blindly. "+
		"No automatic retry or parameter removal was attempted. Updating is not job execution; disabling does not cancel existing jobs. Fabric error code: InvalidSchedule"+help)
	env, _ = call(t, cs, "jobs_delete-item-schedule", map[string]any{"workspace-id": ws1, "item-id": item1, "job-type": "RunNotebook", "schedule-id": sched})
	expect(t, env, 200, "")
	if res := env["results"].(map[string]any); res["deletionStatus"] != "Deleted" || res["scheduleId"] != sched {
		t.Errorf("results = %s", jsonOf(res))
	}
}
