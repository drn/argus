## MODIFIED Requirements

### Requirement: Rail sections — Pinned, Active, Freelance, Archive (area 2)

The system SHALL render the rail in a fixed section order: a "Pinned" section (rendered when pinned orchestrators OR pinned non-root roles exist), the active orchestrators, a "Freelance (N)" fold section (only when freelance roles exist), and an "Archive (N)" expando section at the bottom (only when archived orchestrators exist). Within the Pinned section, pinned orchestrators (with their subtrees) render first, followed by pinned non-root roles (each as a two-line breadcrumb entry — see the pinned-non-root-role requirement). A non-selectable horizontal-rule divider (the same `─` `StyleBorder` rule drawn above the Freelance and Archive sections) SHALL separate the Pinned section from the active list, rendered ONLY when the Pinned section is present AND at least one active entry follows it (no stray rule when nothing is pinned, and none when the Pinned section is the only content). The Freelance section defaults expanded; the Archive section defaults collapsed. When the model is entirely empty the rail renders a single "No hera orchestrators" placeholder row.

The active list (everything not pinned and not archived) SHALL be partitioned into ordered kanban-status sub-groups, applying ONLY to TOP-LEVEL (root — no canonical parent) orchestrators: `active` (rendered exactly as the historical headerless active list, preserving the Pinned-divider rule above unchanged), then `backlog`, `blocked`, `done` — each rendered as a plain, non-selectable, non-collapsible `"Backlog (N)"` / `"Blocked (N)"` / `"Done (N)"` section label, unconditionally preceded by the same horizontal-rule divider whenever that group has at least one matching orchestrator (the same unconditioned-divider convention the Freelance and Archive sections already use). A kanban sub-group with zero matching top-level orchestrators renders NEITHER its header NOR its divider. A nested/bridged (non-root) orchestrator's own kanban status is never consulted for placement — it continues to render nested under its canonical parent exactly as before, regardless of section boundaries.

Derived from: `internal/tui/hera/rail.go` (`buildRows`), `internal/tui/hera/rail.go` (`NewRail` archive default), `internal/tui/hera/model.go` (Model sections, `OrchView.KanbanStatus`), `internal/tui/hera/model.go` (`canonicalParents`).

#### Scenario: Pinned section appears when an orchestrator is pinned

- **WHEN** at least one orchestrator is pinned
- **THEN** a "Pinned" header renders above the active list with the pinned orchestrators (and their subtrees) under it

#### Scenario: Pinned section appears when only a non-root role is pinned

- **WHEN** no orchestrator is pinned but at least one non-root role is pinned
- **THEN** a "Pinned" header still renders, with the pinned role(s) shown as breadcrumb entries under it

#### Scenario: Divider separates the Pinned section from the active list

- **WHEN** the rail has a Pinned section AND at least one top-level `active`-status orchestrator below it
- **THEN** a single non-selectable horizontal-rule divider renders between the last Pinned row and the first active row, and the cursor skips it during `j`/`k` navigation

#### Scenario: No Pinned divider when nothing is pinned or no active-status entry follows

- **WHEN** there is no Pinned section, OR the Pinned section is present but no top-level `active`-status orchestrator follows it
- **THEN** no Pinned→active divider renders (a non-empty Backlog/Blocked/Done group below still renders its OWN unconditioned divider, per the scenario below)

#### Scenario: Non-active kanban groups render a labeled, divided section

- **WHEN** at least one top-level orchestrator carries kanban status `backlog` (or `blocked`, or `done`)
- **THEN** a `"Backlog (N)"` (or `"Blocked (N)"`, or `"Done (N)"`) section label renders, preceded by a horizontal-rule divider, with those orchestrators (and their subtrees) listed under it, in rail order `active → backlog → blocked → done`

#### Scenario: An empty kanban group renders nothing

- **WHEN** no top-level orchestrator carries kanban status `blocked`
- **THEN** no "Blocked" header and no divider for it render at all

#### Scenario: A nested coordinator's kanban status never affects its placement

- **WHEN** a nested/bridged (non-root) orchestrator carries kanban status `done`
- **THEN** it still renders nested under its canonical parent's subtree, not hoisted into a top-level "Done" group

#### Scenario: Archive section collapsed by default

- **WHEN** archived orchestrators exist
- **THEN** an "Archive (N)" expando renders at the bottom, collapsed by default, expanding only when toggled

#### Scenario: Empty model shows a placeholder

