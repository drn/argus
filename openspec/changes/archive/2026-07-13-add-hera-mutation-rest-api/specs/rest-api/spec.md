## ADDED Requirements

### Requirement: Hera mutation endpoints resolve the target orchestrator's coordinator server-side

For every Hera mutation endpoint under `/api/hera/orchestrators/{orch_id}/...`, the server SHALL parse `orch_id` as an integer (400 on a non-numeric value), resolve the orchestrator (404 if unknown), and resolve that orchestrator's live coordinator role (409 "orchestrator has no live coordinator" if the orchestrator has no live coordinator binding). The resolved coordinator role SHALL act as the caller identity for the mutation — no client-supplied field identifies the sender/actor. This mirrors the precondition every MCP `hera_spawn_worker`/`hera_plan*` tool already enforces (a live coordinator binding), adapted for a caller with no `cwd` to resolve identity from.

#### Scenario: Unknown orchestrator id

- **WHEN** a mutation request names an `orch_id` that does not exist
- **THEN** the server responds 404 Not Found

#### Scenario: Non-numeric orchestrator id

- **WHEN** a mutation request's `orch_id` path segment is not a valid integer
- **THEN** the server responds 400 Bad Request

#### Scenario: Orchestrator with no live coordinator

- **WHEN** a mutation request targets an orchestrator whose coordinator role has no live binding (e.g. its task was deleted, or the binding was moved via `hera_move`)
- **THEN** the server responds 409 Conflict

### Requirement: Spawn a hera worker over REST

