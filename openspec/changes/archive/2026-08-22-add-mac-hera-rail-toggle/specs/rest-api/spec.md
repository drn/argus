## MODIFIED Requirements

### Requirement: Hera orchestration roster endpoint

The REST API SHALL expose `GET /api/hera`, a read-only endpoint returning the Hera orchestration roster: a list of orchestrators — each with `id`, `name`, `pinned`, `archived`, `kanban_status` (`active`/`backlog`/`blocked`/`done`), `subtree_cost_usd`, `bridge_parent_orch_id` and `bridge_parent_role_id` (both null when the orchestrator is top-level, both set to the parent orchestrator/role when it is nested beneath another orchestrator via a worker→coordinator bridge), `subtree_needs_input` (a boolean rollup, true when any role in the orchestrator's subtree needs input), and its non-freelance `roles` — plus a top-level `freelance` list of hoisted freelance roles. Each role SHALL carry `role_id`, `orch_id`, `name`, `kind` (`coordinator`/`worker`/`freelance`), `status` (`idle`/`working`/`blocked`/`done`, or empty when no status row exists), `task_id`, `task_name`, `task_status`, `live`, `ready_to_close`, `archived`, `needs_input` (a boolean mirroring the same daemon-authoritative idle-detection signal that drives `GET /api/tasks` and the SSE events stream), `tokens_input`, `tokens_cache_write_1h`, `tokens_cache_write_5m`, `tokens_cache_read`, `tokens_output`, and `cost_usd` (omitted or null when the role's resolved model has no rate-table entry for its accrued usage, or when its token totals are all zero — see `cost-estimation`). Both `cost_usd` and `subtree_cost_usd` SHALL be the PERSISTED, already-priced `cost_usd_accrued` values described in `cost-estimation`'s accrual-time-stamping requirement — the endpoint SHALL NOT compute or reprice cost against a live rate table on read. The endpoint MUST be authenticated like every other `/api/*` route. The handler MUST source all data from the database; nesting/bridging computation (canonical-parent resolution, `bridge_parent_orch_id`/`bridge_parent_role_id`, `subtree_needs_input`) MUST use the shared, tview-free `internal/hera/model` package rather than importing `internal/tui/hera` directly, keeping tview/tcell out of the API binary.

**Scope note (unchanged from prior scope):** `subtree_cost_usd` at THIS endpoint SHALL still be the sum of the orchestrator's OWN roles' cost only (every kind, including nuked ones) — it SHALL NOT recurse into orchestrators nested beneath it, even though `bridge_parent_orch_id` now exposes the same underlying bridge relationship. Extending `subtree_cost_usd` itself to recurse remains a separate, still-deferred follow-up; this change does not alter that field's semantics, only adds the new nesting/needs-input fields alongside it.

`kanban_status` continues to be emitted as-is for every orchestrator regardless of nesting (a nested orchestrator's own, rail-inert `kanban_status` value is still visible in its envelope) — but a client can now distinguish top-level from nested orchestrators itself via `bridge_parent_orch_id`, rather than needing to infer it. `subtree_cost_usd`, `bridge_parent_orch_id`/`bridge_parent_role_id`, `subtree_needs_input`, `needs_input`, and every per-role cost/token field are read-only: mutating any of them over REST is out of scope — this stays under the existing standing exception that Hera mutations are TUI-only (`GET /api/hera` stays read-only in every field).

These fields SHALL be populated regardless of which client renders them: the native TUI itself reads through this endpoint in `--remote` mode, and the web SPA renders no nesting/needs-input Hera UI yet (an explicit, separately-tracked follow-up, not a reason to omit the data here).

Derived from: `internal/api/hera.go` (`heraOrchJSON`, `heraRoleJSON`, `handleHera`), `internal/hera/model` (`BuildModel`, extracted from the former `internal/tui/hera/model.go`).

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

#### Scenario: A top-level orchestrator carries no bridge parent

- **WHEN** an orchestrator has no worker role bridging into it from another orchestrator
- **THEN** its `bridge_parent_orch_id` and `bridge_parent_role_id` fields are both null

#### Scenario: A nested orchestrator surfaces its bridge parent

- **WHEN** an orchestrator is nested beneath another orchestrator's worker role via a coordinator bridge
- **THEN** its `bridge_parent_orch_id` and `bridge_parent_role_id` identify that parent orchestrator and role

#### Scenario: subtree_needs_input rolls up from any role in the subtree

- **WHEN** any role within an orchestrator's subtree (including nested sub-orchestrators reached via bridges) currently needs input
- **THEN** that orchestrator's `subtree_needs_input` field is `true`

#### Scenario: A role's needs_input mirrors the daemon's idle-detection signal

- **WHEN** a role's bound task is flagged by the daemon's idle-detection system as needing input
- **THEN** that role's `needs_input` field is `true`, and `false` otherwise
