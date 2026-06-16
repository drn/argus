# hera-view delta: restore the `J` adopt/reparent key

## MODIFIED Requirements

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Enter` enters the selected role's pane (restarting a dead session first); `w` spawns a worker under the selected coordinator's orchestrator; `r` renames the selected role/orchestrator; `a` toggles archive; `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `Ctrl+D` deletes the selected role/orchestrator; `J` adopts a freelancer into, or re-parents a coordinator under, a chosen orchestrator (see the dedicated requirement). Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

Derived from: `internal/tui/hera/rail.go:548` (rail `InputHandler`), `internal/tui/hera/page.go:288` (page `InputHandler` focus ladder), `internal/tui/hera/page.go:371` (`handleRailMutation`), `internal/tui/modal/help.go:70` (help overlay Hera section).

`NOTE:` Native still OMITS several plugin rail keys: `n` (new orchestrator — canonical path is the `hera_new_orchestrator` MCP tool), `/` (rail name filter), `Ctrl+R` (hera-prune — Tasks-tab-only in native), `l` (toggle archived visibility), and `Ctrl+Z` (fullscreen pane). `J` (adopt/reparent) is now bound (this change). Plain Left/Right are unused by the rail (free for future horizontal nav). Per `docs/NATIVE-HERA-FOLLOWUPS.md`, the remaining omissions are known parity gaps; `Cmd+↑/↓` rail-selection-while-pane-focused collides at the byte level with agent-view task navigation and remains an unresolved rebinding decision.

#### Scenario: Mutation key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected role
- **THEN** the archive-toggle callback fires for that role's `(role, orchestrator)` selection and the key does not leak to navigation

#### Scenario: Omitted plugin key is inert

- **WHEN** the user presses `/`, `n`, or `l` while the rail is focused
- **THEN** nothing happens (no filter, no new-orchestrator, no archive-visibility toggle) because native binds none of them

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Tab/Ctrl+Q, Enter, `w`, `r`, `a`, `P`, `s`/`S`, `Ctrl+D`, and `J`

### Requirement: Freelance roles hoisted into a top-level section (area 8)

The system SHALL hoist active freelance-kind roles into a top-level "Freelance" rail section rather than nesting them under their orchestrator. `BuildModel` skips active freelance-kind roles when filling an orchestrator's roles and appends them to `Model.Freelance`, sorted by name. The native view SHALL provide a manual adopt affordance on the `J` key (see the dedicated requirement): a freelance row is adopted as a worker under a chosen orchestrator, and a coordinator selection is re-parented under a chosen orchestrator.

Derived from: `internal/tui/hera/model.go:189` (freelance hoist), `internal/tui/hera/model.go:206` (sort), `internal/tui/hera/rail.go:174` (Freelance section), `internal/tui/hera/adopt.go` (adopt/reparent ops).

`NOTE:` Native's Freelance source is still narrower than the plugin's. The plugin derived freelancers from UNMANAGED live argus tasks grouped by repo (zero hera bindings), so its adopt guard rejected ANY live binding. Native's Freelance section reflects only roles explicitly created with kind `freelance` (via `hera_join`), which already carry their own live binding under the orchestrator they joined. Native's adopt therefore rejects only a DUPLICATE binding under the SAME chosen orchestrator (the per-`(task, orchestrator)` unique index), not any live binding, and remains faithful to the multi-binding model. The unmanaged-task freelance source itself is out of scope for the read-only hera store (a separate follow-up moves freelancers to the Tasks tab).

#### Scenario: Freelance role appears in its own section

- **WHEN** a role of kind `freelance` is active under some orchestrator
- **THEN** it renders in the top-level "Freelance (N)" section, not nested under that orchestrator

#### Scenario: Adopt affordance exists on the rail

- **WHEN** the operator selects a freelancer (or a coordinator) and presses `J`
- **THEN** the system opens an orchestrator picker and, on selection, creates the worker role + binding (adopt) or re-parents the coordinator — adoption IS a native rail operation

## ADDED Requirements

### Requirement: `J` adopts a freelancer or re-parents a coordinator

While the RAIL is focused, pressing `J` SHALL act on the current selection:

