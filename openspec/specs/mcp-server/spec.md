# MCP Server

## Purpose

The MCP server exposes Argus capabilities to LLM agents over the Model Context Protocol (Streamable HTTP transport). It surfaces a knowledge-base tool set and, when the daemon wires them in, tools for task lifecycle management, dependency linking, inter-task messaging, recurring schedules, clipboard staging, and viewable artifacts. The server lets an agent (or an orchestrator agent) drive Argus programmatically without going through the TUI or web UI.

## Requirements

### Requirement: JSON-RPC over Streamable HTTP transport

The server SHALL serve a single MCP endpoint that follows the MCP Streamable HTTP transport: POST carries client-to-server JSON-RPC, GET is a long-lived server-to-client SSE channel, and DELETE acknowledges session termination. JSON-RPC requests (those carrying an `id`) MUST receive a JSON response; pure notifications (no `id`) MUST receive HTTP 202 Accepted with an empty body. Malformed JSON MUST yield a JSON-RPC parse error (code -32700). Unknown methods MUST yield a method-not-found error (code -32601). The request body SHALL be capped (4 MiB) to bound memory.

#### Scenario: Request with id returns a JSON-RPC response

- **WHEN** a POST carries a JSON-RPC request with an `id`
- **THEN** the server returns HTTP 200 with a JSON-RPC response object carrying the same `id`

#### Scenario: Notification returns 202 with empty body

- **WHEN** a POST carries a JSON-RPC message with no `id` (e.g. `notifications/initialized`)
- **THEN** the server returns HTTP 202 Accepted with an empty body and no JSON-RPC response

#### Scenario: Unparseable body is a parse error

- **WHEN** a POST body is not valid JSON
- **THEN** the server returns a JSON-RPC error with code -32700

#### Scenario: Unknown method is method-not-found

- **WHEN** a request names a method the server does not implement
- **THEN** the server returns a JSON-RPC error with code -32601

#### Scenario: GET stream stays open until disconnect or shutdown

- **WHEN** a client opens the GET SSE stream
- **THEN** the connection stays open, emitting periodic keepalive comment frames, and returns only when the client disconnects or the server shuts down

### Requirement: Initialize handshake advertises tool capability

On `initialize` the server SHALL return its server info (name `argus`), advertise the tools capability, include the knowledge-base usage instructions, and echo back the client's requested protocol version (falling back to a default when the client omits it).

#### Scenario: Protocol version is echoed

- **WHEN** a client sends `initialize` with a `protocolVersion`
- **THEN** the response's `protocolVersion` equals the value the client sent

#### Scenario: Missing protocol version falls back to default

- **WHEN** a client sends `initialize` with no `protocolVersion`
- **THEN** the response carries a default protocol version

### Requirement: Tool surface is gated on wired capabilities

`tools/list` SHALL always include the knowledge-base tools. Task tools (and dependency-linking tools) SHALL appear only when task management is wired; clipboard, messaging, and artifact tools SHALL appear only when their dependency AND task management are both wired; schedule tools SHALL appear only when both schedule store and runner are wired. Plugin-registered tools SHALL be unioned in additively, and a registry failure MUST NOT break the listing of built-in tools. Calling a tool whose capability is not wired MUST return a tool error stating the capability is not configured.

#### Scenario: KB tools always listed

- **WHEN** `tools/list` is called on a server with no managers wired
- **THEN** the knowledge-base tools are present in the result

#### Scenario: Task tools appear only when wired

- **WHEN** task management has been wired and `tools/list` is called
- **THEN** the task tools (including the linking tools) appear in the result

#### Scenario: Calling an unwired task tool errors

- **WHEN** a task tool is called on a server where task management is not wired
- **THEN** the response is a tool error indicating task management is not configured

### Requirement: Knowledge-base tools

The server SHALL expose tools to search, read, list, ingest, and delete knowledge-base documents. Search input SHALL be sanitized and an empty post-sanitization query MUST return a no-results message rather than an error. Ingest and delete paths MUST be vault-relative with no escaping (`..` components or absolute paths rejected, and resolved paths that escape the configured vault directory rejected). Ingest MUST require both a non-empty path and content. When a vault path is configured, ingest SHALL write the document back to the vault and delete SHALL remove the vault file.

