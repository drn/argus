## MODIFIED Requirements

### Requirement: Orchestrator, role, and binding storage model

The system SHALL store orchestrators (`hera_orchestrators`), roles of kind `coordinator`/`worker`/`freelance` (`hera_roles`), and role↔task bindings (`hera_bindings`). It SHALL enforce, via partial unique indexes over `WHERE ended_at IS NULL`: at most one live binding per role, one live binding per (argus task, orchestrator), and one live binding per (worktree path, orchestrator). A single argus task MAY therefore hold live bindings under several distinct orchestrators at once, but never two under the same one.

Derived from: `internal/db/schema.go:447` (live-role unique index), `internal/db/schema.go:448` (live task+orchestrator unique index), `internal/db/schema.go:449` (live worktree+orchestrator unique index), `internal/db/hera.go:71` (role-kind constants).

#### Scenario: Second live binding under the same orchestrator is rejected

- **WHEN** a task already holds a live binding under an orchestrator and a second live binding under the same orchestrator is attempted
- **THEN** the unique index rejects it

#### Scenario: Task may bind under multiple orchestrators

- **WHEN** a task holds a live binding under orchestrator A
- **THEN** it may also hold a live binding under orchestrator B (the constraint is per-orchestrator, not global) — reached only via `hera_new_orchestrator` self-promotion (the worker-promotion/subcoord pattern); neither `hera_join` nor `hera_move` ever leaves a task bound under two orchestrators at once (see their respective requirements)

### Requirement: Native hera_* MCP tool surface

