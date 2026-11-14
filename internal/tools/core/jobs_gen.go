package core

// Code generated from internal/parity/testdata/ref-tools.json; descriptions
// and annotations must match upstream verbatim. Edit by regenerating.

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/slachiewicz/fabric-mcp-go/internal/server"
)

type cancelItemJobInstanceInput struct {
	WorkspaceID   string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID        string `json:"item-id" jsonschema:"The ID of the Microsoft Fabric item whose job instance should be canceled. Must be a nonempty UUID."`
	JobInstanceID string `json:"job-instance-id" jsonschema:"The ID of the existing job instance to cancel, not a Fabric operation ID. Must be a nonempty UUID."`
}

func cancelItemJobInstanceTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "cancel-item-job-instance",
		Description: "Requests cancellation of one existing Microsoft Fabric item job using explicit workspace-id, item-id,\n" +
			"and job-instance-id nonempty UUIDs. This destructive action requires consent and item execution\n" +
			"permission; delegated callers need Item.Execute.All or the corresponding item-specific Execute.All scope.\n" +
			"Makes exactly one POST with no body or job-type. Never discovers jobs, retries, polls, waits, or fetches results.\n" +
			"Returns cancellationStatus Requested and the requested IDs when Fabric accepts cancellation, not a Cancelled job state.\n" +
			"Wait at least the supplied Retry-After guidance before calling jobs_get-item-job-instance separately.\n" +
			"Missing or invalid response metadata does not undo acceptance. JobAlreadyCompleted is a failure, not successful cancellation.\n" +
			"Repeated cancellation targets the same job, but may fail; no automatic retry is performed.\n" +
			"After an uncertain failure, check this known job with Get before considering another cancellation request.\n" +
			"Canceling the MCP request itself does not cancel the Fabric job. No rollback of completed work is promised.\n" +
			"Job-instance IDs are not Fabric operation IDs; do not use operation-state or operation-result tools.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Cancel Fabric Item Job Instance", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type getItemJobInstanceInput struct {
	WorkspaceID   string `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID        string `json:"item-id" jsonschema:"The ID of the Microsoft Fabric item whose job instance should be retrieved. Must be a nonempty UUID."`
	JobInstanceID string `json:"job-instance-id" jsonschema:"The ID of an existing job instance for this item, not a Fabric operation ID. Must be a nonempty UUID."`
}

func getItemJobInstanceTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-item-job-instance",
		Description: "Gets one existing Microsoft Fabric item's job instance using explicit workspace, item, and job-instance UUIDs.\n" +
			"Returns the job identity, type, status, invocation type, available UTC timing and trace fields,\n" +
			"safe failure metadata, and Retry-After guidance when supplied by Fabric.\n" +
			"HTTP success means the snapshot was read, not that the job completed successfully.\n" +
			"Makes one GET only: does not poll, wait, discover jobs or job types, retrieve results, run, cancel, or schedule jobs.\n" +
			"Job-instance IDs are not Fabric operation IDs; do not use the operation-state or operation-result tools for them.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Item Job Instance", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type listItemJobInstancesInput struct {
	WorkspaceID       string  `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID            string  `json:"item-id" jsonschema:"The ID of the Microsoft Fabric item whose job instances should be listed. Must be a nonempty UUID."`
	ContinuationToken *string `json:"continuation-token,omitempty" jsonschema:"The unchanged continuation token from a previous page for the same workspace and item. Omit for the first page. Must be valid Unicode, not blank, without control characters and at most 8,192 UTF-8 bytes. Do not supply a continuation URL."`
}

func listItemJobInstancesTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "list-item-job-instances",
		Description: "Lists one complete page of Microsoft Fabric job instances for explicit workspace and item UUIDs.\n" +
			"Returns job identities, types, statuses, invocation types, available UTC timing, safe failure metadata,\n" +
			"and any continuation token and Retry-After guidance. Call again explicitly with the same IDs and\n" +
			"unchanged token for another page. Makes one GET only, without per-job lookups or automatic pagination.\n" +
			"For most items, the API reports up to 100 recently completed instances; active instances have no corresponding limit.\n" +
			"This is not a page-size cap or a full-history guarantee. HTTP success does not mean the jobs succeeded.\n" +
			"Does not poll, retrieve results, run, cancel, or schedule jobs. A listed job cannot establish the\n" +
			"identity of an earlier ambiguous submission or one without a tracking ID. Cancellation requires separate authorization.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Fabric Item Job Instances", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
	}
}

type createItemScheduleInput struct {
	WorkspaceID   string  `json:"workspace-id" jsonschema:"The explicit ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID        string  `json:"item-id" jsonschema:"The explicit ID of the Microsoft Fabric item to schedule. Must be a nonempty UUID."`
	JobType       string  `json:"job-type" jsonschema:"The job type supported by this item. Required; no default or discovery. Encoded as one path component, within the 1,024 UTF-8 byte tool budget."`
	Enabled       *bool   `json:"enabled" jsonschema:"Explicit true or false; no default. Enabling a schedule can consume compute or change data, and a past startDateTime may trigger a job immediately. Confirm this timing before consenting."`
	Configuration string  `json:"configuration" jsonschema:"Required JSON configuration object with type, startDateTime, endDateTime, and localTimeZoneId. Supply explicit UTC bounds (end after start) and a Windows time zone ID; offset-less API examples are treated as UTC, never machine-local time. Cron requires interval in minutes (1-5270400), not a cron expression. Daily/Weekly/Monthly require their documented timing fields. Extensible types and fields are preserved. At most 1 MiB UTF-8 and 32 nesting levels; the complete serialized request also has a 1 MiB budget."`
	ExecutionData *string `json:"execution-data,omitempty" jsonschema:"Optional JSON object passed unchanged as executionData: fixed static, workload/job-type-defined inputs that may be sensitive. Omit to omit the field; no inference, stripping, or retry. At most 1 MiB UTF-8 and 32 nesting levels; the complete serialized request also has a 1 MiB budget."`
}

func createItemScheduleTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "create-item-schedule",
		Description: "Creates one Microsoft Fabric item schedule for explicit workspace-id and item-id UUIDs and job-type.\n" +
			"Requires explicit enabled (true or false) and configuration JSON with type, UTC start/end, and localTimeZoneId.\n" +
			"Before requesting consent, confirm the timing and enabled intent: a past start time may trigger a job immediately,\n" +
			"consuming compute or changing data. No timezone, time, cadence, or enabled defaults are inferred or rewritten.\n" +
			"Cron interval is in minutes, not a Unix cron expression; Daily, Weekly, and Monthly use their documented fields.\n" +
			"Optional execution-data is fixed static JSON defined by the item/job type; extensible settings remain intact.\n" +
			"Requires Fabric permissions and, for delegated callers, both Item.Execute.All and Item.ReadWrite.All.\n" +
			"Makes exactly one POST, without discovery, schedule-count preflight, automatic retry, parameter stripping,\n" +
			"polling, result retrieval, or a follow-up run. Fabric enforces its documented 20-schedule limit.\n" +
			"HTTP 201 returns the created schedule, including available configuration, owner, and execution data, which may be sensitive.\n" +
			"Creation is not proof of job execution or success. Schedule IDs are not job-instance or operation IDs.\n" +
			"After an uncertain failure, verify existing schedules separately before any new creation; never resubmit blindly.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Create Fabric Item Schedule", ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
		Meta: mcp.Meta{server.SecretHint: true},
	}
}

type deleteItemScheduleInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The explicit ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID      string `json:"item-id" jsonschema:"The explicit ID of the Microsoft Fabric item whose schedule is being deleted. Must be a nonempty UUID."`
	JobType     string `json:"job-type" jsonschema:"The job type supported by this item. Required; no default or discovery. Encoded as one path component, within the 1,024 UTF-8 byte tool budget."`
	ScheduleID  string `json:"schedule-id" jsonschema:"The explicit ID of the existing item schedule to delete. Must be a nonempty UUID, not a job-instance or operation ID. No lookup is performed."`
}

func deleteItemScheduleTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "delete-item-schedule",
		Description: "Deletes one existing Microsoft Fabric item schedule for explicit workspace-id, item-id, and schedule-id UUIDs and job-type.\n" +
			"This destructive action requires consent, Fabric permissions, and delegated Item.ReadWrite.All or the corresponding item-type ReadWrite.All scope.\n" +
			"Makes exactly one DELETE with no request body, schedule configuration, discovery, preflight, automatic retry, or follow-up verification.\n" +
			"Only HTTP 200 confirms deletion and returns deletionStatus Deleted with the requested context; no schedule snapshot is returned.\n" +
			"This is not a Fabric operation LRO. Never follows Location, polls, waits, retrieves results, executes a job, or cancels a job.\n" +
			"Deleting a schedule does not cancel already-running jobs or promise rollback. Cancellation requires a separate tool and authorization.\n" +
			"Repeated deletion targets the same schedule but may fail, including with 404; failures are not converted to successful deletion.\n" +
			"Canceling the MCP request does not undo a deletion. After an uncertain failure, inspect the target with Get or List Schedules\n" +
			"separately before considering another deletion; never retry blindly. A later read is not proof of which request changed it.\n" +
			"Schedule IDs are not job-instance or operation IDs.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Delete Fabric Item Schedule", ReadOnlyHint: false, IdempotentHint: true,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}

type getItemScheduleInput struct {
	WorkspaceID string `json:"workspace-id" jsonschema:"The explicit ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID      string `json:"item-id" jsonschema:"The explicit ID of the Microsoft Fabric item whose schedule is being read. Must be a nonempty UUID."`
	JobType     string `json:"job-type" jsonschema:"The job type supported by this item. Required; no default or discovery. Encoded as one path component, within the 1,024 UTF-8 byte tool budget."`
	ScheduleID  string `json:"schedule-id" jsonschema:"The explicit ID of the existing item schedule to read. Must be a nonempty UUID, not a job-instance or operation ID. No lookup is performed."`
}

func getItemScheduleTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "get-item-schedule",
		Description: "Gets one existing Microsoft Fabric item schedule for explicit workspace-id, item-id, and schedule-id UUIDs and job-type.\n" +
			"Returns the matching schedule ID and enabled state, plus available creation time, configuration, owner, execution data,\n" +
			"and Retry-After guidance. Configuration, owner, and execution data may be sensitive and require disclosure consent, even in read-only mode.\n" +
			"Requires Fabric item read permission and delegated Item.Read.All or Item.ReadWrite.All, or the corresponding item-type scope.\n" +
			"Preserves optional fields, extensible settings and owner types, and UTC timestamp text without timezone conversion.\n" +
			"Cron interval is in minutes, not a cron expression. Owner identifies the creator or last modifier, not necessarily the item owner.\n" +
			"Makes one GET only: no List or name lookup, job-type discovery, enrichment, pagination, polling, or automatic retries.\n" +
			"This snapshot is not job execution status or proof of a previous ambiguous create/update outcome.\n" +
			"Schedule IDs are not job-instance or operation IDs. Reading does not authorize updating, deleting, running, or canceling anything.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Get Fabric Item Schedule", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
		Meta: mcp.Meta{server.SecretHint: true},
	}
}

type listItemSchedulesInput struct {
	WorkspaceID       string  `json:"workspace-id" jsonschema:"The ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID            string  `json:"item-id" jsonschema:"The ID of the Microsoft Fabric item whose schedules should be listed. Must be a nonempty UUID."`
	JobType           string  `json:"job-type" jsonschema:"The explicit job type supported by the item, such as DefaultJob. No discovery is performed. Must be not blank, valid Unicode without control characters or dot segments, within the 1,024 UTF-8 byte tool budget."`
	ContinuationToken *string `json:"continuation-token,omitempty" jsonschema:"The unchanged continuation token from a previous page for the same workspace, item, and job type. Omit for the first page. Must be valid Unicode, not blank, without control characters and at most 8,192 UTF-8 bytes. Do not supply a continuation URL."`
}

func listItemSchedulesTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "list-item-schedules",
		Description: "Lists one complete page of Microsoft Fabric schedules for explicit workspace and item UUIDs and a job type.\n" +
			"Returns schedule IDs and enabled state, plus available creation time, configuration, owner, execution data,\n" +
			"continuation token, and Retry-After guidance. Owner and execution data can contain sensitive information.\n" +
			"Preserves extensible schedule settings and UTC timestamp text without local-time conversion.\n" +
			"Cron configuration interval is measured in minutes, not a Unix cron expression.\n" +
			"Call again explicitly with the same IDs, job type, and unchanged token for another page.\n" +
			"Makes one GET only, without name or job-type discovery, per-schedule lookups, or automatic pagination.\n" +
			"Does not create, update, delete, or execute schedules; run, cancel, or poll jobs; or predict the next run.\n" +
			"Schedule IDs are not job-instance or operation IDs. A listed enabled schedule does not prove a job ran or succeeded.",
		Annotations: &mcp.ToolAnnotations{
			Title: "List Fabric Item Schedules", ReadOnlyHint: true, IdempotentHint: true,
			DestructiveHint: boolPtr(false), OpenWorldHint: boolPtr(false),
		},
		Meta: mcp.Meta{server.SecretHint: true},
	}
}

type updateItemScheduleInput struct {
	WorkspaceID   string  `json:"workspace-id" jsonschema:"The explicit ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID        string  `json:"item-id" jsonschema:"The explicit ID of the Microsoft Fabric item whose schedule is being updated. Must be a nonempty UUID."`
	JobType       string  `json:"job-type" jsonschema:"The job type supported by this item. Required; no default or discovery. Encoded as one path component, within the 1,024 UTF-8 byte tool budget."`
	ScheduleID    string  `json:"schedule-id" jsonschema:"The explicit ID of the existing item schedule to update. Must be a nonempty UUID, not a job-instance or operation ID. No lookup is performed."`
	Enabled       *bool   `json:"enabled" jsonschema:"Explicit true or false; required even for PATCH, with no default. Enabling a schedule can consume compute or change data, and a past startDateTime may trigger a job immediately. Disabling does not cancel existing jobs. Confirm timing and enabled intent before consenting."`
	Configuration string  `json:"configuration" jsonschema:"Required full JSON configuration object, not a partial patch: type, startDateTime, endDateTime, and localTimeZoneId. Supply explicit UTC bounds (end after start) and a Windows time zone ID; offset-less API examples are treated as UTC, never machine-local time. Cron requires interval in minutes (1-5270400), not a cron expression. Daily/Weekly/Monthly require their documented timing fields. Extensible types and fields are preserved. At most 1 MiB UTF-8 and 32 nesting levels; the complete serialized request also has a 1 MiB budget. Read existing settings separately if needed; no Get/merge/PATCH is performed."`
	ExecutionData *string `json:"execution-data,omitempty" jsonschema:"Optional JSON object sent unchanged as executionData: fixed static, workload/job-type-defined inputs that may be sensitive. Omit to omit the field; the API does not document whether omission preserves or clears stored data. JSON null is not an object. No inference, stripping, or retry. At most 1 MiB UTF-8 and 32 nesting levels; the complete serialized request also has a 1 MiB budget."`
}

func updateItemScheduleTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "update-item-schedule",
		Description: "Updates one existing Microsoft Fabric item schedule for explicit workspace-id, item-id, and schedule-id UUIDs and job-type.\n" +
			"Requires both explicit enabled (true or false) and full configuration JSON, even though the API uses PATCH.\n" +
			"Supply type, UTC start/end, and localTimeZoneId. Read existing settings with List Schedules separately if needed;\n" +
			"this tool never fetches or merges them. No timezone, time, cadence, or enabled defaults are inferred or rewritten.\n" +
			"Before requesting consent, confirm timing, enabled intent, and the calling identity: a past start time may trigger\n" +
			"a job immediately, consuming compute or changing data. The schedule owner is the identity that created or last\n" +
			"modified it, separately from item ownership. Disabling a schedule does not cancel existing job instances.\n" +
			"Cron interval is in minutes, not a Unix cron expression; Daily, Weekly, and Monthly use their documented fields.\n" +
			"Optional execution-data is fixed static JSON defined by the item/job type; extensible settings remain intact.\n" +
			"Omission only omits executionData from the request; preservation or clearing of stored data is not documented.\n" +
			"Requires Fabric permissions and, for delegated callers, both Item.Execute.All and Item.ReadWrite.All.\n" +
			"Makes exactly one PATCH, without discovery, preflight, Get/merge/PATCH, automatic retry, parameter stripping,\n" +
			"polling, result retrieval, or job execution. HTTP 200 returns the updated schedule, not a Fabric operation LRO.\n" +
			"Configuration, owner, and execution data may be sensitive. Update success is not proof a job ran or succeeded.\n" +
			"Schedule IDs are not job-instance or operation IDs. After an uncertain failure, inspect the target schedule\n" +
			"separately before another update; never resubmit blindly.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Update Fabric Item Schedule", ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
		Meta: mcp.Meta{server.SecretHint: true},
	}
}

