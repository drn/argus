## MODIFIED Requirements

### Requirement: Native hera_* MCP tool surface

The system SHALL register fourteen native `hera_*` MCP tools with the same names, parameters, descriptions, and required lists as the external Hera daemon where they overlap: `hera_new_orchestrator`, `hera_join`, `hera_move`, `hera_rebind`, `hera_send`, `hera_inbox`, `hera_mark_read`, `hera_status`, `hera_spawn_worker`, `hera_tree_updates`, `hera_get_messages`, and the three plan-authoring tools `hera_plan_node`, `hera_block`, and `hera_plan`. The plan-authoring tools SHALL be coordinator-only (a worker or freelance caller is rejected, mirroring `hera_spawn_worker`): `hera_plan_node` creates a planned node, `hera_block` adds a blocking edge (cycle-checked, single-orchestrator), and `hera_plan` submits a whole graph of nodes and edges in one call. The tools SHALL be available only when the hera service is wired AND task management is enabled (caller resolution via `cwd` requires task management). A dup-tool guard SHALL suppress any plugin tool scoped `hera` while native Hera is enabled, so in-tree and plugin tools never both appear.

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

### Requirement: Caller role resolution from cwd with orchestrator disambiguation

The system SHALL resolve the calling agent's hera role by mapping `cwd` to an argus task (subject to the shared-worktree disambiguation described under the `mcp-server` capability's "Caller identity resolved by id or cwd" requirement), then to that task's live binding+role. When the task holds exactly one live binding the `orchestrator` argument is optional; when it holds two or more, the tool SHALL return a disambiguation error listing the caller's orchestrator names, and the caller MUST re-call with `orchestrator=<name>`. An unbound task SHALL get an error instructing it to call `hera_join` or `hera_new_orchestrator`.

Binding resolution SHALL key first on the resolved task's `argus_task_id` and, on a miss, fall back to a lookup keyed on the task's `worktree_path` (scoped to the same orchestrator when one is given) — the same key the live-binding uniqueness index is defined on (BUG-059). This closes the gap where a `cwd` resolves to a task whose id has no live binding even though the binding physically rooted at that worktree is live: the worktree-keyed fallback resolves the exact binding an attach `hera_join` would collide with, so a claim now succeeds precisely when an attach would have been rejected. The fallback never rewrites the resolved binding's `argus_task_id` — it only returns the existing binding's identity; reconciling a binding whose own `argus_task_id` has drifted is the separate `hera_rebind` tool.

Derived from: `internal/mcp/hera.go:398` (`resolveCallerRole`), `internal/mcp/hera.go:457` (`liveBindingForOrch`), `internal/mcp/hera.go:478` (`liveBindingForTask`), `internal/mcp/hera.go:494` (`buildHeraAmbiguousError`).

#### Scenario: Single binding resolves without orchestrator

- **WHEN** the caller's task holds exactly one live binding and no `orchestrator` is passed
- **THEN** the role resolves successfully

#### Scenario: Ambiguous binding lists options

- **WHEN** the caller's task holds two or more live bindings and no `orchestrator` is passed
- **THEN** the tool errors with the list of bound orchestrator names to disambiguate

#### Scenario: Unbound task is guided

- **WHEN** the caller's task holds no live binding
- **THEN** the tool errors instructing it to call hera_join or hera_new_orchestrator

#### Scenario: Worktree-keyed fallback resolves a binding the task-keyed lookup missed

- **WHEN** the resolved task's id has no live binding under the given orchestrator, but a live binding for that orchestrator exists at the task's `worktree_path`
- **THEN** the role resolves to that worktree-keyed binding instead of reporting no binding exists

### Requirement: hera_new_orchestrator bootstraps and claims the coordinator role

