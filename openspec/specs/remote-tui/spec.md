# Remote TUI Mode

## Purpose

Remote TUI mode lets the Argus terminal UI drive a daemon running on another
host entirely over the REST API instead of a local SQLite database and Unix
socket. It is launched with `--remote URL --token TOKEN` and is intended for
controlling a home/work daemon over a private network such as Tailscale. All
persistence and agent-session control routes through HTTP and Server-Sent
Events, so the daemon remains the single owner of the database and the running
agent processes.

## Requirements

### Requirement: Remote launch authentication and preflight

The remote TUI SHALL require a bearer token and SHALL verify connectivity to
the remote host before taking over the terminal, failing loudly with a
distinct exit code or message for each failure class so a misconfigured host
or token never produces a silent or corrupted alt-screen.

#### Scenario: Missing token

- **WHEN** `--remote` is launched with an empty token (neither `--token` nor `ARGUS_TOKEN` provided)
- **THEN** the process SHALL print an error instructing the user to supply a token and exit with a non-zero status without starting the UI

#### Scenario: Token rejected by remote

- **WHEN** the initial status request to the remote host returns 401 Unauthorized
- **THEN** the process SHALL print an error indicating the token was rejected and exit without starting the UI

#### Scenario: Remote unreachable

- **WHEN** the initial status request fails for any non-401 reason (host down, network error)
- **THEN** the process SHALL print an error naming the unreachable base URL and exit without starting the UI

#### Scenario: Token passed on the command line

- **WHEN** the token is supplied via `--token` rather than the `ARGUS_TOKEN` environment variable
- **THEN** the process SHALL emit a warning that the token is visible in process listings before the UI starts

### Requirement: Authenticated REST transport

The HTTP client SHALL attach the bearer token to every API request, normalise
the base URL, and translate non-2xx responses into typed errors that callers
can classify without string matching.

#### Scenario: Authorization header on requests

- **WHEN** any `/api/*` request is issued with a non-empty token
- **THEN** the request SHALL carry an `Authorization: Bearer <token>` header

#### Scenario: Base URL normalisation

- **WHEN** a client is constructed with a base URL that has a trailing slash
- **THEN** the trailing slash SHALL be stripped before requests are built

#### Scenario: Not-found classification

- **WHEN** a request returns HTTP 404 with an `{"error":"..."}` body
- **THEN** the returned error SHALL be classifiable as not-found and expose the status code and server message

#### Scenario: Unauthorized classification

- **WHEN** a request returns HTTP 401
- **THEN** the returned error SHALL be classifiable as unauthorized

### Requirement: Persistence proxied over REST

The remote store SHALL satisfy the same persistence interface as the local
database, proxying each task, project, backend, schedule, and config operation
to the corresponding REST endpoint, and SHALL translate remote not-found
responses back into the local sentinel errors callers expect.

#### Scenario: Listing and fetching tasks

- **WHEN** the store lists or fetches tasks
- **THEN** it SHALL return full task records sourced from the raw task endpoints, preserving fields such as dependencies

#### Scenario: Not-found translated to local sentinel

- **WHEN** fetching a task or schedule by ID returns a remote 404
- **THEN** the store SHALL return the local not-found sentinel error (`db.ErrTaskNotFound` / `db.ErrScheduleNotFound`) rather than the raw HTTP error

#### Scenario: Mutating a task

- **WHEN** the store updates, renames, archives, or deletes a task
- **THEN** it SHALL invoke the matching REST endpoint and the server SHALL own the resulting worktree, branch, and message/artifact cleanup

### Requirement: Project and backend upsert semantics

The store SHALL upsert projects and backends by attempting a create first and
falling back to an update ONLY when the create fails specifically because the
record already exists, so that genuine validation errors are surfaced rather
than masked by a second attempt.

#### Scenario: Create succeeds

- **WHEN** a project or backend is set and the create request succeeds
- **THEN** the store SHALL NOT issue an update request

#### Scenario: Conflict falls back to update

- **WHEN** the create request fails with HTTP 409 conflict
- **THEN** the store SHALL retry the operation as an update with the same body

#### Scenario: Other failures are surfaced

- **WHEN** the create request fails with a non-409 status (other 4xx, 5xx) or a transport error
- **THEN** the store SHALL return that error without attempting an update

