## MODIFIED Requirements

### Requirement: Cursor navigation and collapse over selectable rows (area 1)

The system SHALL move the cursor only across selectable rows (orchestrator headers, roles, freelance roles, the archive expando, and the freelance fold header), skipping rules and plain section labels — including kanban-group headers (Active/Backlog/Blocked/Done), which remain non-selectable. After any rebuild the cursor SHALL be re-pinned to the same logical row (by role id, or negated orchestrator id) when it still exists, and clamped onto a selectable row otherwise. Collapse state (per-orchestrator, freelance, archive) SHALL survive rebuilds; kanban-group fold is a separate, DERIVED axis (see "Kanban groups auto-fold to the focused group") that is never independently persisted. Stepping past the boundary of the currently-focused kanban group into an adjacent, currently-collapsed one is a special case of "skipping a non-selectable row": rather than continuing to scan past it, the system re-focuses that group (expanding it and collapsing the one just left) and lands the cursor on its first (moving down) or last (moving up) member row in the same step.

Derived from: `internal/tui/hera/rail.go:43` (`selectable`), `internal/tui/hera/rail.go:119` (`currentRef`), `internal/tui/hera/rail.go:134` (`restoreCursor`), `internal/tui/hera/rail.go:230` (`step`), `internal/tui/hera/rail.go:257` (`clampCursor`), `internal/tui/hera/rail.go:287` (`ToggleCollapse`).

#### Scenario: Down/up skip non-selectable rows

- **WHEN** the cursor steps past a rule or plain header
- **THEN** it lands on the next selectable row in that direction, or stays put if none exists

#### Scenario: Cursor survives a rebuild

- **WHEN** the model is replaced and the rail rebuilds
- **THEN** the cursor re-pins to the previously selected role/orchestrator if it still exists, else clamps onto a selectable row

#### Scenario: Collapse toggles the row under the cursor

- **WHEN** the user presses Space on an orchestrator header, the freelance fold header, or the archive expando
- **THEN** that section's collapsed flag flips, the rows rebuild, and the cursor is restored

#### Scenario: Stepping down across a kanban-group boundary expands the next group

- **WHEN** the cursor is on the last member row of the currently-focused kanban group and the operator moves down
- **THEN** the next non-empty kanban group expands, the group just left collapses to its header, and the cursor lands on the newly-expanded group's first member row

#### Scenario: Stepping up across a kanban-group boundary expands the previous group

- **WHEN** the cursor is on the first member row of the currently-focused kanban group and the operator moves up
- **THEN** the previous non-empty kanban group expands, the group just left collapses to its header, and the cursor lands on the newly-expanded group's last member row

### Requirement: Rail sections — Pinned, Active, Freelance, Archive (area 2)

The system SHALL render the rail in a fixed section order: a "Pinned" section (rendered when pinned orchestrators OR pinned non-root roles exist), the four kanban sub-groups (Active, Backlog, Blocked, Done — see below), a "Freelance (N)" fold section (only when freelance roles exist), and an "Archive (N)" expando section at the bottom (only when archived orchestrators exist). Within the Pinned section, pinned orchestrators (with their subtrees) render first, followed by pinned non-root roles (each as a two-line breadcrumb entry — see the pinned-non-root-role requirement). Each non-empty kanban sub-group renders its OWN leading horizontal-rule divider (the same `─` `StyleBorder` rule drawn above the Freelance and Archive sections), independent of what rendered above it — there is no longer a distinct Pinned→Active divider special case; an empty kanban sub-group renders neither its header nor a divider. The Freelance section defaults expanded; the Archive section defaults collapsed. When the model is entirely empty the rail renders a single "No hera orchestrators" placeholder row.

The active list (everything not pinned and not archived) SHALL be partitioned into ordered kanban-status sub-groups, applying ONLY to TOP-LEVEL (root — no canonical parent) orchestrators: `active`, `backlog`, `blocked`, `done`, in that order. Each sub-group renders a plain, non-selectable `"Active (N)"` / `"Backlog (N)"` / `"Blocked (N)"` / `"Done (N)"` section label, unconditionally preceded by the same horizontal-rule divider whenever that group has at least one matching orchestrator (the same unconditioned-divider convention the Freelance and Archive sections already use) — `active` is no longer headerless; it renders uniformly with the other three. Exactly ONE sub-group is expanded (its member orchestrators, with their subtrees, rendered under its header) at any time — the one containing the current rail selection; the other three render their header line only, with their members hidden (see "Kanban groups auto-fold to the focused group"). A kanban sub-group with zero matching top-level orchestrators renders NEITHER its header NOR its divider, regardless of fold state. A nested/bridged (non-root) orchestrator's own kanban status is never consulted for placement — it continues to render nested under its canonical parent exactly as before, regardless of section boundaries.

