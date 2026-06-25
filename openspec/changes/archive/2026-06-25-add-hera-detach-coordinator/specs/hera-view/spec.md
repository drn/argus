# Hera View

## MODIFIED Requirements

### Requirement: `J` adopts a freelancer or re-parents a coordinator

While the RAIL is focused, pressing `J` SHALL act on the current selection:

- A FREELANCER selection (a `freelance`-kind role row carrying a live argus task) SHALL open a target picker listing the active (non-archived) orchestrators.
- A COORDINATOR selection (a `coordinator`-kind role row, OR an orchestrator header whose orchestrator has a coordinator role and is not archived) SHALL open a target picker. The picker SHALL include, as a sentinel row at the TOP, a "Detach (make top-level)" entry that detaches the coordinator to top-level, followed by the OTHER active orchestrators (excluding the coordinator's own orchestrator) as re-parent targets.
- Any other selection (a managed worker role, an empty selection, an archived row) SHALL surface visible feedback that only a freelancer or coordinator can be adopted, and SHALL NOT create or change any role or binding (never a silent no-op).

The picker SHALL be a themed, focusable, dismissable modal in which typed characters narrow the list by case-insensitive substring on the orchestrator name, `Enter` selects the highlighted orchestrator, and `Esc` cancels without change. The picker SHALL name the row being adopted in its title. For a FREELANCER, when no eligible target orchestrator exists, pressing `J` SHALL surface visible feedback that a coordinator must be created first and SHALL NOT open the picker or create any role or binding. For a COORDINATOR the picker SHALL always open (the detach sentinel is always offered), so there is no "no eligible target" feedback path for a coordinator.

`J` SHALL be RAIL-focus-only. In a COORD or AGENT pane the `J` rune SHALL forward to the bound task's PTY like any other character; the lowercase `j` navigation key SHALL be unaffected. The adopt/reparent/detach role+binding writes are cheap local SQLite mutations and run synchronously on the tview event loop, consistent with the other rail mutations (rename/archive/pin/status/delete); they do NOT touch a worktree or session, so they never perceptibly block the loop. (This differs from worker SPAWN, which creates a worktree + PTY session and is therefore dispatched off-thread.)

#### Adopt (freelancer → worker)

Selecting an orchestrator for a freelancer SHALL adopt the freelancer's argus task into it by creating, server-side and without any agent action, through the SAME transactional DAO `hera_join`'s attach-mode and the born-bound spawn use (`CreateHeraRoleWithBinding`, not a duplicate implementation), so a binding-insert failure (e.g. a worktree-orchestrator uniqueness collision) rolls the freshly-created worker role back — no orphan role:

- a `worker` role under the chosen orchestrator whose name defaults to the freelancer's name and is de-collided (a numeric suffix appended) when an active role of that name already exists; the role SHALL record the freelancer's argus repo as its `argus_project`; and
- a live binding from the freelancer's argus task to that role, recording the freelancer's argus-task worktree path.

The freelancer's argus task SHALL be best-effort stamped `meta:hera.role=worker` for parity with `hera_join`; a transient failure to stamp SHALL NOT undo or fail the binding. The adopt SHALL be REJECTED with visible feedback, creating no role or binding, when: the freelance row has no argus task id; or the task already holds a live binding under the chosen orchestrator (a duplicate).

#### Re-parent (coordinator → sub-coordinator)

Selecting a parent orchestrator for a coordinator SHALL re-parent it by creating a `worker` role under the chosen parent bound to the coordinator's coordinator argus task — the multi-binding the orchestration tree renders as a nested sub-coordinator. The coordinator's whole subtree moves with it (the subtree derives from the coordinator, which is untouched). The coordinator argus task + worktree SHALL be resolved from the coordinator role's LATEST binding (live, else most-recent ended) so a dormant coordinator can still be re-parented.

The re-parent SHALL be REJECTED with visible feedback when the chosen parent IS the coordinator's own orchestrator, or is a descendant of it (a cycle), or the coordinator has no coordinator role / no binding to re-parent.

**Teardown invariant (BUG-026):** before creating the new link, the re-parent SHALL end EVERY prior parent-link of the coordinator's task by ROLE id — both LIVE and ENDED. Live parent-link bindings SHALL be ended with reason `reparented`; then every distinct parent-link role (any role other than the coordinator's own coordinator role, reached through any binding of that task) SHALL be deleted so its bindings cascade away. This guarantees that repeated re-parents never pile up de-collided duplicate link roles (`name`, `name-2`, `name-3`, …); exactly one clean link remains. The teardown is the SAME single-source operation that detach (below) runs without recreating a link.

#### Detach (coordinator → top-level)

