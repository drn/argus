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

Endpoints that mutate shared configuration or act across all tasks SHALL require the master token: project CRUD, backend CRUD, config and settings reads/writes, token minting/listing/revocation, stop-all sessions, and prune-completed. A request authenticated with a device token or a plugin-scoped token SHALL be rejected with 403 for these endpoints. Per-task operations (stop, delete, archive, rename, set-status, write input) SHALL remain available to any authenticated token.

#### Scenario: Device token rejected from a master-only endpoint

- **WHEN** a request authenticated as `device` calls a master-only endpoint such as stop-all, token minting, or project/backend CRUD
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