The system SHALL, on `hera_new_orchestrator`, create (or idempotently fetch) the named orchestrator, then transactionally create a coordinator role bound to the calling task's worktree, persisting the caller's argus project on the role. It SHALL reject the call when the calling task already holds a live binding under that (target) orchestrator, directing the caller to `hera_join`. It SHALL ALSO reject the call when a live binding already occupies the caller's `worktree_path` under that orchestrator even if the task-keyed check above found nothing — a reused worktree path can leave the caller's cwd resolving to a task whose id has no binding while the physical worktree does (BUG-059) — directing the caller to `hera_join` to claim it, or `hera_rebind` when that existing binding's `argus_task_id` differs from the caller's resolved task. It SHALL ALSO reject the call — before creating or fetching the orchestrator, so no orphan orchestrator is left behind — when the calling task already holds a live **coordinator**-kind binding under a DIFFERENT orchestrator: a coordinator dispatches work with `hera_spawn_worker` (whose `project=` targets any repo) and MUST NOT bind its own session as a second coordinator of another orchestrator, so the rejection error SHALL direct the caller to spawn a worker, or — for genuine multi-project/multi-phase decomposition — to use the worker-promotion pattern or a `kind=subcoord` plan node. A caller re-calling for the SAME orchestrator it already coordinates falls through to the target-orchestrator rejection above (the `hera_join` guidance); a caller holding only worker/freelance bindings (worker self-promotion) or no binding at all (fresh bootstrap) SHALL be allowed to proceed. It SHALL mirror the role to the `task_meta` "hera" namespace best-effort (a mirror failure never undoes local state). Required args: `cwd`, `name`, `coordinator_role_name`.

Derived from: `internal/mcp/hera.go:511` (`toolHeraNewOrchestrator`).

#### Scenario: First call creates orchestrator + coordinator binding

- **WHEN** a task with no binding under orchestrator X calls hera_new_orchestrator(name=X)
- **THEN** orchestrator X exists, a coordinator role bound to the caller is created, and the binding id is returned

#### Scenario: Re-bootstrap under an already-bound orchestrator is rejected

- **WHEN** the caller already holds a live binding under the target orchestrator
- **THEN** the tool errors and directs the caller to hera_join

#### Scenario: A coordinator cannot create another orchestrator on its own session

- **WHEN** a task that already holds a live coordinator binding under orchestrator A calls hera_new_orchestrator(name=B) for a different orchestrator B
- **THEN** the tool errors, no orchestrator B and no second coordinator binding are created on that task, and the error directs the caller to hera_spawn_worker (or a kind=subcoord plan node for a real sub-team)

#### Scenario: Worker self-promotion to sub-coordinator is still allowed

- **WHEN** a task holding only a live worker binding calls hera_new_orchestrator(name=B)
- **THEN** orchestrator B and a coordinator role bound to the caller are created (the worker-promotion pattern succeeds)

#### Scenario: A worktree collision under a drifted task id is rejected with an actionable message

- **WHEN** the caller's `worktree_path` already holds a live binding under the target orchestrator whose `argus_task_id` differs from the caller's resolved task id
- **THEN** the tool errors directing the caller to `hera_join` to claim it (or `hera_rebind` to reconcile the drift) instead of the role+binding insert surfacing a raw uniqueness-constraint error

### Requirement: hera_join claims an existing role or attaches a new one

The system SHALL support two `hera_join` modes. With `role_name` omitted (claim mode) it SHALL return the caller's existing binding for the resolved orchestrator plus its unread message count, without cancelling any pending doorbell deliveries. With `role_name` + `kind` supplied (attach mode) it SHALL create a new role+binding of kind `worker` or `freelance` (rejecting `coordinator` — that path is `hera_new_orchestrator`), persisting the attaching task's project, optionally setting an initial status, and rejecting a second binding under an orchestrator the task is already bound to — whether that existing binding is found by the caller's resolved task id OR by the caller's `worktree_path` (BUG-059: a reused worktree can leave the task-keyed check blind to a binding that is nonetheless live at that physical location). When the collision is detected via the worktree-keyed check and the existing binding's `argus_task_id` differs from the caller's resolved task, the rejection SHALL additionally point at `hera_rebind` to reconcile the drift. Attach mode SHALL ALSO reject the call — creating no binding — when the calling task already holds a live binding under a DIFFERENT orchestrator than the one being joined, directing the caller to the `hera_move` tool instead: joining is for an unbound task or a fresh orchestrator, not for relocating an existing membership. Attach mode requires `orchestrator`.

Derived from: `internal/mcp/hera.go:640` (`toolHeraJoin`).

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

#### Scenario: Claim mode resolves the live task despite a stale worktree collision

