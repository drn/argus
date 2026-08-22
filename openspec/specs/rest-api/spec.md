# REST API

## Purpose

The REST API exposes the Argus daemon's tasks, terminal sessions, configuration, git status, and uploads over HTTP so mobile devices and external scripts can drive the daemon remotely. It is designed for single-user remote control over Tailscale: it refuses to listen on untrusted LANs, authenticates every API request with a bearer token, and reserves destructive or configuration-mutating operations for the master token while allowing per-device tokens to perform per-task operations.
## Requirements
### Requirement: Network binding refuses untrusted networks

The server SHALL bind to the loopback address (`127.0.0.1`) and, when a Tailscale CGNAT address is detected, additionally bind to that address. It SHALL NEVER bind to `0.0.0.0`. Loopback binding is mandatory; if it cannot bind within the retry window the server SHALL return an error. Tailscale binding is best-effort: failure to detect or bind the Tailscale address SHALL leave the server running on loopback only rather than aborting startup.

#### Scenario: Loopback bind succeeds on the requested port

- **WHEN** the requested port is free
- **THEN** the server binds `127.0.0.1` on that port and reports the port it bound

#### Scenario: Requested port in use advances to the next port

- **WHEN** the requested port is already in use but a subsequent port within the retry window is free
- **THEN** the server binds the next free port and reports that actual port

#### Scenario: All candidate ports occupied

- **WHEN** every port in the retry window is occupied
- **THEN** the server returns an error explaining the bind failure that unwraps to the underlying syscall error

#### Scenario: Tailscale address resolves only legitimate CGNAT addresses

- **WHEN** Tailscale address discovery returns a non-CGNAT address such as `0.0.0.0`
- **THEN** the discovery result is rejected and treated as "no Tailscale address" so the server binds loopback only

### Requirement: Authenticated API access via bearer token or query param

Every `/api/*` route SHALL require authentication. The server SHALL accept either an `Authorization: Bearer <token>` header or a `?token=<token>` query parameter, with the header taking precedence. A request with no recognized token SHALL be rejected with 401. A small set of routes that the browser must fetch before login (the dashboard, the share target, vendored static assets, the PWA manifest, the service worker, and icons) SHALL be served without authentication.

#### Scenario: Valid master token accepted

- **WHEN** a request presents `Authorization: Bearer <master-token>`
- **THEN** the request is authenticated and tagged with `X-Argus-Auth: master`

#### Scenario: Missing or wrong token rejected

- **WHEN** a request to an `/api/*` route presents no token or an unrecognized token
- **THEN** the server responds 401 Unauthorized

#### Scenario: Query-param token accepted for EventSource

- **WHEN** a request presents a valid token only via the `?token=` query parameter and no Bearer header
- **THEN** the request is authenticated

#### Scenario: Bearer header wins over query param

- **WHEN** a request presents a valid Bearer header and a wrong `?token=` query parameter
- **THEN** the request is authenticated using the Bearer header

#### Scenario: Unauthenticated dashboard load

- **WHEN** a request hits the dashboard route or a vendored static asset without a token
- **THEN** the asset is served without requiring authentication

### Requirement: Per-device and plugin-scoped tokens

The server SHALL accept non-revoked per-device tokens persisted as SHA-256 hashes in addition to the master token. A device token (empty scope) SHALL be tagged `X-Argus-Auth: device`; a token bound to a non-empty scope SHALL be tagged `X-Argus-Auth: scope:<name>`. Revoked tokens SHALL be rejected. The plaintext token SHALL be returned only at mint time.

#### Scenario: Device token authenticates and is tagged device

- **WHEN** a request presents a valid, non-revoked device token
- **THEN** the request is authenticated and tagged `X-Argus-Auth: device`

#### Scenario: Plugin-scoped token tagged with its scope

- **WHEN** a request presents a valid token minted with scope `ludwig`
- **THEN** the request is authenticated and tagged `X-Argus-Auth: scope:ludwig`

#### Scenario: Revoked token rejected

- **WHEN** a request presents a token that has been revoked
- **THEN** the server responds 401 Unauthorized

### Requirement: Master-only gating for destructive and configuration endpoints

