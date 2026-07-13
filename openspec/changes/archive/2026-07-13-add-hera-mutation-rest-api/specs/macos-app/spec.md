## RENAMED Requirements

- FROM: `### Requirement: Hera roster (read-only)`
- TO: `### Requirement: Hera roster with spawn, send, and plan mutations`

## MODIFIED Requirements

### Requirement: Hera roster with spawn, send, and plan mutations

The system SHALL display the hera role roster for a task or orchestrator by reading `GET /api/hera`, as before, and SHALL additionally provide mutation controls that call the new REST endpoints (`/api/hera/orchestrators/{orch_id}/...`), acting as the target orchestrator's coordinator in every case:

- **Spawn worker** — reachable from a coordinator/orchestrator row: a form collecting `prompt` (required) plus optional role name, project, branch, backend, and model, calling `POST .../workers`.
- **Send message** — reachable from a coordinator row: a compose form with a recipient picker (populated from that orchestrator's roles in the already-fetched roster), body, and tldr, calling `POST .../messages`. No sender-role picker is offered — sends are always attributed to the orchestrator's coordinator.
- **Plan mutations** — reachable from an orchestrator's plan view: create a planned node (name, prompt or goal, optional project), edit a planned node's prompt/project, cancel a planned node, and add/remove a blocking edge between two roles, calling the corresponding `plan/nodes` and `plan/blocks` endpoints.

`hera_join`/`hera_move` (re-binding a task to a different orchestrator) remain unexposed in this app — out of scope for this change, matching the TUI/MCP-only scope decision.

#### Scenario: Roster renders with mutation controls

- **WHEN** the user opens the Hera tab for an orchestrator with a live coordinator
- **THEN** the app renders the role tree from `GET /api/hera` and presents spawn-worker, send-message, and plan-mutation controls for that orchestrator

#### Scenario: Spawn worker from the app

- **WHEN** the user submits the spawn-worker form with a prompt for an orchestrator with a live coordinator
- **THEN** the app calls `POST /api/hera/orchestrators/{orch_id}/workers` and, on success, the new worker appears in the roster on the next refresh

#### Scenario: Send message from the app

- **WHEN** the user submits the send-message form with a recipient, body, and tldr
- **THEN** the app calls `POST /api/hera/orchestrators/{orch_id}/messages` attributed to that orchestrator's coordinator, with no sender-role selector shown to the user

#### Scenario: Cancel a planned node requires confirmation

- **WHEN** the user chooses to cancel a planned (not yet materialized) node
- **THEN** the app shows a confirmation dialog before calling `POST .../plan/nodes/{role_id}/cancel`; a cancelled dialog issues no request

#### Scenario: Mutation on an orchestrator with no live coordinator

- **WHEN** the user attempts a spawn/send/plan mutation against an orchestrator whose coordinator has no live binding
- **THEN** the app surfaces the server's 409 response as an inline error rather than silently failing

#### Scenario: Join/move remain unavailable

- **WHEN** the user views the Hera tab
- **THEN** the app presents no control to re-bind a task's orchestrator membership (`hera_join`/`hera_move` stay TUI/MCP-only)
