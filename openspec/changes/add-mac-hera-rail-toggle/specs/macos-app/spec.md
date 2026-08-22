## REMOVED Requirements

### Requirement: Hera roster (read-only)

**Reason**: Superseded by the sidebar's nested Hera-tree mode and dual-pane coordinator/agent detail view (see the ADDED requirements below), which subsume the flat, all-orchestrators roster this toolbar toggle showed, with nesting and a richer detail view besides.

**Migration**: Use the sidebar's mode toggle to switch into Hera mode instead of the toolbar toggle. Selecting a role in the tree opens the new dual-pane detail view (a coordinator's own pane plus the selected role's pane, or a roster-list details region when the selection is itself a coordinator) in place of the old flat roster.

## ADDED Requirements

### Requirement: Hera tree sidebar mode

The system SHALL provide a toggle that switches the sidebar between the flat task list (grouped by project, as today) and a nested Hera tree, sourced from `GET /api/hera`. Top-level orchestrators SHALL be grouped by `kanban_status`. An orchestrator whose `bridge_parent_orch_id` is set SHALL render nested under that parent's bridging role, not as a separate top-level entry. Fold/expand state for tree nodes SHALL be maintained locally in the app's view state and SHALL NOT be persisted server-side or synced across clients.

#### Scenario: Toggle switches sidebar mode

- **WHEN** the user activates the sidebar's mode toggle
- **THEN** the sidebar's content switches between the flat project-grouped task list and the nested Hera tree, without affecting the currently selected task/role binding unless that task no longer exists in the new mode's data

#### Scenario: Top-level orchestrators group by kanban status

- **WHEN** the Hera tree is displayed
- **THEN** top-level orchestrators (those with a null `bridge_parent_orch_id`) are grouped into sections by their `kanban_status` value

#### Scenario: A nested orchestrator renders under its bridge parent

- **WHEN** an orchestrator's `bridge_parent_orch_id` identifies another orchestrator
- **THEN** it renders nested beneath that parent orchestrator's bridging role, and does not also appear as a separate top-level or kanban-grouped entry

#### Scenario: Fold state survives a refresh poll

- **WHEN** the user collapses a tree node and the sidebar's periodic data refresh completes
- **THEN** that node remains collapsed, without the app persisting the fold state to the daemon

### Requirement: Hera dual-pane detail view

The system SHALL show a dual-pane detail view when the sidebar is in Hera mode and a role is selected: one pane bound to the active orchestrator's own coordinator task, the other bound to the selected role's task — except when the selected role is itself a coordinator, in which case that role's pane SHALL show a read-only roster-list details region (the same role data the flat roster previously rendered) instead of a live terminal. This view SHALL replace the single-task `DetailView` only while the sidebar is in Hera mode; `DetailView`'s existing behavior in the flat task-list mode SHALL be unchanged. This requirement covers read-only navigation only — no Hera mutation (spawning a worker, sending a message, editing a plan node) SHALL be exposed anywhere in this view, matching the existing TUI-only mutation scope.

#### Scenario: Selecting a worker or freelance role shows two live panes

- **WHEN** the user selects a worker or freelance role in the Hera tree
- **THEN** the detail area shows two panes side by side: the active orchestrator's coordinator pane, and the selected role's own live pane

#### Scenario: Selecting a coordinator shows a roster region instead of a terminal

- **WHEN** the user selects a role whose `kind` is `coordinator`
- **THEN** the corresponding pane shows a read-only roster list for that coordinator's orchestrator instead of a live terminal

#### Scenario: Flat task-list mode is unaffected

- **WHEN** the sidebar is in flat task-list mode
- **THEN** selecting a task shows the existing single-task `DetailView` (Terminal/Diff/Files/Info tabs) exactly as before this change

#### Scenario: No mutation controls are present

- **WHEN** the user views either pane of the dual-pane detail view, including the roster-list details region
- **THEN** no control is presented that would spawn a worker, send a hera message, or mutate a plan node
