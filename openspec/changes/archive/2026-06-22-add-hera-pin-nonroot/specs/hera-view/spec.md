# Hera View

## MODIFIED Requirements

### Requirement: Rail sections — Pinned, Active, Freelance, Archive (area 2)

The system SHALL render the rail in a fixed section order: a "Pinned" section (rendered when pinned orchestrators OR pinned non-root roles exist), the active orchestrators (no header, like the task list's active section), a "Freelance (N)" fold section (only when freelance roles exist), and an "Archive (N)" expando section at the bottom (only when archived orchestrators exist). Within the Pinned section, pinned orchestrators (with their subtrees) render first, followed by pinned non-root roles (each as a two-line breadcrumb entry — see the pinned-non-root-role requirement). The Freelance section defaults expanded; the Archive section defaults collapsed. When the model is entirely empty the rail renders a single "No hera orchestrators" placeholder row.

Derived from: `internal/tui/hera/rail.go` (`buildRows`), `internal/tui/hera/rail.go` (`NewRail` archive default), `internal/tui/hera/model.go` (Model sections).

#### Scenario: Pinned section appears when an orchestrator is pinned

- **WHEN** at least one orchestrator is pinned
- **THEN** a "Pinned" header renders above the active list with the pinned orchestrators (and their subtrees) under it

#### Scenario: Pinned section appears when only a non-root role is pinned

- **WHEN** no orchestrator is pinned but at least one non-root role is pinned
- **THEN** a "Pinned" header still renders, with the pinned role(s) shown as breadcrumb entries under it

#### Scenario: Archive section collapsed by default

- **WHEN** archived orchestrators exist
- **THEN** an "Archive (N)" expando renders at the bottom, collapsed by default, expanding only when toggled

#### Scenario: Empty model shows a placeholder

- **WHEN** there are no orchestrators or freelance roles at all
- **THEN** the rail renders a single non-selectable "No hera orchestrators" row

## ADDED Requirements

### Requirement: Per-role pin state is projected into the read model (area 2)

The system SHALL project each hera role's pin state into the read model so the rail can render a pinned non-root role. `buildRoleView` SHALL set `RoleView.Pinned` true when the role's `hera_roles.pinned_at` is set and false otherwise. The existing `P` mutation path (`Ops.PinToggle` → `db.PinHeraRole` / `UnpinHeraRole`) is unchanged — it already stamps/clears `pinned_at` for a selected role; this requirement only adds the missing read projection that made the pin a silent no-op.

Derived from: `internal/tui/hera/model.go` (`buildRoleView`), `internal/db/hera.go` (`PinHeraRole`/`UnpinHeraRole`), `internal/tui/hera/ops.go` (`PinToggle`).

#### Scenario: Pinned role projects a true flag

- **WHEN** a role's `hera_roles.pinned_at` is set
- **THEN** its `RoleView.Pinned` is true

#### Scenario: Unpinned role projects a false flag

- **WHEN** a role's `hera_roles.pinned_at` is NULL
- **THEN** its `RoleView.Pinned` is false

### Requirement: Pinned non-root roles render as a two-line breadcrumb entry (area 2/3)

The system SHALL float a pinned non-root role OUT of its parent subtree and render it in the Pinned section as a two-line entry: line 1 is a SELECTABLE breadcrumb row showing the role's status glyph (dimmed) followed by its lineage trail, and line 2 is a NON-SELECTABLE continuation row showing the role's name. The lineage trail SHALL be the orchestrator-name chain from the root down to and including the role's own orchestrator (`role.OrchID`), derived from the rail's `canonicalParents` nesting so the trail matches how the rail nests the subtree, joined with `" › "`. An over-wide trail SHALL be left-truncated with a leading `…` so the nearest parent stays visible (rune-aware). A pinned role whose own orchestrator is itself pinned SHALL NOT float (it stays nested under the pinned orchestrator). A pinned role's orchestrator that cannot be resolved in the model SHALL NOT be floated (skipped and logged) rather than rendered without lineage.

Derived from: `internal/tui/hera/rail.go` (`collectPinnedRoles`, `appendOrchWorkers` float-skip, `drawPinnedBreadcrumbRow`, the breadcrumb continuation draw), `internal/tui/hera/model.go` (`canonicalParents`, `OrchByID`), `anutron/hera` `internal/view/rail_list.go` (BUG-025 prior art).

#### Scenario: Pinned leaf role floats with a breadcrumb

- **WHEN** a worker role under an unpinned orchestrator is pinned
- **THEN** it renders in the Pinned section as a selectable breadcrumb line (dimmed glyph + lineage trail) immediately followed by a non-selectable name line, and does NOT also render nested under its orchestrator in the active tree

#### Scenario: Over-wide lineage trail is left-truncated

- **WHEN** a pinned role's lineage trail is wider than the rail
- **THEN** the trail is left-truncated with a leading `…`, keeping the nearest parent (rightmost text) visible

#### Scenario: Lineage trail spans the full canonical chain

- **WHEN** a pinned role's orchestrator is a sub-coordinator nested under a root
- **THEN** the breadcrumb trail shows the full chain `root › sub ›` derived from `canonicalParents`

#### Scenario: Role under a pinned orchestrator stays nested

- **WHEN** a role is pinned and its own orchestrator is also pinned
- **THEN** the role renders nested under the pinned orchestrator (it does NOT float to a standalone breadcrumb entry)

#### Scenario: Unresolvable parent is not floated

- **WHEN** a pinned role's orchestrator cannot be resolved in the model
- **THEN** the role is skipped from the Pinned block (logged), never rendered without lineage

### Requirement: A pinned sub-coordinator hoists its whole subtree (area 1/2)

The system SHALL, when the pinned non-root role is a bridging sub-coordinator (a worker row carrying a child orchestrator), hoist the role AND its whole nested subtree into the Pinned section: the breadcrumb + name entry renders, and the child orchestrator's subtree renders beneath it. The hoisted child orchestrator SHALL be marked placed so it renders exactly once — neither double-rendered in the active tree nor leaked to a top-level root by the safety sweep — in both collapsed and expanded fold states. The pinned sub-coordinator's breadcrumb row SHALL carry the child orchestrator id so Space folds the hoisted subtree and `Ctrl+D` cascades the whole nested sub-team (the same conservative cascade as when nested).

Derived from: `internal/tui/hera/rail.go` (`collectPinnedRoles`, `appendOrch`/`appendOrchWorkers` hoist + `placed` marking, `structuralReach`), `internal/tui/hera/model.go` (`Selection.BridgeChildOrchID`), `anutron/hera` `internal/view/rail_list.go` (BUG-021 prior art).

#### Scenario: Pinned sub-coordinator brings its subtree

- **WHEN** a bridging sub-coordinator role is pinned
- **THEN** it floats to the Pinned section with its child orchestrator's subtree rendered beneath it

#### Scenario: Hoisted subtree renders exactly once

- **WHEN** a pinned sub-coordinator's subtree is hoisted into the Pinned section
- **THEN** the child orchestrator does not also render in the active tree, in both collapsed and expanded fold states

#### Scenario: Folding and cascading a hoisted sub-coordinator

- **WHEN** the cursor is on a pinned sub-coordinator's breadcrumb line
- **THEN** Space folds its hoisted subtree and `Ctrl+D` cascades the whole nested sub-team

### Requirement: Cursor anchors on the breadcrumb line of a pinned entry (area 1)

The system SHALL anchor the rail cursor on the SELECTABLE breadcrumb line (line 1) of a two-line pinned entry; the continuation name line (line 2) is non-selectable and is skipped by cursor navigation. The cursor identity for a breadcrumb line SHALL be the role id, so after a rebuild the cursor re-pins onto the same pinned role's breadcrumb line via the existing selection-ref mechanism, and the continuation line never shadows that identity. A `P` press on a floated pinned role's breadcrumb line SHALL unpin it (returning it to its parent subtree).

Derived from: `internal/tui/hera/rail.go` (`selectable`, `currentRef`, `restoreCursor`, `SelectByTaskID`, `step`), `internal/tui/hera/ops.go` (`PinToggle`).

#### Scenario: Navigation skips the continuation line

- **WHEN** the cursor steps with `j`/`k` past a two-line pinned entry
- **THEN** it lands on the breadcrumb line, never on the non-selectable continuation name line

#### Scenario: Cursor re-pins onto a pinned breadcrumb after a rebuild

- **WHEN** the model is replaced while the cursor is on a pinned role's breadcrumb line
- **THEN** the cursor re-pins to that pinned role's breadcrumb line by role id

#### Scenario: Unpin from the breadcrumb line

- **WHEN** `P` is pressed on a floated pinned role's breadcrumb line
- **THEN** the role is unpinned and returns to its parent subtree on the next rebuild