Endpoints that mutate shared configuration or act across all tasks SHALL require the master token: project CRUD, backend CRUD, the config snapshot read (`GET /api/config`), settings writes (`PUT /api/settings`), token minting/listing/revocation, stop-all sessions, prune-completed, and the cleanup-candidates clean endpoint. A request authenticated with a device token or a plugin-scoped token SHALL be rejected with 403 for these endpoints. The curated settings read (`GET /api/settings`) SHALL remain available to any authenticated token, including device tokens. Per-task operations (stop, delete, archive, rename, set-status, write input) SHALL remain available to any authenticated token. The cleanup-candidates compute-trigger and list (read) endpoints are NOT master-gated — they are read/trigger operations available to any authenticated token, matching the per-task-operation tier rather than the bulk-mutation tier.

#### Scenario: Device token rejected from a master-only endpoint

- **WHEN** a request authenticated as `device` calls a master-only endpoint such as stop-all, token minting, project/backend CRUD, or the cleanup-candidates clean endpoint
- **THEN** the server responds 403 Forbidden

#### Scenario: Master token permitted

- **WHEN** a request authenticated as `master` calls a master-only endpoint
- **THEN** the endpoint executes and returns its normal success status

#### Scenario: Plugin scope token does not satisfy master

- **WHEN** a request authenticated as `scope:<plugin>` calls a master-only endpoint
- **THEN** the server responds 403 Forbidden

#### Scenario: Device token allowed for per-task operations

- **WHEN** a request authenticated as `device` calls a per-task operation such as stop or delete
- **THEN** the operation executes (subject to task existence)

#### Scenario: Settings read available to a device token

- **WHEN** a request authenticated as `device` calls `GET /api/settings`
- **THEN** the server returns the curated settings view (the read is not master-gated)

#### Scenario: Device token allowed to trigger or read cleanup-candidate classification

- **WHEN** a request authenticated as `device` calls the cleanup-candidates compute-trigger or list endpoint
- **THEN** the operation executes normally — only the clean endpoint is master-gated

### Requirement: Task creation

The server SHALL create a task from a JSON body containing `prompt`, `project`, and optional `name` and `backend` fields. `project` SHALL be required. At least one of `name` or `prompt` SHALL be required. When `name` is empty it SHALL be synthesized from the prompt (first 40 characters, newlines/tabs replaced with spaces) and an asynchronous rename may follow. A `backend` that is not configured SHALL be rejected before any worktree is created. On success the server SHALL respond 201 with the new task id, name, and status. Requests with a `multipart/form-data` body SHALL additionally accept file attachments.

#### Scenario: Creates a task

- **WHEN** a valid create request with name, prompt, and project is submitted
- **THEN** the server responds 201 with the task id and name

#### Scenario: Missing project rejected

- **WHEN** a create request omits `project`
- **THEN** the server responds 400 Bad Request

#### Scenario: Missing name and prompt rejected

- **WHEN** a create request supplies neither `name` nor `prompt`
- **THEN** the server responds 400 Bad Request

#### Scenario: Unknown backend rejected

- **WHEN** a create request names a backend that is not configured
- **THEN** the server responds 400 Bad Request

#### Scenario: Backend override persists to the task

- **WHEN** a create request names a configured backend
- **THEN** the created task records that backend

### Requirement: Task listing and retrieval with runtime idle state

The server SHALL list tasks and SHALL support optional filtering by `status`, `project`, and `archived` (`0`/default excludes archived, `1` returns only archived, `all` returns both). For each task the server SHALL derive a runtime `idle` flag that is true only when the task is `in_progress` and either has no live session or its session is waiting for input; non-`in_progress` tasks SHALL never be reported idle. Retrieving a task that does not exist SHALL return 404.

#### Scenario: Lists all non-archived tasks

- **WHEN** a list request omits the archived filter
- **THEN** the server returns the non-archived tasks

#### Scenario: In-progress task without a live session reports idle

- **WHEN** a task is `in_progress` but has no live session
- **THEN** its listed entry reports `idle: true`

#### Scenario: Non-in-progress task never idle

- **WHEN** a task is `pending`
- **THEN** its listed entry reports idle false

#### Scenario: Filter by status and project

- **WHEN** a list request supplies `status` and/or `project` filters
- **THEN** only tasks matching the filters are returned