`POST /api/hera/orchestrators/{orch_id}/workers` SHALL create a born-bound worker task under the target orchestrator, acting as its coordinator (per the resolution requirement above) — the REST equivalent of `hera_spawn_worker`. The request body SHALL require `prompt` (non-empty) and MAY supply `role_name`, `project`, `branch`, `backend`, and `model`, with the same defaulting rules as the MCP tool (role name derived from the prompt slug and uniquified if omitted; project/branch/backend default to the coordinator's own). On success the server SHALL respond 201 with the new role's `role_id`, `orch_id`, `name`, `kind`, and the created task's `argus_task_id`, `task_name`, `task_status`.

#### Scenario: Spawns a worker with defaults

- **WHEN** a request supplies only `prompt` for an orchestrator with a live coordinator
- **THEN** the server creates a worker role + bound task (name derived from the prompt, project/branch/backend from the coordinator) and responds 201

#### Scenario: Missing prompt rejected

- **WHEN** a spawn-worker request omits `prompt`
- **THEN** the server responds 400 Bad Request

#### Scenario: Unknown backend rejected

- **WHEN** a spawn-worker request names a `backend` that is not configured
- **THEN** the server responds 400 Bad Request, and no task, role, or binding is created

### Requirement: Send a hera message over REST, as the orchestrator's coordinator

`POST /api/hera/orchestrators/{orch_id}/messages` SHALL send a message from the target orchestrator's coordinator role (resolved per the shared precondition) to another role in the same orchestrator — the REST equivalent of `hera_send`, narrowed to coordinator-as-sender only (no `from_role_id`, no `status` field; matching `hera_send`'s own rule that a coordinator sender needs no status). The request body SHALL require `to` (the recipient's `role_id`, unique within the orchestrator), `tldr` (non-empty, ≤120 chars), and `body` (non-empty, ≤64 KiB), and MAY supply `in_reply_to` (a prior message id). On success the server SHALL respond 201 with `message_id`, `to_role_id`, and `delivery_mode`.

#### Scenario: Coordinator sends to a worker

- **WHEN** a request supplies a valid `to` role_id within the same orchestrator plus `body` and `tldr`
- **THEN** the message is recorded from the coordinator role to that role and the server responds 201 with the message id

#### Scenario: Recipient not found

- **WHEN** a request's `to` role_id does not exist, or belongs to a different orchestrator than `orch_id`
- **THEN** the server responds 404 Not Found

#### Scenario: Self-send rejected

- **WHEN** a request's `to` names the orchestrator's own coordinator role
- **THEN** the server responds 409 Conflict, matching `ErrHeraMessageSelfSend`

#### Scenario: Missing tldr rejected

- **WHEN** a request omits `tldr` or supplies one over 120 characters
- **THEN** the server responds 400 Bad Request

#### Scenario: Body too large

- **WHEN** a request's `body` exceeds 64 KiB
- **THEN** the server responds 413 Request Entity Too Large

#### Scenario: Recipient inbox full or sender rate-limited

- **WHEN** the recipient already holds 500 unread messages, or the sending orchestrator's coordinator has sent 50+ messages in the last minute
- **THEN** the server responds 429 Too Many Requests

### Requirement: Author hera plan nodes over REST

`POST /api/hera/orchestrators/{orch_id}/plan/nodes` SHALL create one PLANNED node (a worker or sub-coordinator role with no live agent yet) under the target orchestrator — the REST equivalent of `hera_plan_node`. The request body SHALL require `name` and, depending on `kind` (`worker` default, or `subcoord`), either `prompt` (worker) or `goal` (subcoord); it MAY supply `project` (defaults to the coordinator's own). The name SHALL be uniquified within the orchestrator exactly as the MCP tool does. On success the server SHALL respond 201 with `role_id`, `name`, `project`, `kind`, and `status: "planned"`.

`POST /api/hera/orchestrators/{orch_id}/plan` SHALL create a whole plan graph — a set of planned nodes plus blocking edges among them — in one transaction, the REST equivalent of `hera_plan`. The request body SHALL supply a non-empty `nodes` array (each per the single-node shape above) and MAY supply an `edges` array of `{blocked, blocker}` pairs, where each side names either an in-batch node's `name` or an existing role's current `name` (edges reference names, not IDs, because in-batch nodes have no ID until the transaction commits). Any node or edge validation failure SHALL roll back the entire call — no partial graph is persisted.

#### Scenario: Creates a single planned node

- **WHEN** a request supplies `name` and `prompt` for a `kind=worker` (default) node
- **THEN** the server creates a planned role with no binding and responds 201 with `status: "planned"`

#### Scenario: Subcoord node requires a goal

- **WHEN** a request supplies `kind=subcoord` without `goal`
- **THEN** the server responds 400 Bad Request

#### Scenario: Whole-graph creation is all-or-nothing

- **WHEN** a `POST .../plan` request's `edges` array references a name that matches neither an in-batch node nor an existing role
- **THEN** the server responds 400 Bad Request and creates no nodes and no edges from that request

#### Scenario: Whole-graph edges reference in-batch names

- **WHEN** a `POST .../plan` request's `edges` array names two nodes present in its own `nodes` array
- **THEN** both nodes and the blocking edge between them are created in one transaction

### Requirement: Update and cancel a planned hera node over REST

`PATCH /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}` SHALL edit a PLANNED node's `prompt` and/or `project` — the REST equivalent of `hera_plan_node_update`. The request body SHALL supply at least one of `prompt` or `project`. The server SHALL reject the request with 409 Conflict if the named node has already materialized (holds a binding) — its prompt has already been delivered.

`POST /api/hera/orchestrators/{orch_id}/plan/nodes/{role_id}/cancel` SHALL cancel a PLANNED node — the REST equivalent of `hera_plan_node_cancel`: stamps `cancelled_at`, excludes it from materialization, and unblocks its dependents. The server SHALL reject the request with 409 Conflict if the node has already materialized.

#### Scenario: Update edits a planned node

- **WHEN** a `PATCH` request supplies a new `prompt` for a planned (not yet materialized) node
- **THEN** the node's stored prompt is updated and the server responds 200

#### Scenario: Update rejected for a materialized node

- **WHEN** a `PATCH` request targets a role_id that already holds a binding
- **THEN** the server responds 409 Conflict

#### Scenario: Cancel a planned node

- **WHEN** a cancel request targets a planned (not yet materialized) node
- **THEN** the node is stamped cancelled, its dependents are unblocked, and the server responds 200

#### Scenario: Cancel rejected for a materialized node

- **WHEN** a cancel request targets a role_id that already holds a binding
- **THEN** the server responds 409 Conflict

### Requirement: Manage hera blocking edges over REST

`POST /api/hera/orchestrators/{orch_id}/plan/blocks` SHALL add a blocking edge between two existing roles in the target orchestrator, addressed by `blocked_role_id` and `blocker_role_id` — the REST equivalent of `hera_block`. The server SHALL reject the request if it would create a cycle, if the two roles are in different orchestrators, or if `blocked_role_id == blocker_role_id`, mapping the existing `internal/db` sentinel errors (`ErrHeraBlockCycle`, `ErrHeraBlockCrossOrchestrator`, `ErrHeraBlockSelf`) to 400/409 responses.

`DELETE /api/hera/orchestrators/{orch_id}/plan/blocks` (with `blocked_role_id` and `blocker_role_id` as query parameters) SHALL remove a blocking edge — the REST equivalent of `hera_unblock`. Removing a non-existent edge SHALL succeed as an idempotent no-op.

#### Scenario: Add a blocking edge

- **WHEN** a request names two existing roles in the same orchestrator with no edge between them and no resulting cycle
- **THEN** the edge is created and the server responds 201

#### Scenario: Cyclic edge rejected

- **WHEN** a request's edge would create a cycle in the orchestrator's block graph
- **THEN** the server responds 409 Conflict and no edge is created

#### Scenario: Cross-orchestrator edge rejected

- **WHEN** a request's two role_ids belong to different orchestrators
- **THEN** the server responds 400 Bad Request

#### Scenario: Remove a blocking edge is idempotent

- **WHEN** a `DELETE` request names an edge that does not exist (already removed, or never created)
- **THEN** the server responds 200 with no error