#### Scenario: Empty query after sanitization

- **WHEN** `kb_search` is called with a query that sanitizes to empty
- **THEN** the result reports no results and is not flagged as an error

#### Scenario: Path traversal rejected on ingest and delete

- **WHEN** `kb_ingest` or `kb_delete` is called with a path containing `..` or an absolute path
- **THEN** the response is a tool error reporting an invalid path

#### Scenario: Ingest requires path and content

- **WHEN** `kb_ingest` is called without a path or without content
- **THEN** the response is a tool error reporting path and content are required

### Requirement: Task creation with worktree and orchestration fields

`task_create` SHALL require a project and a prompt, derive the name from the prompt when omitted, create a worktree, start the agent session, and return a summary including the task ID, name, status, and project. It MUST cap concurrent creations and reject when the limit is exceeded. When `name` is supplied, it SHALL be idempotent on the `(name, project)` pair: an existing non-archived match errors unless `upsert: true` is passed, in which case the existing task is returned unchanged. `depends_on` entries MUST reference real, non-archived tasks, MUST be capped in count, and MUST be rejected when they would form a cycle in the persisted dependency graph or when the graph is too large to verify acyclic.

#### Scenario: Missing required fields

- **WHEN** `task_create` is called without a project or without a prompt
- **THEN** the response is a tool error naming the missing field

#### Scenario: Duplicate name without upsert errors

- **WHEN** `task_create` is called with a `name` matching an existing non-archived task in the same project and `upsert` is not set
- **THEN** the response is a tool error reporting the task already exists and suggesting `upsert: true`

#### Scenario: Duplicate name with upsert returns the existing task

- **WHEN** `task_create` is called with a duplicate `(name, project)` and `upsert: true`
- **THEN** the existing task is returned unchanged with a summary indicating it already exists

#### Scenario: Dependency on unknown or archived task rejected

- **WHEN** `task_create` declares a `depends_on` entry that does not exist or is archived
- **THEN** the response is a tool error identifying the offending dependency

#### Scenario: Concurrent-create limit enforced

- **WHEN** more than the allowed number of `task_create` calls are in flight at once
- **THEN** further calls return a tool error reporting too many concurrent task creations

### Requirement: Task read and listing

`task_list` SHALL list non-archived tasks, optionally filtered by status, project, and plan_slug, and report a no-tasks message when none match. `task_get` SHALL require an `id`, error when the task is not found, and render the task's details including its dependency state, surfacing the subset of dependencies that have not yet reached complete (or are missing) as the blocking set.

#### Scenario: List filters by status

- **WHEN** `task_list` is called with a `status` filter
- **THEN** only non-archived tasks whose status matches are returned

#### Scenario: Get unknown task errors

- **WHEN** `task_get` is called with an `id` that does not exist
- **THEN** the response is a tool error reporting the task was not found

#### Scenario: Get surfaces unresolved dependencies as blocking

- **WHEN** `task_get` is called on a task with dependencies that have not all reached complete
- **THEN** the rendered details include the unresolved dependencies as the blocked-by set

### Requirement: Caller identity resolved by id or cwd

Tools invoked by an agent that does not know its own task ID (archive, rename, complete, set-result, clipboard, messaging, artifacts) SHALL resolve the task from an explicit `id` or, failing that, from a `cwd` matched against task worktree paths using longest-prefix-wins with separator guarding so sibling worktrees do not collide. When neither an `id` nor a matching `cwd` is provided, the tool MUST return a tool error.

#### Scenario: Resolve by exact worktree cwd

- **WHEN** a tool is called with a `cwd` equal to or nested inside a task's worktree
- **THEN** the operation targets that task

#### Scenario: Sibling worktree with shared prefix does not match

- **WHEN** a `cwd` shares a string prefix with a worktree but is not that worktree or a child of it
- **THEN** that task is not selected