type runOnDemandItemJobInput struct {
	WorkspaceID   string  `json:"workspace-id" jsonschema:"The explicit ID of the Microsoft Fabric workspace containing the item. Must be a nonempty UUID."`
	ItemID        string  `json:"item-id" jsonschema:"The explicit ID of the Microsoft Fabric item to execute. Must be a nonempty UUID."`
	JobType       string  `json:"job-type" jsonschema:"The job type supported by this item. Required; no default or discovery. Encoded as one path component, with a tool budget of 1,024 UTF-8 bytes."`
	ExecutionData *string `json:"execution-data,omitempty" jsonschema:"Optional JSON object passed unchanged as executionData. Workload-specific content is opaque. Omit to omit the field. At most 1 MiB UTF-8 and 32 nesting levels; the serialized request also has a 1 MiB budget."`
	JobParameters *string `json:"job-parameters,omitempty" jsonschema:"Optional JSON array passed as the API parameters field. Each object requires name, type, and value; names must be case-insensitively unique and at most 256 characters. Types and values remain workload-specific. Omit to omit the field; unsupported parameters are never stripped or retried. At most 1 MiB UTF-8 and 32 nesting levels; the serialized request also has a 1 MiB budget."`
}

func runOnDemandItemJobTool() *mcp.Tool {
	return &mcp.Tool{
		Name: "run-on-demand-item-job",
		Description: "Submits one on-demand Microsoft Fabric item job with explicit workspace-id, item-id UUIDs and job-type.\n" +
			"May consume compute or change data. Requires item execution permission and, for delegated callers,\n" +
			"Item.Execute.All or the corresponding item-specific Execute.All scope.\n" +
			"Optional execution-data is a JSON object; job-parameters is a JSON parameter array, supported only by some item/job types.\n" +
			"Makes exactly one POST, never discovers jobs, retries, strips parameters, polls, waits, or fetches results.\n" +
			"Returns an Accepted submission receipt, not running or completed job status.\n" +
			"When a trustworthy jobInstanceId is returned, wait at least the supplied Retry-After guidance before\n" +
			"calling jobs_get-item-job-instance separately. Missing or invalid receipt headers leave acceptance intact,\n" +
			"with explicit tracking/timing limitations. Never blindly resubmit after an uncertain failure.\n" +
			"Job-instance IDs are not Fabric operation IDs; do not use operation-state or operation-result tools.",
		Annotations: &mcp.ToolAnnotations{
			Title: "Run Fabric On-Demand Item Job", ReadOnlyHint: false, IdempotentHint: false,
			DestructiveHint: boolPtr(true), OpenWorldHint: boolPtr(false),
		},
	}
}