#### Scenario: Get a missing task

- **WHEN** a get request names a task id that does not exist
- **THEN** the server responds 404 Not Found

### Requirement: Task lifecycle transitions

Stopping a task SHALL stop its session (if any) and set its status to `in_review`. Resuming a task SHALL start or reattach a session and set status to `in_progress`, except that an actively-working (non-idle, live-session) `in_progress` task SHALL be refused with 409; idle or ghost `in_progress` tasks SHALL be resumed (healed) rather than refused. Setting status SHALL accept only `pending`, `in_progress`, `in_review`, `complete` and reject any other value. Renaming SHALL require a non-empty name. Deleting SHALL remove the task, its session, logs, and worktree/branch. Forking SHALL create a new task inheriting the source's prompt/project/backend when not overridden. All of these SHALL return 404 when the target task does not exist.

#### Scenario: Stop flips status to in_review

- **WHEN** a stop request targets an existing `in_progress` task
- **THEN** the session is stopped and the task status becomes `in_review`

#### Scenario: Resume refuses an actively-running task

- **WHEN** a resume request targets an `in_progress` task with a live, non-idle session
- **THEN** the server responds 409 Conflict

#### Scenario: Resume heals a desynced live session

- **WHEN** a resume request targets a task whose DB row drifted off `in_progress` while a live session still exists
- **THEN** the server reattaches, re-syncs the row to `in_progress`, and reports the resume as healed

#### Scenario: Set status rejects unknown value

- **WHEN** a set-status request supplies a status outside the allowed set
- **THEN** the server responds 400 Bad Request

#### Scenario: Rename rejects empty name

- **WHEN** a rename request supplies a blank name
- **THEN** the server responds 400 Bad Request

#### Scenario: Delete removes the worktree and branch

- **WHEN** a delete request targets a task with a worktree and branch
- **THEN** the task is removed and its worktree directory and branch are cleaned up

#### Scenario: Fork requires a project

- **WHEN** a fork request omits a project and the source task has none
- **THEN** the server responds 400 Bad Request

#### Scenario: Lifecycle action on a missing task

- **WHEN** a stop, resume, rename, set-status, archive, or delete request names a task id that does not exist
- **THEN** the server responds 404 Not Found

### Requirement: Terminal output, input, and resize

The server SHALL return a task's terminal output, preferring the on-disk session log and falling back to the live ring buffer, advertising a resume cursor (`X-Output-Total`) and source (`X-Source`) so a client can resume a stream without gap or overlap. A task with neither a log nor a live session SHALL return 404 for output. Writing input and reading/setting PTY size SHALL require a live session and return 404 otherwise. Resize SHALL reject zero dimensions and dimensions out of range (greater than 1000).

Writing input SHALL accept an optional origin indicator distinguishing human-typed input from system-injected input (see the `agent-execution` capability). The indicator SHALL default to human origin when absent — the endpoint's only behavior before this indicator existed — so every pre-existing caller (including plugin-scoped tokens that never learn about the indicator) is unaffected. Only a recognized system-origin value SHALL classify the write as system-injected; any other value SHALL also default to human origin.

#### Scenario: Output served from the on-disk log with resume cursor

- **WHEN** an output request targets a task that has a session log on disk
- **THEN** the server returns the log tail, sets `X-Source: log`, and sets `X-Output-Total` to the full log size

#### Scenario: Tail bound does not change the resume cursor

- **WHEN** an output request asks for a tail smaller than the full log
- **THEN** the body is the requested tail but `X-Output-Total` still advertises the full file size

#### Scenario: No log and no session

- **WHEN** an output request targets a task with no log file and no live session
- **THEN** the server responds 404 Not Found

#### Scenario: Input or size without a live session

- **WHEN** a write-input, get-size, or resize request targets a task with no live session
- **THEN** the server responds 404 Not Found

#### Scenario: Resize rejects invalid dimensions

- **WHEN** a resize request supplies zero or out-of-range dimensions on a live session
- **THEN** the server responds 400 Bad Request

#### Scenario: Absent origin indicator defaults to human origin

- **WHEN** a write-input request carries no origin indicator
- **THEN** the write is classified as human origin, advancing both the session's work-cycle and user-input timestamps