#### Scenario: Neither id nor matching cwd

- **WHEN** a tool is called with no `id` and a `cwd` that matches no worktree
- **THEN** the response is a tool error

### Requirement: Task lifecycle transitions

`task_stop` SHALL send a stop signal (reporting an eventual transition to in_review) and require an `id`. `task_complete` SHALL set status to complete (stamping the end time) and be a no-op when already complete; it does not stop a running session. `task_archive` SHALL set or toggle the archived flag, report a no-op when the requested state already holds, and on archive best-effort clear queued messages for the task. `task_rename` SHALL require a non-empty, length-capped name, update only the display name, and report a no-op when the name is unchanged.

#### Scenario: Stop requires id

- **WHEN** `task_stop` is called without an `id`
- **THEN** the response is a tool error reporting id is required

#### Scenario: Complete is a no-op when already complete

- **WHEN** `task_complete` resolves a task already in complete status
- **THEN** the task is unchanged and the result reports it is already complete

#### Scenario: Archive toggles when no explicit state given

- **WHEN** `task_archive` resolves a task and no `archived` value is supplied
- **THEN** the archived flag is flipped to its opposite value

#### Scenario: Rename rejects empty name

- **WHEN** `task_rename` is called with a name that is empty after trimming
- **THEN** the response is a tool error reporting name is required

### Requirement: Structured task result storage

`task_set_result` SHALL require a `result` that is a JSON object (arrays, scalars, and bare strings rejected), re-encode it canonically before storing, enforce a serialized-size cap, and persist it as last-write-wins. The daemon SHALL NOT interpret the payload.

#### Scenario: Non-object result rejected

- **WHEN** `task_set_result` is called with a `result` that is not a JSON object
- **THEN** the response is a tool error reporting the result must be a JSON object

#### Scenario: Oversized result rejected

- **WHEN** the canonically-serialized result exceeds the size cap
- **THEN** the response is a tool error reporting the size limit

> Note: the `task_link` / `task_unlink` / `task_deps` / `task_halt_downstream` /
> `task_set_plan_slug` dependency-linking tools were retired together with the
> `depends_on` DAG; Hera (coordinator-driven worker spawning) is the single
> orchestration model. See the hera-coordination spec.

### Requirement: Inter-task messaging

`task_message_send` SHALL require a recipient `to` and a `body`, resolve the sender by id/cwd, reject when the recipient does not exist, default the kind to note, and persist a durable message; it MUST translate body-too-large, self-send, inbox-full, and rate-limit failures into clear tool errors, and when a nudger is wired best-effort write a notification line to a live recipient's session. `task_inbox` SHALL return messages addressed to the caller oldest-first (defaulting to unread-only) without auto-acking them, supporting sender and since filters and a capped limit. `task_message_ack` SHALL require a non-empty, capped list of message IDs and mark the caller's matching messages read, silently ignoring IDs not belonging to the caller. `task_ask` SHALL send a question and, when given a positive timeout within the cap, block for a reply, otherwise return immediately with the question ID.

#### Scenario: Send requires recipient and body

- **WHEN** `task_message_send` is called without `to` or without `body`
- **THEN** the response is a tool error naming the missing field

#### Scenario: Send to unknown recipient rejected

- **WHEN** `task_message_send` targets a `to` that does not resolve to a task
- **THEN** the response is a tool error reporting the recipient was not found

#### Scenario: Inbox defaults to unread only

- **WHEN** `task_inbox` is called without `unread_only`
- **THEN** only unread messages addressed to the caller are returned, oldest first, and not marked read

#### Scenario: Ask with zero timeout returns immediately

- **WHEN** `task_ask` is called with `timeout_seconds` of 0
- **THEN** the question is sent and the result returns the question ID for polling without blocking

#### Scenario: Ask timeout over the cap rejected

- **WHEN** `task_ask` is called with `timeout_seconds` above the maximum
- **THEN** the response is a tool error reporting the timeout cap

### Requirement: Schedule management

