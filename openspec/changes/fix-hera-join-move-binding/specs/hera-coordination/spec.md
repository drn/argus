## MODIFIED Requirements

### Requirement: Orchestrator, role, and binding storage model

The system SHALL store orchestrators (`hera_orchestrators`), roles of kind `coordinator`/`worker`/`freelance` (`hera_roles`), and role↔task bindings (`hera_bindings`). It SHALL enforce, via partial unique indexes over `WHERE ended_at IS NULL`: at most one live binding per role, one live binding per (argus task, orchestrator), and one live binding per (worktree path, orchestrator). A single argus task MAY therefore hold live bindings under several distinct orchestrators at once, but never two under the same one.

Derived from: `internal/db/schema.go:447` (live-role unique index), `internal/db/schema.go:448` (live task+orchestrator unique index), `internal/db/schema.go:449` (live worktree+orchestrator unique index), `internal/db/hera.go:71` (role-kind constants).

#### Scenario: Second live binding under the same orchestrator is rejected

- **WHEN** a task already holds a live binding under an orchestrator and a second live binding under the same orchestrator is attempted
- **THEN** the unique index rejects it

#### Scenario: Task may bind under multiple orchestrators

- **WHEN** a task holds a live binding under orchestrator A
- **THEN** it may also hold a live binding under orchestrator B (the constraint is per-orchestrator, not global) — reached only via `hera_new_orchestrator` self-promotion (the worker-promotion/subcoord pattern) or via `hera_join` attach mode with `keep_existing: true`; plain `hera_join` attach mode no longer produces this shape by default (see "hera_join claims an existing role or attaches a new one")

### Requirement: hera_join claims an existing role or attaches a new one

The system SHALL support two `hera_join` modes. With `role_name` omitted (claim mode) it SHALL return the caller's existing binding for the resolved orchestrator plus its unread message count, without cancelling any pending doorbell deliveries. With `role_name` + `kind` supplied (attach mode) it SHALL create a new role+binding of kind `worker` or `freelance` (rejecting `coordinator` — that path is `hera_new_orchestrator`), persisting the attaching task's project, optionally setting an initial status, and rejecting a second binding under an orchestrator the task is already bound to. Attach mode SHALL, by default and before creating the new role+binding, end (set `ended_at`/`end_reason: "moved"`) any OTHER live binding the calling task holds under a DIFFERENT orchestrator, transactionally with the new binding's creation — a task joining a new orchestrator moves its membership rather than accumulating a second one. Attach mode SHALL accept an optional `keep_existing` boolean; when `true`, the prior-binding-ending step is skipped and today's duplicate-binding behavior is preserved. The attach-mode response SHALL report the orchestrator and role name of any prior binding that was ended, or omit that field when none was. Attach mode requires `orchestrator`.

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

#### Scenario: Attach mode moves an existing binding by default

- **WHEN** a task already holding a live binding under orchestrator A calls hera_join attach mode targeting a different orchestrator B, without `keep_existing`
- **THEN** the task's live binding under orchestrator A is ended (`ended_at`/`end_reason: "moved"`), a new role+binding is created under orchestrator B, and the response reports orchestrator A and the ended role's name as the binding that was moved

#### Scenario: keep_existing preserves the prior binding

- **WHEN** a task already holding a live binding under orchestrator A calls hera_join attach mode targeting a different orchestrator B with `keep_existing: true`
- **THEN** the binding under orchestrator A remains live, a new role+binding is created under orchestrator B, and the task now holds live bindings under both

#### Scenario: Same-orchestrator conflict is unaffected

- **WHEN** a task already holding a live binding under orchestrator A calls hera_join attach mode targeting orchestrator A again
- **THEN** the tool errors as before, directing the caller to hera_join without role_name, and no binding is ended or created