#### Scenario: System-origin indicator classifies the write as system-injected

- **WHEN** a write-input request carries the recognized system-origin indicator
- **THEN** the write is classified as system origin, advancing only the session's work-cycle timestamp

### Requirement: Configuration CRUD for projects and backends

The server SHALL expose master-only CRUD for projects and backends. Creating a project SHALL require non-empty name and path; creating a backend SHALL require non-empty name and command; updating a backend SHALL require a non-empty command. The full configuration snapshot SHALL be retrievable (master-only) and SHALL include defaults, backends, and projects.

#### Scenario: Create a backend

- **WHEN** a master request creates a backend with name and command
- **THEN** the backend is persisted and the server responds 201

#### Scenario: Create backend rejects empty name

- **WHEN** a master request creates a backend with an empty name
- **THEN** the server responds 400 Bad Request

#### Scenario: Config snapshot includes core sections

- **WHEN** a master request reads the configuration snapshot
- **THEN** the response contains defaults, backends, and projects

### Requirement: Git status and diff are path-traversal safe

The server SHALL return git status, file diff, and file-tree listings scoped to a task's worktree. These SHALL return 404 when the task or its worktree is unknown. Diff and file-tree path parameters SHALL reject absolute paths and any path containing `..` with 400, so the diff cannot be coerced into reading files outside the worktree.

#### Scenario: Diff rejects absolute or dotdot paths

- **WHEN** a git-diff request supplies an absolute path or a path containing `..`
- **THEN** the server responds 400 Bad Request

#### Scenario: Git status for an unknown worktree

- **WHEN** a git-status request targets a task with no worktree
- **THEN** the server responds 404 Not Found

### Requirement: File uploads enforce size and count caps

The server SHALL accept multipart file uploads (per task and at task creation) and SHALL enforce a per-file cap of 10 MB, a per-request total cap of 50 MB, and a maximum of 20 files. Uploaded filenames SHALL be sanitized (directory components, control characters, bidi overrides, and leading dashes removed) and written into the worktree without clobbering existing files (auto-suffixed). Oversize or too-many uploads SHALL return 413; empty or invalid-name parts SHALL return 400.

#### Scenario: Oversize attachment rejected

- **WHEN** an upload contains a file exceeding the per-file or per-request size cap, or exceeds the file count cap
- **THEN** the server responds 413 Request Entity Too Large

#### Scenario: Empty or invalid attachment name rejected

- **WHEN** an upload contains an empty file part or a part whose name sanitizes to nothing
- **THEN** the server responds 400 Bad Request

#### Scenario: Filename collision auto-suffixed

- **WHEN** an uploaded file's sanitized name already exists in the destination directory
- **THEN** the saved file is given a numeric suffix so the existing file is not overwritten

### Requirement: Settings partial update is master-only

The server SHALL return a curated settings view (sandbox, KB, API, defaults) and SHALL apply partial updates where each absent section means "leave unchanged". Settings updates SHALL require the master token. Apple-event bundle IDs SHALL be filtered through validation before persistence so invalid entries are dropped. Log retrieval SHALL be limited to the whitelisted log names and SHALL return an empty body rather than 404 when a log file does not yet exist.

#### Scenario: Settings update requires master

- **WHEN** a settings update is requested without the master token
- **THEN** the server responds 403 Forbidden

#### Scenario: Missing log file returns empty body

- **WHEN** a log read targets a whitelisted log that does not yet exist on disk
- **THEN** the server responds 200 with an empty body

#### Scenario: Unknown log name rejected

- **WHEN** a log read targets a name that is not whitelisted
- **THEN** the server responds 400 Bad Request

### Requirement: Hera orchestration roster endpoint