Selecting the "Detach (make top-level)" sentinel for a coordinator SHALL un-nest it back to a root orchestrator with no parent, WITHOUT creating any new link. Detach resolves the coordinator's argus task + coord role from the coordinator role's LATEST binding (the same resolution re-parent uses), then runs ONLY the teardown invariant above (end every live parent-link binding with reason `detached`, then delete every distinct parent-link role so its bindings cascade) — it recreates NO link. The coordinator's own coordinator role and its coordinator binding are NEVER touched, so the coordinator and its whole subtree survive intact, now at top-level.

Detach SHALL be IDEMPOTENT: a coordinator that is already top-level (no parent-link roles) SHALL be a clean no-op (no error, no role or binding changed). Detach SHALL be REJECTED with visible feedback only when the orchestrator no longer exists, or the coordinator has no coordinator role / no binding to resolve its task.

#### Scenario: `J` on a freelancer creates a worker binding under the chosen coordinator

- **WHEN** the operator selects a freelancer, presses `J`, and picks an orchestrator
- **THEN** a `worker` role and a live binding from the freelancer's argus task to that role MUST be created under the chosen orchestrator

#### Scenario: The default role name is de-collided

- **WHEN** the freelancer's name matches an existing active role name under the chosen orchestrator
- **THEN** the adopted role MUST be created under a de-collided name (a numeric suffix appended) rather than failing or colliding

#### Scenario: An already-bound task is not adopted again under the same orchestrator

- **WHEN** the freelancer's argus task already has a live binding under the chosen orchestrator
- **THEN** the adopt MUST be rejected with visible feedback and MUST NOT create a second binding under that orchestrator

#### Scenario: `J` on a freelancer with no argus task id surfaces feedback

- **WHEN** the operator presses `J` on a freelance row that carries no live argus task
- **THEN** the view MUST surface visible feedback and MUST NOT create any role or binding

#### Scenario: `J` re-parents a coordinator under the chosen parent

- **WHEN** the operator selects a coordinator (role row or orchestrator header), presses `J`, and picks a different orchestrator (not the detach sentinel)
- **THEN** a `worker` role under the chosen parent bound to the coordinator's coordinator argus task MUST be created, nesting the coordinator's subtree under the parent

#### Scenario: Re-parenting ends all prior parent-links by role id

- **WHEN** a coordinator that is already nested under some parent (with a live link, and a leftover ended link role from a prior move) is re-parented under a new parent
- **THEN** the prior live link binding MUST be ended with reason `reparented` AND every prior parent-link role MUST be deleted, so exactly one clean link to the new parent remains (no de-collided duplicate link roles accumulate)

#### Scenario: Re-parent rejects a self or descendant target (cycle)

- **WHEN** the operator tries to re-parent a coordinator under itself or under one of its own sub-coordinators
- **THEN** the re-parent MUST be rejected with visible feedback and MUST NOT create any role or binding

#### Scenario: `J` detaches a nested coordinator to top-level

- **WHEN** the operator selects a coordinator that is currently nested under a parent (a parent-link role + live binding), presses `J`, and picks the "Detach (make top-level)" sentinel
- **THEN** every parent-link binding of the coordinator's task MUST be ended (reason `detached`) and every distinct parent-link role MUST be deleted, so the coordinator holds no live parent link and is top-level again; the coordinator's own coordinator role and binding MUST be untouched

#### Scenario: Detaching an already-top-level coordinator is an idempotent no-op

- **WHEN** the operator picks "Detach (make top-level)" for a coordinator that is already top-level (no parent-link roles)
- **THEN** the detach MUST be a clean no-op — no error, no role or binding changed — and the coordinator's own coordinator role and binding remain intact

#### Scenario: A coordinator picker always offers detach

- **WHEN** the operator presses `J` on a valid coordinator, even when no OTHER active orchestrator exists to re-parent under
- **THEN** the picker MUST still open with the "Detach (make top-level)" sentinel available (the coordinator path has no "no eligible target" feedback)

#### Scenario: `J` on a non-adoptable row surfaces feedback

- **WHEN** the operator presses `J` while a managed worker role, an empty selection, or an archived row is selected
- **THEN** the view MUST surface visible feedback and MUST NOT create any role or binding

#### Scenario: No eligible target orchestrator surfaces feedback (freelancer)

- **WHEN** the operator presses `J` on a valid freelancer but no eligible (non-archived) target orchestrator exists
- **THEN** the view MUST surface visible feedback that a coordinator must be created first and MUST NOT open the picker or create any role or binding

#### Scenario: `J` in a pane forwards to the PTY

- **WHEN** focus is in a COORD or AGENT pane and the operator types `J`
- **THEN** the `J` rune MUST be forwarded to the bound task's PTY and MUST NOT open the picker
