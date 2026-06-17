# hera-view

## ADDED Requirements

### Requirement: Coordinator Details renders rich coordinator metadata (area 6)

For a coordinator selection the system SHALL render, in the read-only `" Details "` roster panel and in addition to the coordinator status line and the Agents roster, a metadata block describing the coordinator's group: **Created** (the orchestrator's creation time), **Last activity** (the maximum over the orchestrator's creation time, each role's creation time, each role's live-binding start time, and each role's status-update time), **Agent** (the coordinator's bound argus task name, omitted when the coordinator is unbound), **Worktree** (the coordinator's live-binding worktree path, omitted when absent, shortened to its trailing `project/task` components when it overflows the available width), and **Repos in scope** (the distinct argus projects across the orchestrator's roster roles, sorted, rendered as a `(none)` line when empty). The system SHALL also render a reserved **Summary** field showing an `(auto-generated overview coming soon)` placeholder after the roster.

Every field SHALL be derived from the already-built rail model projection (no Draw-time I/O), so the Details pane never disagrees with the rail and stays safe on the tview main thread. Time fields SHALL render an en-dash placeholder when zero. The existing roster and the embedded `" Orchestration Tree "` graph SHALL remain unchanged — the metadata block is purely additive.

Derived from: `internal/tui/hera/details.go` (`deriveCoordMeta`, `Draw`, `ContentHeight`), `internal/tui/hera/model.go` (`OrchView`/`RoleView` projection fields, `BuildModel`).

#### Scenario: Created and Last activity render
- **WHEN** a coordinator with a live binding and at least one role status update is selected
- **THEN** the Details pane shows a `Created:` line with the orchestrator's creation time and a `Last activity:` line equal to the most recent of the orchestrator/role creation times, the live-binding start, and the role-status update

#### Scenario: Repos in scope are distinct and sorted
- **WHEN** a coordinator's roster roles span argus projects `b`, `a`, and `a`
- **THEN** the `Repos in scope:` block lists `a` and `b` once each, in sorted order

#### Scenario: Agent and Worktree come from the coordinator role
- **WHEN** the selected coordinator's role is bound to an argus task with a name and a worktree path
- **THEN** the Details pane shows an `Agent:` line with that task name and a `Worktree:` line with that worktree path (shortened when it overflows the pane)

#### Scenario: Unbound coordinator omits Agent and Worktree
- **WHEN** the selected coordinator has no live binding
- **THEN** the `Agent:` and `Worktree:` lines are omitted while Created, Last activity, Repos in scope, the roster, and the Summary placeholder still render

#### Scenario: Summary placeholder is reserved
- **WHEN** any coordinator is selected
- **THEN** the Details pane ends with a `Summary:` field showing `(auto-generated overview coming soon)`

#### Scenario: ContentHeight matches the rendered row budget
- **WHEN** the Details region sizes the roster panel via `ContentHeight()`
- **THEN** the returned height equals the exact number of rows `Draw` emits for the current selection, including the metadata block and the Summary placeholder