- A FREELANCER selection (a `freelance`-kind role row carrying a live argus task) SHALL open a target picker listing the active (non-archived) orchestrators.
- A COORDINATOR selection (a `coordinator`-kind role row, OR an orchestrator header whose orchestrator has a coordinator role and is not archived) SHALL open a target picker listing the OTHER active orchestrators (excluding the coordinator's own orchestrator).
- Any other selection (a managed worker role, an empty selection, an archived row) SHALL surface visible feedback that only a freelancer or coordinator can be adopted, and SHALL NOT create or change any role or binding (never a silent no-op).

The picker SHALL be a themed, focusable, dismissable modal in which typed characters narrow the list by case-insensitive substring on the orchestrator name, `Enter` selects the highlighted orchestrator, and `Esc` cancels without change. The picker SHALL name the row being adopted in its title. When no eligible target orchestrator exists, pressing `J` SHALL surface visible feedback that a coordinator must be created first and SHALL NOT open the picker or create any role or binding.

`J` SHALL be RAIL-focus-only. In a COORD or AGENT pane the `J` rune SHALL forward to the bound task's PTY like any other character; the lowercase `j` navigation key SHALL be unaffected. The adopt/reparent role+binding writes are cheap local SQLite mutations and run synchronously on the tview event loop, consistent with the other rail mutations (rename/archive/pin/status/delete); they do NOT touch a worktree or session, so they never perceptibly block the loop. (This differs from worker SPAWN, which creates a worktree + PTY session and is therefore dispatched off-thread.)

#### Adopt (freelancer → worker)

Selecting an orchestrator for a freelancer SHALL adopt the freelancer's argus task into it by creating, server-side and without any agent action, through the SAME transactional DAO `hera_join`'s attach-mode and the born-bound spawn use (`CreateHeraRoleWithBinding`, not a duplicate implementation), so a binding-insert failure (e.g. a worktree-orchestrator uniqueness collision) rolls the freshly-created worker role back — no orphan role:

- a `worker` role under the chosen orchestrator whose name defaults to the freelancer's name and is de-collided (a numeric suffix appended) when an active role of that name already exists; the role SHALL record the freelancer's argus repo as its `argus_project`; and
- a live binding from the freelancer's argus task to that role, recording the freelancer's argus-task worktree path.

The freelancer's argus task SHALL be best-effort stamped `meta:hera.role=worker` for parity with `hera_join`; a transient failure to stamp SHALL NOT undo or fail the binding. The adopt SHALL be REJECTED with visible feedback, creating no role or binding, when: the freelance row has no argus task id; or the task already holds a live binding under the chosen orchestrator (a duplicate).

#### Re-parent (coordinator → sub-coordinator)

Selecting a parent orchestrator for a coordinator SHALL re-parent it by creating a `worker` role under the chosen parent bound to the coordinator's coordinator argus task — the multi-binding the orchestration tree renders as a nested sub-coordinator. The coordinator's whole subtree moves with it (the subtree derives from the coordinator, which is untouched). The coordinator argus task + worktree SHALL be resolved from the coordinator role's LATEST binding (live, else most-recent ended) so a dormant coordinator can still be re-parented.

The re-parent SHALL be REJECTED with visible feedback when the chosen parent IS the coordinator's own orchestrator, or is a descendant of it (a cycle), or the coordinator has no coordinator role / no binding to re-parent.

**Teardown invariant (BUG-026):** before creating the new link, the re-parent SHALL end EVERY prior parent-link of the coordinator's task by ROLE id — both LIVE and ENDED. Live parent-link bindings SHALL be ended with reason `reparented`; then every distinct parent-link role (any role other than the coordinator's own coordinator role, reached through any binding of that task) SHALL be deleted so its bindings cascade away. This guarantees that repeated re-parents never pile up de-collided duplicate link roles (`name`, `name-2`, `name-3`, …); exactly one clean link remains.

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

- **WHEN** the operator selects a coordinator (role row or orchestrator header), presses `J`, and picks a different orchestrator
- **THEN** a `worker` role under the chosen parent bound to the coordinator's coordinator argus task MUST be created, nesting the coordinator's subtree under the parent

#### Scenario: Re-parenting ends all prior parent-links by role id

- **WHEN** a coordinator that is already nested under some parent (with a live link, and a leftover ended link role from a prior move) is re-parented under a new parent
- **THEN** the prior live link binding MUST be ended with reason `reparented` AND every prior parent-link role MUST be deleted, so exactly one clean link to the new parent remains (no de-collided duplicate link roles accumulate)

#### Scenario: Re-parent rejects a self or descendant target (cycle)

- **WHEN** the operator tries to re-parent a coordinator under itself or under one of its own sub-coordinators
- **THEN** the re-parent MUST be rejected with visible feedback and MUST NOT create any role or binding

#### Scenario: `J` on a non-adoptable row surfaces feedback

- **WHEN** the operator presses `J` while a managed worker role, an empty selection, or an archived row is selected
- **THEN** the view MUST surface visible feedback and MUST NOT create any role or binding

#### Scenario: No eligible target orchestrator surfaces feedback

- **WHEN** the operator presses `J` on a valid freelancer or coordinator but no eligible (non-archived, non-self) target orchestrator exists
- **THEN** the view MUST surface visible feedback that a coordinator must be created first and MUST NOT open the picker or create any role or binding

#### Scenario: `J` in a pane forwards to the PTY

- **WHEN** focus is in a COORD or AGENT pane and the operator types `J`
- **THEN** the `J` rune MUST be forwarded to the bound task's PTY and MUST NOT open the picker