The REST API SHALL expose `GET /api/hera`, a read-only endpoint returning the Hera orchestration roster: a list of orchestrators — each with `id`, `name`, `pinned`, `archived`, `kanban_status` (`active`/`backlog`/`blocked`/`done`), `subtree_cost_usd`, and its non-freelance `roles` — plus a top-level `freelance` list of hoisted freelance roles. Each role SHALL carry `role_id`, `orch_id`, `name`, `kind` (`coordinator`/`worker`/`freelance`), `status` (`idle`/`working`/`blocked`/`done`, or empty when no status row exists), `task_id`, `task_name`, `task_status`, `live`, `ready_to_close`, `archived`, `tokens_input`, `tokens_cache_write_1h`, `tokens_cache_write_5m`, `tokens_cache_read`, `tokens_output`, and `cost_usd` (omitted or null when the role's resolved model has no rate-table entry for its accrued usage, or when its token totals are all zero — see `cost-estimation`). Both `cost_usd` and `subtree_cost_usd` SHALL be the PERSISTED, already-priced `cost_usd_accrued` values described in `cost-estimation`'s accrual-time-stamping requirement — the endpoint SHALL NOT compute or reprice cost against a live rate table on read. The endpoint MUST be authenticated like every other `/api/*` route. The handler MUST source all data from the database and MUST NOT import the TUI Hera package (to keep tview out of the API binary).

**Scope note, discovered during implementation:** `subtree_cost_usd` at THIS endpoint SHALL be the sum of the orchestrator's OWN roles' cost (every kind, including nuked ones) — it SHALL NOT recurse into orchestrators nested beneath it via the worker→coordinator bridge. Reproducing that recursive walk here would require importing `internal/tui/hera` (where the bridge-discovery logic — `Model.BridgeSubtree` — already lives), which this handler deliberately avoids per the constraint above: the `hera` package's rail/rendering code (tview/tcell) lives in the SAME Go package as its Model/BuildModel logic, so importing any part of it pulls in the whole thing. The TUI's LOCAL-mode rollup (`Model.SubtreeCostUSD`, in `hera-view`) computes the FULL recursive total directly against the database, with no REST round-trip — this scope note affects ONLY what a REST/remote-mode consumer sees, not local-mode TUI accuracy. A true cross-orchestrator recursive total for REST/remote-mode consumers is a named follow-up, not shipped in this change.

`kanban_status` is emitted as-is for every orchestrator regardless of nesting — the endpoint does not resolve canonical parents or otherwise distinguish top-level from nested orchestrators, so a nested orchestrator's own (rail-inert) `kanban_status` value is still visible in its envelope. `subtree_cost_usd` and every per-role cost/token field are likewise read-only: mutating any of them, or the underlying rate table, over REST is out of scope — this stays under the existing standing exception that Hera mutations are TUI-only (`GET /api/hera` stays read-only in every field).

These fields SHALL be populated regardless of which client renders them: the native TUI itself reads through this endpoint in `--remote` mode, and the web SPA and macOS app render no cost UI yet (an explicit, separately-tracked follow-up, not a reason to omit the data here).

Derived from: `internal/api/hera.go` (`heraOrchJSON`, `heraRoleJSON`, `handleHera`).

#### Scenario: Empty roster

- **WHEN** an authenticated client requests `/api/hera` with no orchestrators present
- **THEN** the response is `{"orchestrators": [], "freelance": []}`

#### Scenario: Bound role surfaces task fields

- **WHEN** a role has a live binding
- **THEN** that role appears under the orchestrator's `roles` with `live: true`, its hera `status`, and the bound task's `task_id`/`task_name`/`task_status`

#### Scenario: ready_to_close surfaces from task_meta

- **WHEN** a bound role's task carries `meta:hera.ready_to_close=true`
- **THEN** its `ready_to_close` field is `true`

#### Scenario: kanban_status defaults to active

- **WHEN** an orchestrator has never had its kanban status explicitly set
- **THEN** its envelope's `kanban_status` field reads `"active"`

#### Scenario: kanban_status reflects an explicit value

- **WHEN** an orchestrator's kanban status has been set to `"blocked"`
- **THEN** its envelope's `kanban_status` field reads `"blocked"`

#### Scenario: Missing or invalid auth is rejected

- **WHEN** `/api/hera` is requested without a valid bearer token or `?token=`
- **THEN** the request is rejected before any data is read

#### Scenario: A role's cost fields reflect its accumulated token totals

- **WHEN** a role's live or ended binding carries nonzero raw token totals and a nonzero persisted `cost_usd_accrued`
- **THEN** the role's JSON carries those five token totals and its persisted `cost_usd`, with no rate-table lookup performed by this endpoint

#### Scenario: An unmeasured role carries no cost figure

- **WHEN** a role's token totals are all zero, or its resolved model has no rate-table entry
- **THEN** its `cost_usd` field is omitted or null, not `0`

#### Scenario: An orchestrator's subtree_cost_usd sums its own roles, including a nuked one

- **WHEN** an orchestrator has two roles with recorded cost, one of which has since been nuked
- **THEN** its `subtree_cost_usd` field includes both — the live role's and the nuked role's

#### Scenario: subtree_cost_usd does not recurse into a nested sub-coordinator

- **WHEN** an orchestrator bridges to a nested sub-coordinator via a worker row, and that nested orchestrator has its own recorded cost
- **THEN** THIS orchestrator's `subtree_cost_usd` reflects only its own roles' cost — the nested orchestrator's cost is NOT added in (see the scope note above; the TUI's local-mode `Model.SubtreeCostUSD` computes the full recursive figure separately, not through this endpoint)

