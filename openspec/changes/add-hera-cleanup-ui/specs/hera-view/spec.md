## ADDED Requirements

### Requirement: Cleanup popup for the stuck-task backlog

The Hera view SHALL provide a Cleanup popup, reachable via the Ctrl+K command palette (not scoped to the currently-selected coordinator/orchestrator), that lists every task matching the stuck-task predicate (`archived=1`, `status=in_review`, no live Hera binding) across ALL projects, grouped into a **Safe** section (confirmed merged by the merge-safety classifier) and a **Needs Review** section (not confirmed), rendered as a scrollable, sectioned list mirroring the task switcher's grouped/header-row pattern. Each Needs Review row SHALL show the reason it was held back.

#### Scenario: Popup lists candidates in two sections
- **WHEN** the Cleanup popup is opened
- **THEN** it shows every stuck-task-predicate-matching task across all projects, split into a Safe section and a Needs Review section

#### Scenario: Needs Review rows show why
- **WHEN** a task appears in the Needs Review section
- **THEN** its row shows the specific reason it wasn't confirmed safe (e.g. branch gone with no matching merged PR, ambiguous branch-name reuse, unresolvable project)

#### Scenario: First open triggers classification with a visible wait state
- **WHEN** the Cleanup popup is opened and candidates exist without a cached classification
- **THEN** the popup triggers classification and shows a scanning/in-progress state until results are ready, rather than appearing empty or frozen

### Requirement: Cleanup popup bulk actions mark tasks complete, never delete

The Cleanup popup SHALL offer two bulk actions: mark only the Safe section's tasks complete, or mark every listed task (Safe and Needs Review) complete. Both actions SHALL only advance task status to `complete` (via the existing status-transition primitive) — neither SHALL delete a task row, worktree, or branch. Actual removal remains a separate, deliberate, human-triggered step via the existing `Ctrl+R` prune-completed flow.

#### Scenario: Mark-safe-complete only touches the Safe section
- **WHEN** the operator chooses "mark safe complete"
- **THEN** every task in the Safe section has its status advanced to `complete`; Needs Review tasks are untouched

#### Scenario: Mark-all-complete touches every listed task
- **WHEN** the operator chooses "mark all complete"
- **THEN** every listed task (Safe and Needs Review) has its status advanced to `complete`

#### Scenario: Neither bulk action deletes anything
- **WHEN** either bulk action is applied
- **THEN** no task row, worktree, or branch is deleted — only the `status` column changes, leaving the tasks reachable by (but not touched by) the existing prune-completed flow

#### Scenario: A task that stopped qualifying is skipped silently
- **WHEN** a bulk action processes a listed task that no longer matches the stuck-task predicate at apply time
- **THEN** that task is left untouched and the rest of the batch proceeds normally