- **WHEN** hera_join claim mode is called with a cwd whose worktree is shared by a stale/archived task (holding no live binding) and the live task actually bound to the target orchestrator
- **THEN** the response reports the live binding's role identity, not an error saying no binding exists

#### Scenario: Attach mode rejects a worktree collision without leaking a raw constraint error

- **WHEN** hera_join attach mode is called and a live binding for the target orchestrator already exists at the caller's worktree_path, even though no binding is found for the caller's resolved task id
- **THEN** the tool errors with an actionable message (claim it via hera_join, or hera_rebind if the existing binding's argus_task_id has drifted) and never surfaces a raw uniqueness-constraint error

## ADDED Requirements

### Requirement: hera_rebind reconciles a stuck or drifted binding

The system SHALL provide `hera_rebind(cwd, orchestrator, [role_name])` as the supported repair path for a hera binding stuck in the claim-says-none / attach-says-exists state (BUG-059: a worktree path reused across two argus tasks' lifecycles left the live binding pointing at a stale `argus_task_id`, so delivery and status routing — which key on the binding's `argus_task_id` — go nowhere even after the task-then-worktree lookup fallback). `hera_rebind` SHALL reconcile the binding to the caller's real live argus task WITHOUT tearing down the argus session: it reuses the existing role (so the role's prompt, messages, and status, all keyed on `role_id`, survive) and refreshes only the binding row.

`hera_rebind` SHALL resolve the caller's real live task from `cwd` (using the shared-worktree disambiguation specified under the `mcp-server` capability), then gather the caller's live bindings under the named orchestrator keyed both by the resolved task id and by the worktree path (de-duplicated). It SHALL pick the keeper role from those candidates: `role_name`, when given, MUST name a role holding one of the candidate bindings; otherwise exactly one role MUST be represented among the candidates. When the keeper binding already points at the caller's task and worktree and is the sole occupant of both slots, the call SHALL be a no-op that reports success without changing any row. Otherwise it SHALL end the keeper's stale binding and insert one clean binding under the keeper role at the caller's `argus_task_id` and `worktree_path`, and SHALL best-effort mirror `meta:hera.role` to the caller's task.

`hera_rebind` SHALL REFUSE (return a tool error, change no rows) rather than guess when the state is genuinely ambiguous: the cwd itself is ambiguous (two live `in_progress` tasks share the worktree); no candidate live binding exists to reconcile (directing the caller to `hera_join` instead — `hera_rebind` repairs, it does not create); more than one role holds a candidate binding and no `role_name` disambiguates; or a DIFFERENT role's live binding already occupies the caller's target task or worktree slot.

Derived from: `internal/mcp/hera.go:901` (`toolHeraRebind`).

#### Scenario: Rebind reconciles a drifted binding to the caller's live task

- **WHEN** hera_rebind(cwd, orchestrator=X) is called and the live binding for X at the caller's worktree points at a stale argus_task_id while the caller's cwd resolves unambiguously to a different, real live task
- **THEN** the stale binding is ended, a clean binding is created under the SAME role pointing at the caller's argus_task_id and worktree_path, both the task-keyed and worktree-keyed lookups now resolve that one binding, and the role's messages survive

#### Scenario: Rebind is a no-op when the binding is already consistent

- **WHEN** hera_rebind(cwd, orchestrator=X) is called and the live binding already points at the caller's task and worktree and is the sole occupant of both slots
- **THEN** the response reports success without changing any binding row

#### Scenario: Rebind refuses an ambiguous cwd

- **WHEN** hera_rebind(cwd, orchestrator=X) is called and two live in_progress argus tasks share the caller's worktree
- **THEN** the tool errors naming the candidates and changes no binding row

#### Scenario: Rebind refuses when multiple roles are bound and no role_name is given

- **WHEN** hera_rebind(cwd, orchestrator=X) is called, the caller's task and worktree resolve to bindings held by DIFFERENT roles under X, and no role_name is supplied
- **THEN** the tool errors listing the candidate roles and directing the caller to pass role_name, and changes no binding row

#### Scenario: Rebind refuses when there is nothing to reconcile

- **WHEN** hera_rebind(cwd, orchestrator=X) is called and no live binding for X exists at the caller's worktree or task
- **THEN** the tool errors directing the caller to hera_join, and creates no binding