### Requirement: Reliable pane-delivery endpoint

The system SHALL expose a `POST /api/tasks/{id}/notify` endpoint that registers a text delivery for the named task via the reliable notify service. The request body SHALL require `text` (non-empty string), `submit` (must be `true`), and `delivery_id` (non-empty identifier, max 128 bytes, alphanumeric and `-_` only). An optional `deadline_ms` field controls the delivery deadline in milliseconds (default 300,000; minimum 1,000; maximum 3,600,000). The endpoint SHALL be callable by any authenticated token (master, device, or plugin-scoped). On success it SHALL return the delivery_id and its current state (`"submitted"` or `"pending"`). Re-posting a previously submitted delivery_id SHALL be idempotent (200 with state `"submitted"`).

#### Scenario: Delivery registered and pending

- **WHEN** a client posts a valid notify request for a task whose session is not yet idle
- **THEN** the endpoint returns 202 with `{"delivery_id": "...", "state": "pending"}`

#### Scenario: Delivery submitted inline (session already idle and unfocused)

- **WHEN** a client posts a valid notify request for a task that is idle and unfocused at request time
- **THEN** the endpoint returns 202 with `{"delivery_id": "...", "state": "submitted"}`

#### Scenario: Re-post of submitted delivery_id is idempotent

- **WHEN** a client posts a notify request with a delivery_id that was already submitted
- **THEN** the endpoint returns 200 with `{"delivery_id": "...", "state": "submitted"}` without re-injecting

#### Scenario: Missing or invalid fields rejected

- **WHEN** a client posts a notify request with missing text, missing delivery_id, or `submit` not `true`
- **THEN** the endpoint returns 400 with an error identifying the missing or invalid field

#### Scenario: Delivery_id format rejected

- **WHEN** a client posts a notify request with a delivery_id containing characters outside alphanumeric and `-_`
- **THEN** the endpoint returns 400

#### Scenario: Task not found

- **WHEN** a client posts a notify request for a task ID that does not exist
- **THEN** the endpoint returns 404

#### Scenario: Any authenticated token accepted

- **WHEN** a request authenticated with a device token or a plugin-scoped token posts to this endpoint
- **THEN** the request is accepted (not rejected as master-only)

### Requirement: Cancel pane-delivery endpoint

The system SHALL expose a `DELETE /api/tasks/{id}/notify/{delivery_id}` endpoint that cancels a pending delivery registered via the notify endpoint. If the delivery is pending, it SHALL be removed and the response SHALL indicate `cancelled: true`. If the delivery is not found (already submitted or never registered), the response SHALL indicate `cancelled: false` and return 200 (idempotent).

#### Scenario: Pending delivery cancelled

- **WHEN** a client calls DELETE for a delivery_id that is currently pending
- **THEN** the response is `{"delivery_id": "...", "cancelled": true}` and no further PTY write occurs for that delivery

#### Scenario: Already-submitted delivery cancel is a no-op

- **WHEN** a client calls DELETE for a delivery_id that was already submitted
- **THEN** the response is `{"delivery_id": "...", "cancelled": false}` with 200 (no error)

#### Scenario: Unknown delivery_id is a no-op

- **WHEN** a client calls DELETE for a delivery_id that was never registered
- **THEN** the response is `{"delivery_id": "...", "cancelled": false}` with 200 (no error)