The server SHALL expose tools to list, create, update, delete, and run-now scheduled tasks. `schedule_create` SHALL require name, project, and prompt, accept exactly one of a cron `schedule` or a future RFC3339 `run_once_at`, reject a past one-shot timestamp, default to enabled, validate the schedule, and precompute the next run time. `schedule_update` SHALL apply only the supplied fields, reject supplying both a non-empty cron schedule and a non-empty one-shot timestamp in the same call, auto-clear the opposing cadence field when one is set, and recompute the next run time when the cadence changes. `schedule_run_now` SHALL fire the schedule out of cycle and report the created task.

#### Scenario: Create requires a future one-shot timestamp

- **WHEN** `schedule_create` is given a `run_once_at` that is not in the future
- **THEN** the response is a tool error reporting the timestamp must be in the future

#### Scenario: Update rejects both cadences at once

- **WHEN** `schedule_update` is given both a non-empty `schedule` and a non-empty `run_once_at`
- **THEN** the response is a tool error reporting that only one cadence may be specified

#### Scenario: Run-now creates a task

- **WHEN** `schedule_run_now` is called with a valid schedule id
- **THEN** a fresh task is created from the schedule and the result reports its id and name

### Requirement: Clipboard staging

`argus_clipboard_set` SHALL require non-empty text, resolve the target task by id/cwd, and stage the text for the user to copy via a single gesture (it does not write to the OS clipboard directly). It MUST report a tool error when the clipboard capability is not wired.

#### Scenario: Empty text rejected

- **WHEN** `argus_clipboard_set` is called with empty text
- **THEN** the response is a tool error reporting text is required

#### Scenario: Staged text reported

- **WHEN** `argus_clipboard_set` is called with text for a resolvable task
- **THEN** the text is staged and the result confirms how many bytes were staged for the task

### Requirement: Viewable artifact registration

`artifact_register` SHALL require a source `path`, resolve the owning task by id/cwd, sanitize the destination basename (rejecting path separators and `..`), determine the artifact type from an explicit valid value or infer it from the extension, copy the source file into durable per-task storage under a size cap, and persist a manifest row (last-write-wins per filename). When the manifest write fails after the copy, the copied bytes MUST be removed so no unreferenced file is left behind.

#### Scenario: Path required

- **WHEN** `artifact_register` is called without a `path`
- **THEN** the response is a tool error reporting path is required

#### Scenario: Invalid explicit type rejected

- **WHEN** `artifact_register` is called with a `type` that is not a recognized artifact type
- **THEN** the response is a tool error listing the valid types

#### Scenario: Oversized artifact rejected

- **WHEN** the source file exceeds the artifact size cap
- **THEN** the response is a tool error reporting the cap and no manifest row is created

### Requirement: Plugin tool registry and proxying

Plugin-registered tools SHALL be name-scoped (a tool name MUST start with its scope prefix and contain only an allowlisted character set), validated against size caps and a per-scope tool count limit, and persisted with a last-seen heartbeat. Registering an existing name from a different scope MUST be rejected. A `tools/call` for a registered plugin name SHALL be proxied as an HTTP POST to the plugin's callback URL, returning the plugin's MCP-native result; a non-2xx plugin response or decode failure MUST surface as a tool error. Idle tools (no heartbeat within the idle window) SHALL be sweepable, and a successful invocation MUST refresh the heartbeat.

#### Scenario: Name must carry the scope prefix

- **WHEN** a plugin registers a tool whose name does not start with `<scope>_`
- **THEN** the registration is rejected with a scope-prefix error

#### Scenario: Cross-scope name collision rejected

- **WHEN** a scope tries to register a tool name already owned by a different scope
- **THEN** the registration is rejected

#### Scenario: Plugin call proxied to callback

- **WHEN** `tools/call` names a registered plugin tool
- **THEN** the server POSTs the input to the plugin's callback URL and returns the plugin's tool result

#### Scenario: Plugin error surfaced as tool error

- **WHEN** the plugin callback returns a non-2xx status or an undecodable body
- **THEN** the response is a tool error describing the plugin failure
