## MODIFIED Requirements

### Requirement: Hera orchestration roster endpoint

The REST API SHALL expose `GET /api/hera`, a read-only endpoint returning the Hera orchestration roster: a list of orchestrators — each with `id`, `name`, `pinned`, `archived`, `kanban_status` (`active`/`backlog`/`blocked`/`done`), and its non-freelance `roles` — plus a top-level `freelance` list of hoisted freelance roles. Each role SHALL carry `role_id`, `orch_id`, `name`, `kind` (`coordinator`/`worker`/`freelance`), `status` (`idle`/`working`/`blocked`/`done`, or empty when no status row exists), `task_id`, `task_name`, `task_status`, `live`, `ready_to_close`, and `archived`. The endpoint MUST be authenticated like every other `/api/*` route. The handler MUST source all data from the database and MUST NOT import the TUI Hera package (to keep tview out of the API binary).

`kanban_status` is emitted as-is for every orchestrator regardless of nesting — the endpoint does not resolve canonical parents or otherwise distinguish top-level from nested orchestrators, so a nested orchestrator's own (rail-inert) `kanban_status` value is still visible in its envelope. Mutating `kanban_status` over REST is out of scope: it falls under the existing standing exception that Hera mutations are TUI-only (`GET /api/hera` stays read-only in every field).

Derived from: `internal/api/hera.go` (`heraOrchJSON`, `handleHera`).

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