#### Scenario: Any authenticated token accepted

- **WHEN** a request authenticated with a device token or plugin-scoped token calls DELETE on this endpoint
- **THEN** the request is accepted (not rejected as master-only)

### Requirement: System metrics endpoint

The API SHALL expose a read-only `GET /api/system-metrics` endpoint, authenticated
like other API routes, that returns a snapshot of host-system load: overall CPU
utilization percent, the 1/5/15-minute load average, total/used/available memory
with percent, total/used swap with percent, total/used/free disk for the filesystem
holding the Argus data directory (`~/.argus`) along with that path, the Argus
process resident memory, host uptime, and the count of active and idle agent
sessions. Each metric SHALL carry an availability indicator so a metric the host
platform cannot supply is reported as unavailable rather than failing the whole
response. Metrics SHALL be sampled by a background collector on its own interval and
served from cache so requests return promptly and CPU deltas are accurate; the
session counts SHALL be read live at request time.

#### Scenario: Authenticated request returns a metrics snapshot
- **WHEN** an authenticated `GET /api/system-metrics` request is made
- **THEN** the response is 200 OK with a JSON body containing CPU, memory, swap, disk, process, uptime, and session-count fields

#### Scenario: Unauthenticated request is rejected
- **WHEN** a `GET /api/system-metrics` request is made without a valid token
- **THEN** the response is 401 Unauthorized

#### Scenario: Unavailable metric degrades gracefully
- **WHEN** the host platform cannot supply a particular metric (e.g. load average)
- **THEN** that field is marked unavailable in the response and the remaining metrics are still returned with 200 OK

#### Scenario: Session counts are read live
- **WHEN** the snapshot is served
- **THEN** the active/idle session counts reflect the runner's current state at request time, not the cached sample time

### Requirement: Cleanup-candidate classification endpoints

The system SHALL expose endpoints to trigger and read an on-demand, daemon-side classification of tasks matching the stuck-task predicate (`archived=1`, `status=in_review`, no live Hera binding): `POST /api/maintenance/cleanup-candidates/compute` starts a background classification pass (a no-op if one is already in flight) covering eligible tasks without a cached verdict or with a non-terminal (not-safe) cached verdict, and `GET /api/maintenance/cleanup-candidates` returns the current cached results plus a `computing` flag. Classification SHALL run entirely server-side (the merge-safety classifier), never on the calling client. Results SHALL be cached (surviving a daemon restart) so repeat calls do not re-spend the shared GitHub GraphQL budget on already-confirmed-safe tasks.

#### Scenario: Compute starts a background pass
- **WHEN** `POST /api/maintenance/cleanup-candidates/compute` is called and no computation is currently running
- **THEN** the server starts a background classification pass and returns immediately without waiting for it to finish

#### Scenario: Compute is idempotent while running
- **WHEN** `POST /api/maintenance/cleanup-candidates/compute` is called while a computation is already in flight
- **THEN** the server does not start a second concurrent pass

#### Scenario: List reflects cached results and in-flight status
- **WHEN** `GET /api/maintenance/cleanup-candidates` is called
- **THEN** the response includes every currently-eligible task's last cached classification (tier, safe/not-safe, reason) and a `computing` flag reflecting whether a pass is currently running

#### Scenario: Safe verdicts are cached as terminal
- **WHEN** a task's cached verdict is confirmed-safe
- **THEN** a subsequent compute pass does not re-classify it (no repeat GraphQL cost for that task)

#### Scenario: Not-safe verdicts are re-checked on the next compute
- **WHEN** a task's cached verdict is not-confirmed
- **THEN** a subsequent compute pass re-classifies it, since a later merge could change the outcome

### Requirement: Cleanup-candidate clean endpoint is master-only, immediate, and snapshot-scoped

`POST /api/maintenance/cleanup-candidates/clean` SHALL require the master token (an across-all-tasks bulk mutation). It SHALL accept a `scope` of `safe` or `all`, and SHALL act on the currently cached classification snapshot (not a fresh live re-classification), immediately deleting each matching task's row, worktree, and branch via the same guarded deletion primitive the `Ctrl+R` prune-completed flow uses (see the `worktree-management` capability's explicit-ID-list pruning mode) — no intermediate status-flip step, no requirement for a subsequent manual prune. Before deleting each task it SHALL re-verify the task still matches the stuck-task predicate and the live-Hera-binding guard, and SHALL skip (not error) any that no longer do.

