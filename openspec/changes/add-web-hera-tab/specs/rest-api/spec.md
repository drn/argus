# rest-api

## ADDED Requirements

### Requirement: Hera orchestration roster endpoint

The REST API SHALL expose `GET /api/hera`, a read-only endpoint returning the Hera orchestration roster: a list of orchestrators — each with `id`, `name`, `pinned`, `archived`, and its non-freelance `roles` — plus a top-level `freelance` list of hoisted freelance roles. Each role SHALL carry `role_id`, `orch_id`, `name`, `kind` (`coordinator`/`worker`/`freelance`), `status` (`idle`/`working`/`blocked`/`done`, or empty when no status row exists), `task_id`, `task_name`, `task_status`, `live`, `ready_to_close`, and `archived`. The endpoint MUST be authenticated like every other `/api/*` route. The handler MUST source all data from the database and MUST NOT import the TUI Hera package (to keep tview out of the API binary).

#### Scenario: Empty roster
- **WHEN** an authenticated client requests `/api/hera` with no orchestrators present
- **THEN** the response is `200` with empty `orchestrators` and `freelance` arrays

#### Scenario: Bound coordinator role
- **WHEN** an orchestrator has a coordinator role with a live binding to an argus task
- **THEN** that role appears under the orchestrator's `roles` with `live: true`, its hera `status`, and the bound task's `task_id`/`task_name`/`task_status`

#### Scenario: Ready-to-close flag
- **WHEN** a bound role's task carries `meta:hera.ready_to_close=true`
- **THEN** that role's `ready_to_close` is `true`

#### Scenario: Freelance roles are hoisted
- **WHEN** an active orchestrator has an active freelance-kind role
- **THEN** that role appears in the top-level `freelance` list and not in the orchestrator's `roles`

#### Scenario: Freelance hoist suppression
- **WHEN** a freelance role is archived, or its orchestrator is archived
- **THEN** that role stays nested in its orchestrator's `roles` and is not hoisted

#### Scenario: Authentication required
- **WHEN** `/api/hera` is requested without a valid bearer token or `?token=`
- **THEN** the response is `401`

#### Scenario: Store read failure
- **WHEN** a required store read (orchestrators, live bindings, or an orchestrator's roles) fails
- **THEN** the response is `500` and no partial body is written