- **WHEN** there are no orchestrators or freelance roles at all
- **THEN** the rail renders a single non-selectable "No hera orchestrators" row

### Requirement: Rail keybindings (area 4)

The system SHALL bind the following keys while the rail holds focus: `j`/`k` and Down/Up move the cursor; Left walks to the parent; Space collapses/expands the row under the cursor; `Tab`/`Backtab` and `Ctrl+Alt+Left`/`Ctrl+Alt+Right` walk the focus ladder; `Ctrl+Q` returns focus to the rail; `Enter` enters the selected role's pane (restarting a dead/suspended session first); `w` spawns a worker under the selected coordinator's orchestrator via the full new-task modal; `n` creates a new top-level coordinator via the full new-task modal; `r` renames the selected role/orchestrator; `a` HIDES the selected worker / sub-coordinator into its parent coordinator's nested archive (Tier 1 — reversible, keeps the session + worktree alive); `P` toggles pin; `s`/`S` advance/revert the selected role's hera status; `m`/`M` advance/revert the selected TOP-LEVEL coordinator's kanban status (see the dedicated kanban requirement — a wholly separate axis from `s`/`S`); `J` adopts a freelancer / re-parents a coordinator; `C` clears the selected coordinator's archive (NUKES every Tier-1 hidden item under it); `Ctrl+Z` fullscreens the focused pane; `/` filters the rail by name; `Ctrl+D` NUKES the selected role/orchestrator (Tier 2 — removes it and its whole subtree from the rail, reclaims worktrees). Every bound key SHALL appear in the help overlay's "Hera View (rail)" section.

The rail SHALL NOT bind `R` (retire) or a rail-wide `Ctrl+R` (prune) — both are removed by this redesign. All rail mutation keys SHALL be suppressed while the rail is in `/` filter INPUT mode (every keystroke is filter input). Selection-acting keys (`w`, `r`, `a`, `P`, `s`/`S`, `m`/`M`, `J`, `C`, `Ctrl+D`) are no-ops on an empty selection; `n` is selection-INDEPENDENT and fires even on an empty rail. `m`/`M` additionally no-op on any non-empty selection that is not a top-level coordinator header (a role row, a nested/bridged sub-coordinator row, or a Freelance row) — see the dedicated kanban requirement.

Derived from: `internal/tui/hera/rail.go` (rail `InputHandler`), `internal/tui/hera/page.go` (page `InputHandler` focus ladder + `handleRailMutation`), `internal/tui/heraactions.go` (handlers), `internal/tui/modal/help.go` (help overlay Hera section), `internal/tui/keymap/actions.go` (`ActHeraKanbanAdv`/`ActHeraKanbanRev`).