Derived from: `internal/tui/hera/rail.go` (`buildRows`), `internal/tui/hera/rail.go` (`NewRail` archive default), `internal/tui/hera/model.go` (Model sections, `OrchView.KanbanStatus`), `internal/tui/hera/model.go` (`canonicalParents`).

#### Scenario: Pinned section appears when an orchestrator is pinned

- **WHEN** at least one orchestrator is pinned
- **THEN** a "Pinned" header renders above the active list with the pinned orchestrators (and their subtrees) under it

#### Scenario: Pinned section appears when only a non-root role is pinned

- **WHEN** no orchestrator is pinned but at least one non-root role is pinned
- **THEN** a "Pinned" header still renders, with the pinned role(s) shown as breadcrumb entries under it

#### Scenario: Each non-empty kanban sub-group renders its own leading divider

- **WHEN** a kanban sub-group (Active, Backlog, Blocked, or Done) has at least one top-level orchestrator
- **THEN** its header renders preceded by its own horizontal-rule divider, regardless of whether a Pinned section rendered above it

#### Scenario: Kanban sub-groups render a labeled, divided section header

- **WHEN** at least one top-level orchestrator carries kanban status `active` (or `backlog`, `blocked`, or `done`)
- **THEN** its `"Active (N)"` (or `"Backlog (N)"` / `"Blocked (N)"` / `"Done (N)"`) section label renders, in rail order `active → backlog → blocked → done`, with that group's member orchestrators (and their subtrees) listed under it ONLY when that group currently holds the rail selection — otherwise the header renders alone

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

## ADDED Requirements

### Requirement: Kanban groups auto-fold to the focused group (area 2)

The system SHALL keep exactly one of the four kanban sub-groups (Active/Backlog/Blocked/Done) expanded at a time: the one containing the role/orchestrator currently under the rail cursor. Every code path that moves the selection to a target outside the currently-focused group — the `m`/`M` kanban status-cycle keys, `SelectByTaskID` (the plan view's leaf-jump), and `EnsureAncestorsExpanded` — SHALL re-focus the target's kanban group (resolved via the target's top-level/root orchestrator) before the rows rebuild, so the target row exists to select. Kanban fold state is NOT persisted across restarts, unlike per-orchestrator/Freelance/Archive fold — it is derived fresh from wherever the restored selection lands on each build. When no prior selection resolves (e.g. the very first non-empty build), the default focused group is `active`.

Derived from: `internal/tui/hera/rail.go` (`buildRows`, `step`, `SetModel`, `SelectByTaskID`, `EnsureAncestorsExpanded`), `internal/tui/hera/model.go` (`OrchView.KanbanStatus`, `canonicalParents`).

#### Scenario: Only the focused group's members render

- **WHEN** the rail selection is inside the Active group
- **THEN** Active's member orchestrators render under its header, while Backlog/Blocked/Done each render their header line only, with their members hidden

#### Scenario: Changing a selected coordinator's kanban status keeps it selected

- **WHEN** the operator presses `m` (or `M`) on the selected top-level coordinator, moving it into a different kanban group
- **THEN** that group becomes focused, that coordinator's row remains the selection, and the group it left renders header-only

#### Scenario: A plan-view jump re-focuses the target's kanban group

- **WHEN** `SelectByTaskID` targets a role whose top-level orchestrator belongs to a currently non-focused kanban group
- **THEN** that group becomes focused (expanding it) before the row is located, so the jump succeeds

#### Scenario: Default focus with no prior selection

- **WHEN** the rail builds its rows for the first time with no persisted or resolvable selection
- **THEN** the `active` group is the focused (expanded) group

#### Scenario: Kanban fold is not persisted

- **WHEN** the daemon restarts and the rail state store is reloaded
- **THEN** the focused kanban group is recomputed from the restored selection ref, not read from any persisted kanban-fold field