#### Scenario: Device token rejected
- **WHEN** a request authenticated as `device` or `scope:<plugin>` calls the clean endpoint
- **THEN** the server responds 403 Forbidden

#### Scenario: Safe-only scope only deletes confirmed-safe tasks
- **WHEN** the master token calls clean with `scope: "safe"`
- **THEN** only tasks whose cached verdict is confirmed-safe are deleted (row, worktree, branch); not-safe tasks are untouched

#### Scenario: All scope deletes every listed candidate
- **WHEN** the master token calls clean with `scope: "all"`
- **THEN** every currently cached candidate (safe and not-safe) is deleted

#### Scenario: A task that no longer qualifies is skipped, not errored
- **WHEN** clean processes a cached candidate whose task has since stopped matching the stuck-task predicate or the live-binding guard
- **THEN** that task is skipped without aborting or erroring the rest of the batch

#### Scenario: Clean acts on the reviewed snapshot, not a fresh classification
- **WHEN** clean runs
- **THEN** it uses the classification results already returned by the most recent `GET /api/maintenance/cleanup-candidates` call, not a newly computed pass

#### Scenario: Clean is immediate, with no separate later step
- **WHEN** clean completes for a scope
- **THEN** the deleted tasks' rows, worktrees, and branches are gone at that point — no further manual action (e.g. a separate prune-completed run) is needed to finish the cleanup

### Requirement: Claude session listing and switching endpoints

The system SHALL expose a `GET /api/tasks/{id}/claude-sessions` endpoint that lists the named task's available Claude sessions (via `internal/claudesession.List`, keyed on the task's worktree) and a `POST /api/tasks/{id}/claude-session` endpoint that switches the task to a different session, mirroring the TUI's `ctrl+r` session-switcher flow (`internal/tui/app.go`'s `openSessionPickerModal`/`switchSession`). Both endpoints SHALL be callable by any authenticated token (master, device, or plugin-scoped) and SHALL be restricted to Claude-backed tasks (matching the TUI's own `IsCodexBackend`/`IsPiBackend`/`IsOpencodeBackend` guard) — a non-Claude-backed task returns 400 rather than an empty or garbage session list.

#### Scenario: List sessions for a Claude-backed task

- **WHEN** a client calls `GET /api/tasks/{id}/claude-sessions` for a Claude-backed task
- **THEN** the response is 200 with `{"sessions": [{"id", "title", "branch", "pr_ref", "mod_time", "size_bytes"}, ...], "current_session_id": "..."}`, newest session first

#### Scenario: List sessions for a non-Claude-backed task

- **WHEN** a client calls `GET /api/tasks/{id}/claude-sessions` for a task whose resolved backend is Codex, Pi, or Opencode
- **THEN** the response is 400 identifying that session listing is Claude-only

#### Scenario: Switch to a different session

- **WHEN** a client posts `{"session_id": "<id>"}` to `POST /api/tasks/{id}/claude-session` for a Claude-backed task, where `<id>` differs from the task's current session
- **THEN** the task's stored session ID is updated to `<id>`, any live session for the task is stopped and restarted (or started fresh if none was running) resuming with `<id>`, and the response is 200 with `{"status": "switched", "pid": <int>}`

#### Scenario: Switch to the already-active session is a no-op

- **WHEN** a client posts the task's current session ID to `POST /api/tasks/{id}/claude-session`
- **THEN** no session is stopped or restarted and the response is 200 with `{"status": "unchanged"}`

#### Scenario: Switch for a non-Claude-backed task or unknown session ID

- **WHEN** a client posts to `POST /api/tasks/{id}/claude-session` for a non-Claude-backed task, or with a missing/empty `session_id`
- **THEN** the response is 400

#### Scenario: Task not found

- **WHEN** a client calls either endpoint for a task ID that does not exist
- **THEN** the response is 404

#### Scenario: Any authenticated token accepted

- **WHEN** a request authenticated with a device token or a plugin-scoped token calls either endpoint
- **THEN** the request is accepted (not rejected as master-only)