`NOTE:` `Ctrl+D` is the only key that NUKES a live selection directly (`C` nukes only the selected coordinator's already-hidden Tier-1 archive items); the rail binds no `R` (retire) or rail-wide `Ctrl+R` (prune). `Ctrl+D` never collides with the agent-view `Ctrl+R` (Claude session switcher), which runs in a different mode/widget. A focused content pane forwards `C`/`a`/`Ctrl+D` to its PTY. `m`/`M` never collides with `s`/`S`: the two step entirely independent data (an orchestrator's `kanban_status` column vs. a role's `hera_role_status` row) and use independent stepping rules (`m`/`M` wraps; `s`/`S` clamps).

#### Scenario: Hide key acts on the current selection

- **WHEN** the rail is focused and the user presses `a` on a selected worker
- **THEN** the hide callback fires for that worker's `(role, orchestrator)` selection (no confirmation) and the key does not leak to navigation

#### Scenario: Retire and rail-wide prune keys are unbound

- **WHEN** the user presses `R` or `Ctrl+R` while the rail is focused
- **THEN** nothing end-of-life happens (`R` is unbound; `Ctrl+R` is not a rail-wide prune) — the redesign removed both

#### Scenario: Help overlay lists every rail key

- **WHEN** the help overlay is opened
- **THEN** its "Hera View (rail)" section lists `j`/`k`, space, Left, Tab/Ctrl+Q, Enter, `w`, `n`, `r`, `a` (hide), `P`, `s`/`S`, `m`/`M`, `J`, `C` (clear archive), `Ctrl+Z`, `/`, and `Ctrl+D` (nuke), and does NOT list `R` or a rail-wide `Ctrl+R`

## ADDED Requirements

### Requirement: Top-level coordinators carry an independent kanban status (area 2)

The system SHALL persist a `kanban_status` value (`active` | `backlog` | `blocked` | `done`, default `active`) on every `HeraOrchestrator` row, independent of `pinned_at`, `archived_at`, and any role's `hera_role_status`. The value has no effect on task lifecycle, session behavior, or the `s`/`S` role-status ladder — it exists solely to drive the rail's kanban grouping (see "Rail sections") and the `m`/`M` stepping keys (see below). An orchestrator with no explicit value (including every orchestrator that existed before this axis was introduced) SHALL read as `active`.

Derived from: `internal/db/hera.go` (`HeraOrchestrator.KanbanStatus`, `HeraKanbanStatus`), `internal/db/schema.go` (`hera_orchestrators.kanban_status` column).

#### Scenario: New orchestrator defaults to active

- **WHEN** a new orchestrator is created with no explicit kanban status
- **THEN** its `kanban_status` reads as `active`

#### Scenario: A pre-existing orchestrator defaults to active after the migration

- **WHEN** an orchestrator created before this axis existed is read after the schema migration
- **THEN** its `kanban_status` reads as `active`, not `backlog` or any other value

#### Scenario: Kanban status is independent of pin and archive

- **WHEN** a top-level coordinator is pinned, or archived, and its kanban status is `blocked`
- **THEN** pinning/archiving does not change `kanban_status`, and changing `kanban_status` does not change `pinned_at`/`archived_at`

#### Scenario: Kanban status is independent of role status

- **WHEN** a top-level coordinator's own role carries `hera_role_status` `done` and its orchestrator's `kanban_status` is `backlog`
- **THEN** the two values are read and set completely independently — advancing one via `s`/`S` or `m`/`M` never changes the other

### Requirement: `m`/`M` cycle a top-level coordinator's kanban status

The system SHALL bind `m` (advance) and `M` (revert) in `CtxHeraRail` to step the SELECTED top-level coordinator's `kanban_status` forward or backward through the cyclic sequence `active → backlog → blocked → done`, WRAPPING at both ends (`m` past `done` wraps to `active`; `M` before `active` wraps to `done`) — deliberately unlike the `s`/`S` role-status ladder, which clamps.

The mutation SHALL fire only when the rail cursor rests on a TOP-LEVEL coordinator's orchestrator header row — an orchestrator with no canonical parent (per `Model.canonicalParents()`). It SHALL be a no-op, with no error and no side effect, when the cursor rests on: a role row (worker, freelance, or a bridging sub-coordinator row that visually resembles a coordinator but is structurally a worker role), a nested/bridged orchestrator header reached only through a canonical parent, a section header/divider, or when the rail selection is empty.

Both keys SHALL be no-ops while the rail is in `/` filter input mode, matching every other rail mutation key.

Derived from: `internal/tui/hera/ops.go` (`Ops.KanbanStep`), `internal/tui/hera/model.go` (`Selection.TopLevelOrch`), `internal/tui/hera/page.go` (`handleRailMutation`), `internal/tui/keymap/actions.go` (`ActHeraKanbanAdv`/`ActHeraKanbanRev`).

#### Scenario: m advances through the cycle, wrapping

- **WHEN** `m` is pressed repeatedly on a top-level coordinator starting at `active`
- **THEN** its kanban status steps `active → backlog → blocked → done → active`, wrapping after `done`

#### Scenario: M reverts through the cycle, wrapping

- **WHEN** `M` is pressed repeatedly on a top-level coordinator starting at `active`
- **THEN** its kanban status steps `active → done → blocked → backlog → active`, wrapping after `backlog`

#### Scenario: No-op on a role selection

- **WHEN** the rail cursor rests on a worker, freelance, or bridging sub-coordinator role row and `m` or `M` is pressed
- **THEN** nothing changes — the key does not act on the role's orchestrator

#### Scenario: No-op on a nested orchestrator header

- **WHEN** the rail cursor rests on a nested/bridged (non-root) orchestrator's header row and `m` or `M` is pressed
- **THEN** nothing changes

#### Scenario: No-op on an empty rail

- **WHEN** the rail has no selectable rows and `m` or `M` is pressed
- **THEN** nothing changes and no error occurs

#### Scenario: Fires on a pinned or archived top-level coordinator

- **WHEN** the rail cursor rests on a pinned (or archived) top-level coordinator's header row and `m` is pressed
- **THEN** its kanban status advances normally, and its pinned/archived placement is unaffected