### Requirement: Config snapshot caching with periodic refresh

The store SHALL serve config reads from a cached snapshot rather than a
per-call HTTP request, and the remote mode SHALL refresh that snapshot
periodically in the background so settings changed on the daemon become
visible without a request on every UI frame.

#### Scenario: Config served from cache

- **WHEN** the cached config has been populated via a refresh and config is read repeatedly
- **THEN** each read SHALL return the cached snapshot without issuing a new HTTP request

#### Scenario: Refresh failure preserves prior snapshot

- **WHEN** a config refresh request fails
- **THEN** the previously cached snapshot SHALL be returned unchanged and the error reported to the caller

#### Scenario: Periodic background refresh

- **WHEN** remote mode is running
- **THEN** the config snapshot SHALL be refreshed on a recurring interval until shutdown cancels the refresher

### Requirement: Config value updates restricted to mapped keys

The store SHALL update only the configuration keys that map to a known typed
settings endpoint, and SHALL reject any unmapped key with an error rather than
silently dropping the write.

#### Scenario: Mapped key is forwarded

- **WHEN** a recognised config key (e.g. a sandbox, KB, API, or defaults key) is set
- **THEN** the store SHALL translate it to the typed settings update body and POST it to the settings endpoint

#### Scenario: Unmapped key rejected

- **WHEN** an unrecognised config key is set
- **THEN** the store SHALL return an error indicating no remote handler exists for that key

### Requirement: Agent sessions over HTTP and SSE

The session provider SHALL satisfy the same session-provider interface as the
in-process runner, fronting each task with a session whose terminal output is
streamed over SSE into a local ring buffer while input, resize, stop, and
status queries are issued as REST calls.

#### Scenario: Starting a session attaches the stream

- **WHEN** a session is started for a task
- **THEN** the provider SHALL resume the task on the server, return a session handle, and begin streaming output for that task

#### Scenario: Streamed output reaches the ring buffer

- **WHEN** the SSE stream emits a base64-encoded `data:` output event
- **THEN** the decoded bytes SHALL be appended to the session's local ring buffer and become readable via the recent-output accessors

#### Scenario: Writing input updates last-input time

- **WHEN** input is written to a session
- **THEN** the bytes SHALL be POSTed to the input endpoint and the session's last-input timestamp SHALL advance

#### Scenario: Session liveness queried from the server

- **WHEN** running, idle, or has-session state is requested
- **THEN** the provider SHALL derive the answer from the server's session-state endpoint, returning empty/false when that request fails

### Requirement: Stream termination and reconnection

A session's SSE reader SHALL distinguish a server-reported process exit from a
transient connection drop, attempting a bounded number of reconnects on
transient drops and reporting the appropriate exit reason when the session
ultimately ends.

#### Scenario: Server reports process exit

- **WHEN** the stream emits an `event: exit` or the stream endpoint returns 404 (no session for the task)
- **THEN** the session SHALL be marked done, removed from the provider, and the registered exit callback SHALL fire

#### Scenario: Transient drop is retried

- **WHEN** the stream connection drops without an exit event
- **THEN** the reader SHALL attempt to reconnect up to a bounded retry limit before giving up

#### Scenario: Retries exhausted

- **WHEN** reconnection attempts are exhausted without recovering the stream
- **THEN** the session SHALL be marked done and removed with a stream-lost exit reason

### Requirement: Master-only operations and daemon-admin no-ops

Operations the remote process cannot meaningfully perform SHALL surface a clear
error rather than silently succeed: master-only server operations rejected for
device tokens SHALL propagate the rejection, and persistence operations with no
remote endpoint SHALL return an explanatory error.

#### Scenario: Master-only endpoint rejected for a device token

- **WHEN** a master-only operation (e.g. stop-all, prune-completed, raw task writes) is invoked with a device token and the server returns 403
- **THEN** the error SHALL propagate to the caller rather than be treated as success

#### Scenario: No remote endpoint for an operation

- **WHEN** a store operation has no corresponding REST endpoint (e.g. purging task messages or artifacts directly)
- **THEN** the store SHALL return an explanatory error noting the daemon performs that cleanup on task deletion instead