The system SHALL register thirteen native `hera_*` MCP tools with the same names, parameters, descriptions, and required lists as the external Hera daemon where they overlap: `hera_new_orchestrator`, `hera_join`, `hera_move`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`, `hera_tree_updates`, `hera_get_messages`, and the three plan-authoring tools `hera_plan_node`, `hera_block`, and `hera_plan`. The plan-authoring tools SHALL be coordinator-only (a worker or freelance caller is rejected, mirroring `hera_spawn_worker`): `hera_plan_node` creates a planned node, `hera_block` adds a blocking edge (cycle-checked, single-orchestrator), and `hera_plan` submits a whole graph of nodes and edges in one call. The tools SHALL be available only when the hera service is wired AND task management is enabled (caller resolution via `cwd` requires task management). A dup-tool guard SHALL suppress any plugin tool scoped `hera` while native Hera is enabled, so in-tree and plugin tools never both appear.

#### Scenario: Tools require task management

- **WHEN** task management is disabled
- **THEN** the `hera_*` tools report "hera not configured" rather than acting

#### Scenario: Native and plugin hera tools are mutually exclusive

- **WHEN** native Hera is enabled
- **THEN** any plugin tool scoped `hera` is suppressed so only the in-tree tools appear

#### Scenario: Plan-authoring tools are coordinator-only

- **WHEN** a worker or freelance role calls `hera_plan_node`, `hera_block`, or `hera_plan`
- **THEN** the tool errors that only coordinators may author the plan

#### Scenario: Whole-graph submission in one call

- **WHEN** a coordinator calls `hera_plan` with a set of nodes and blocking edges
- **THEN** the planned nodes and their cycle-checked edges are created together

### Requirement: hera_join claims an existing role or attaches a new one

The system SHALL support two `hera_join` modes. With `role_name` omitted (claim mode) it SHALL return the caller's existing binding for the resolved orchestrator plus its unread message count, without cancelling any pending doorbell deliveries. With `role_name` + `kind` supplied (attach mode) it SHALL create a new role+binding of kind `worker` or `freelance` (rejecting `coordinator` — that path is `hera_new_orchestrator`), persisting the attaching task's project, optionally setting an initial status, and rejecting a second binding under an orchestrator the task is already bound to. Attach mode SHALL ALSO reject the call — creating no binding — when the calling task already holds a live binding under a DIFFERENT orchestrator than the one being joined, directing the caller to the `hera_move` tool instead: joining is for an unbound task or a fresh orchestrator, not for relocating an existing membership. Attach mode requires `orchestrator`.

Derived from: `internal/mcp/hera.go:358` (`toolHeraJoin`), `internal/mcp/hera.go:376` (claim mode), `internal/mcp/hera.go:397` (attach mode + coordinator rejection), `internal/mcp/hera.go:421` (already-bound rejection).

#### Scenario: Claim mode returns the existing binding

- **WHEN** a bound task calls hera_join with no role_name
- **THEN** the response reports its orchestrator, role name, kind, binding id, and unread count, and does NOT cancel pending deliveries

#### Scenario: Attach mode rejects coordinator kind

- **WHEN** hera_join is called in attach mode with kind=coordinator
- **THEN** the tool errors and directs the caller to hera_new_orchestrator

#### Scenario: Attach mode creates a worker/freelance role

- **WHEN** an unbound task calls hera_join with orchestrator, role_name, and kind=worker
- **THEN** a new worker role+binding is created and the binding id is returned

#### Scenario: Same-orchestrator conflict is unaffected

- **WHEN** a task already holding a live binding under orchestrator A calls hera_join attach mode targeting orchestrator A again
- **THEN** the tool errors as before, directing the caller to hera_join without role_name, and no binding is ended or created

#### Scenario: Attach mode rejects and redirects when bound under a different orchestrator

- **WHEN** a task already holding a live binding under orchestrator A calls hera_join attach mode targeting a different orchestrator B
- **THEN** the tool errors, directs the caller to call hera_move instead, and neither ends the binding under A nor creates one under B

## ADDED Requirements

### Requirement: hera_move relocates the caller's binding to a different orchestrator

The system SHALL, on `hera_move`, relocate the calling task's live hera binding to a different orchestrator: it SHALL resolve the caller's current live binding (via the same cwd→task→binding resolution used elsewhere, accepting an optional `from_orchestrator` to disambiguate when the task holds 2+ live bindings), then — transactionally — end that binding (`ended_at`/`end_reason: "moved"`) and create a new role+binding of kind `worker` or `freelance` under the target `orchestrator` (rejecting `coordinator`, mirroring `hera_join`). It SHALL reject the call, ending and creating nothing, when the calling task holds no live binding at all (directing the caller to `hera_join` or `hera_new_orchestrator` instead — there is nothing to move) or when the resolved source orchestrator equals the target orchestrator (a no-op; directing the caller to `hera_join` without `role_name` to see its current binding). The response SHALL report the source orchestrator and role name that were moved, plus the new binding id. Required args: `cwd`, `orchestrator`, `role_name`, `kind`. Optional args: `from_orchestrator`, `status`.

#### Scenario: Happy path moves the binding

- **WHEN** a task holding a live binding under orchestrator A calls hera_move targeting orchestrator B with a role_name and kind
- **THEN** the binding under A is ended with end_reason "moved", a new role+binding is created under B, and the response reports A + the moved role's name plus the new binding id

#### Scenario: Nothing to move

- **WHEN** an unbound task calls hera_move
- **THEN** the tool errors that there is nothing to move and directs the caller to hera_join or hera_new_orchestrator, creating no binding

#### Scenario: Moving to the same orchestrator is a no-op error

- **WHEN** a task holding a live binding under orchestrator A calls hera_move targeting orchestrator A
- **THEN** the tool errors and directs the caller to hera_join without role_name, ending and creating nothing

#### Scenario: Ambiguous caller requires from_orchestrator

- **WHEN** a task holding live bindings under two orchestrators calls hera_move without from_orchestrator
- **THEN** the tool errors listing the bound orchestrator names, and a follow-up call supplying from_orchestrator succeeds

#### Scenario: Coordinator kind is rejected

- **WHEN** hera_move is called with kind=coordinator
- **THEN** the tool errors and directs the caller to hera_new_orchestrator
