# Task List View

## Purpose

The task list view is the primary navigation surface for browsing all tasks, grouped by project into collapsible folders. It provides keyboard-driven cursor navigation, a substring filter, pinned and archive sections, per-task and per-project status indicators, and an accompanying detail and live-output preview pane for the selected task. It exists so a user can scan the state of every task at a glance and drill into one without leaving the keyboard.
## Requirements
### Requirement: Project grouping and section ordering

Tasks SHALL be grouped by project name (alphabetically), and partitioned into three sections rendered top-to-bottom: Pinned, Active, and Archive. A task's section SHALL be chosen by precedence where pinned wins over archived, and archived wins over active. Tasks with no project name SHALL be grouped under "(no project)".

#### Scenario: Pinned task surfaces above active and archive

- **WHEN** a task is both pinned and archived
- **THEN** it appears in the Pinned section, not the Archive section

#### Scenario: Empty project name is grouped under a placeholder

- **WHEN** a task has an empty project name
- **THEN** it is grouped under the project label "(no project)"

#### Scenario: Pinned section renders above active

- **WHEN** there are both pinned and active tasks
- **THEN** the Pinned section header and its tasks render above the active project groups

### Requirement: One active project expanded at a time

Within the Active section, exactly one project SHALL be expanded at a time, showing its task rows; all other active project folders SHALL be collapsed to their header. When no project is expanded and active projects exist, the first project SHALL auto-expand. The Pinned section SHALL always be fully expanded and SHALL never auto-collapse.

#### Scenario: First active project auto-expands

- **WHEN** tasks are loaded and no project is currently expanded
- **THEN** the first active project (alphabetically) becomes expanded and its task rows are visible

#### Scenario: Moving the cursor into another project switches expansion

- **WHEN** the cursor moves into a different active project
- **THEN** that project expands and the previously expanded project collapses

#### Scenario: Pinned section stays expanded when cursor leaves

- **WHEN** the cursor leaves the Pinned section into another section
- **THEN** the Pinned section remains fully expanded

### Requirement: Cursor navigation skips non-task rows

Cursor movement (up/down via arrow keys or j/k) SHALL always land the cursor on a task row, skipping project headers, section headers, and separators. When moving up past a project header, the cursor SHALL land on the last task of the previous expanded project. The cursor change callback SHALL fire only when the cursor actually moves to a different position.

#### Scenario: Cursor never rests on a header

- **WHEN** the cursor moves in either direction across a project or section header
- **THEN** it lands on a task row, never on the header or a separator

#### Scenario: Moving up past a header lands on the previous group's last task

- **WHEN** the cursor is on the first task of a project and the user moves up
- **THEN** the cursor lands on the last task of the previous expanded project

#### Scenario: No movement fires no cursor-change notification

- **WHEN** the cursor is at a boundary and a move attempt does not change its position
- **THEN** the cursor-change callback does not fire

### Requirement: Archive section auto-expand on cursor entry

The Archive section SHALL auto-expand when the cursor enters it and auto-collapse when the cursor leaves it. Within the expanded archive, exactly one archived project SHALL be expanded at a time, tracked independently from the active-section expansion.

#### Scenario: Archive expands when cursor enters

- **WHEN** the cursor navigates into the Archive section
- **THEN** the archive expands to reveal its archived project groups

#### Scenario: Archive collapses when cursor leaves

- **WHEN** the cursor navigates out of the Archive section
- **THEN** the archive collapses back to its header

### Requirement: Substring filter narrows visible tasks

Pressing `/` SHALL activate a filter input mode. Typed text SHALL filter tasks by case-insensitive substring; whitespace splits the text into terms, and every term MUST match either the task name or its project name for the task to remain visible. While a filter is active, all matching projects and sections SHALL be shown expanded regardless of normal collapse state. Escape SHALL clear the filter; Enter SHALL confirm the filter (keeping the text but exiting input mode).

#### Scenario: Filter matches name or project per term

- **WHEN** the filter text is "forge download" and a task named "Download-this-video" lives in project "forge"
- **THEN** the task remains visible because each term matches the name or project

#### Scenario: Filter is case-insensitive

- **WHEN** the filter text differs only in letter case from a task name or project
- **THEN** the task still matches

#### Scenario: Escape clears an active filter

- **WHEN** the filter input is active and the user presses Escape
- **THEN** the filter text is cleared and filter input mode exits

#### Scenario: Enter confirms the filter

- **WHEN** the filter input is active with text and the user presses Enter
- **THEN** the filter text is retained and filter input mode exits

#### Scenario: Filtered view expands all groups

- **WHEN** a filter is active
- **THEN** every project group containing a match is shown expanded, including archived groups

### Requirement: Task selection and status actions

Pressing Enter on a non-complete task SHALL fire the select callback; Enter on a complete task SHALL do nothing. Keyboard actions on the selected task SHALL cycle status forward (`s`) and backward (`S`), toggle archive (`a`), toggle pinned (`P`), rename (`r`), and open the copy-choice menu (`c`). Each action SHALL fire its corresponding callback.

#### Scenario: Enter ignores completed tasks

- **WHEN** the cursor is on a task with complete status and the user presses Enter
- **THEN** the select callback does not fire

#### Scenario: Enter selects an in-progress task

- **WHEN** the cursor is on a non-complete task and the user presses Enter
- **THEN** the select callback fires with that task

#### Scenario: Status cycle keys advance and reverse status

- **WHEN** the user presses `s` (or `S`) on the selected task
- **THEN** the task status advances to its next (or previous) value and the status-change callback fires

#### Scenario: Copy key opens the copy-choice menu

- **WHEN** the user presses `c` on the selected task
- **THEN** the copy callback fires with that task (regardless of whether the prompt is empty), so the caller can present a choice of copying the name or the prompt

### Requirement: Per-task status indicator

Each task row SHALL display a status glyph reflecting its state. For an in-progress task the glyph SHALL reflect, in priority order: needs-input (agent blocked on a prompt), idle-but-unvisited, idle/session-absent, and actively-running (animated spinner). Pending tasks, in-review tasks, and complete tasks SHALL each render their own distinct glyph.

#### Scenario: Needs-input outranks other in-progress states

- **WHEN** an in-progress task is flagged as needing user input
- **THEN** its row shows the needs-input glyph even if it is also actively running or idle-unvisited

#### Scenario: Actively-running task shows a spinner

- **WHEN** an in-progress task has a live session and is not idle or needs-input
- **THEN** its row shows the animated spinner glyph

### Requirement: Per-project aggregated status indicator

Each project header SHALL display a single aggregated status glyph computed from the statuses of that project's tasks within that header's section. The aggregation priority SHALL be: any needs-input, then any actively-running, then any in-review, then idle in-progress, then all-complete, then mixed complete-and-pending, then all-pending.

#### Scenario: One blocked task surfaces at the project level

- **WHEN** any task in a busy project is blocked on user input
- **THEN** the project header shows the needs-input glyph even though other tasks are actively running

#### Scenario: Aggregation is scoped to the header's section

- **WHEN** a project name exists in more than one section
- **THEN** the header's aggregated glyph reflects only the tasks in that header's own section

### Requirement: Task detail panel

The detail panel SHALL display metadata for the selected task: name, status (annotated "(running)" or "(idle)" for in-progress), and any present fields among project, branch, backend, sandbox flag, worktree (truncated to fit), created date, elapsed time, and prompt. When no task is selected, it SHALL display "No task selected" and SHALL not error.

#### Scenario: No task selected shows placeholder

- **WHEN** the detail panel has no task set
- **THEN** it renders "No task selected"

#### Scenario: In-progress status annotates running vs idle

- **WHEN** the selected task is in-progress and its session is running
- **THEN** the status line reads "(running)", and reads "(idle)" when the session is not running

#### Scenario: Absent fields are omitted

- **WHEN** the selected task has an empty branch, backend, or worktree
- **THEN** that field's row is not rendered

### Requirement: Task output preview panel

The preview panel SHALL render a terminal snapshot of the selected task's most recent agent output, showing the latest visible lines (newest output at the bottom) clipped to the panel viewport. It SHALL show placeholder text — "No task selected", "Loading...", "Waiting for output...", or "Preview unavailable" — when there is no task, no output yet, or output cannot be rendered. Switching the previewed task SHALL clear the cached snapshot. Drawing SHALL never block or panic, including at zero dimensions.

#### Scenario: Latest output lines are shown

- **WHEN** the agent output has more lines than the preview viewport height
- **THEN** the newest lines are shown at the bottom and the oldest top-of-buffer lines are dropped

#### Scenario: Empty output shows a waiting placeholder

- **WHEN** the previewed task has produced no output
- **THEN** the panel shows "Waiting for output..."

#### Scenario: Switching task clears the snapshot

- **WHEN** the previewed task id changes
- **THEN** the cached cells are cleared and the panel shows "Loading..." until new output arrives

#### Scenario: Unrenderable output degrades gracefully

- **WHEN** output cannot be rendered by the terminal emulator
- **THEN** the panel shows "Preview unavailable" rather than crashing

### Requirement: Empty-state banner

When there are no tasks at all, the task page SHALL display a centered banner with the hint "Press [n] to create your first task" instead of the three-panel layout. When tasks exist, the three-panel layout SHALL be shown and the hint SHALL not appear.

#### Scenario: Banner shown with no tasks

- **WHEN** the task page is drawn and there are no tasks
- **THEN** the empty-state hint text is rendered

#### Scenario: Layout shown with tasks present

- **WHEN** the task page is drawn and at least one task exists
- **THEN** the three-panel layout is rendered and the empty-state hint is absent

### Requirement: Cursor preservation across data refresh

When the task list is refreshed with a new task set, the cursor SHALL be restored to the same task (or project) within the same section when it still exists; otherwise the cursor SHALL be clamped to a valid task row.

#### Scenario: Cursor follows the same task after refresh

- **WHEN** the task set is replaced and the previously selected task still exists in the same section
- **THEN** the cursor is restored onto that same task

### Requirement: Hide hera-managed tasks toggle

The task list SHALL provide a single hera-visibility toggle bound to the `H` key. The toggle SHALL default to OFF (hera-managed tasks **visible** inline), so the Tasks tab shows hera-spawned workers and live coordinators alongside plain tasks by default, each marked with a per-row hera-role indicator. While the toggle is ON, the list SHALL hide every **hera-managed** task and SHALL show freelancer and plain non-hera tasks; pressing `H` toggles between the two states.

A task SHALL be classified as **hera-managed** when EITHER of the following holds:

- It is a hera-spawned worker — its `task_meta` `hera.role` is `worker` (the sidecar stamped at spawn/join, which is permanent and is never cleared when a binding ends); OR
- It holds at least one live hera binding (a binding whose `ended_at` is unset) to a role of kind `coordinator` or `worker`, as reported by the hera bindings/roles store.

A task SHALL be classified as a **freelancer** (and therefore SHALL remain visible regardless of the toggle) when it is neither a hera-spawned worker nor holds a live coordinator/worker binding — i.e. it has no live binding, or holds only `freelance`-kind live bindings. A plain non-hera task (no hera role at all) SHALL likewise always remain visible.

The toggle SHALL compose with the substring filter (`/`) — each is an independent exclusion applied in the same row-build pass. In remote (`--remote`) mode, where no binding-query REST endpoint exists, the live-binding signal MAY fall back to a best-effort union of the `task_meta` `hera.role` worker and coordinator entries; this MAY report a finished worker or coordinator as still managed until the next tick refresh, and is a known degradation documented in the design.

#### Scenario: Hera worker visible by default, hidden by H

- **WHEN** a task is a hera-spawned worker and the toggle is OFF (the default)
- **THEN** the task is shown in the Tasks tab with a worker indicator; pressing `H` hides it and pressing `H` again shows it

#### Scenario: Live coordinator visible by default, hidden by H

- **WHEN** a task holds a live coordinator-kind binding and the toggle is OFF (the default)
- **THEN** the task is shown in the Tasks tab with a coordinator indicator; pressing `H` hides it and pressing `H` again shows it

#### Scenario: Freelancer always visible

- **WHEN** a task has no live hera binding (or only `freelance`-kind live bindings) and is not a hera-spawned worker
- **THEN** the task is visible regardless of the toggle state

#### Scenario: Plain non-hera task always visible

- **WHEN** a task holds no hera role at all
- **THEN** the task is visible regardless of the toggle state

#### Scenario: Composes with the substring filter

- **WHEN** the toggle is ON and a substring filter is active
- **THEN** a task is visible only if it is not hera-managed AND matches every substring term

### Requirement: Per-task hera-role indicator

Each task row SHALL render a hera-role indicator in a dedicated indicator cell when the task participates in Hera, so a hera-managed task is identifiable at a glance without opening the Hera tab. A task holding a Hera coordinator role SHALL render the coordinator glyph; a hera-spawned worker (or a task holding a live worker-kind binding) that is not a coordinator SHALL render a distinct worker glyph. The coordinator glyph SHALL take precedence if a task would qualify for both. Freelancer and plain non-hera tasks SHALL render no hera-role indicator. The indicator cell SHALL consume row width only when an indicator is present (the name column reclaims the space otherwise) and SHALL be orthogonal to — never replace — the status and PR-review glyphs.

#### Scenario: Worker row shows the worker indicator

- **WHEN** a task row is a hera-spawned worker that holds no coordinator role
- **THEN** the row renders the worker glyph in the hera-role indicator cell

#### Scenario: Coordinator outranks worker

- **WHEN** a task qualifies as both a coordinator and a worker
- **THEN** the row renders the coordinator glyph, not the worker glyph

#### Scenario: Plain task renders no hera indicator

- **WHEN** a task holds no hera role
- **THEN** no hera-role indicator cell is drawn and the name column reclaims the space

### Requirement: Prune completed tasks is confirmation-gated

The system SHALL gate the Tasks-tab `Ctrl+R` "prune completed tasks" action
behind an explicit y/N confirmation before any deletion occurs. On `Ctrl+R`
(Tasks tab, task-list mode), the system SHALL count the completed tasks and:

- WHEN at least one completed task exists, open a confirmation modal
  (`modal.ConfirmModal`, title "Prune completed tasks") whose message names the
  count and states that the tasks' worktrees and branches will be removed and
  the action cannot be undone. The destructive two-phase prune SHALL run ONLY on
  an explicit confirm (`Enter` or `y`); `Esc`, `Ctrl+Q`, or `n` SHALL dismiss
  the modal and remove nothing.
- WHEN no completed task exists, skip the modal entirely and surface a brief
  status note ("No completed tasks to prune") instead of acting silently.

The system SHALL NOT open the confirmation while a prune is already in progress
(the header cleanup notice is set), preserving the prior double-`Ctrl+R`
re-entrancy guard. The `Ctrl+R` keybinding and its "prune completed" label are
unchanged, so the help modal and statusbar are unaffected.

#### Scenario: Ctrl+R opens the confirmation, prunes nothing yet

- **WHEN** the user presses `Ctrl+R` on the Tasks tab with at least one completed task
- **THEN** the confirm-prune modal opens and no task is removed until the user confirms

#### Scenario: Confirming prunes completed tasks

- **WHEN** the confirm-prune modal is open and the user presses `Enter` or `y`
- **THEN** the modal is dismissed and the completed tasks are pruned (DB rows, sessions, worktrees, and branches), leaving non-completed tasks intact

#### Scenario: Cancelling removes nothing

- **WHEN** the confirm-prune modal is open and the user presses `Esc` or `n`
- **THEN** the modal is dismissed and every task — completed and active — remains

#### Scenario: Nothing to prune skips the modal

- **WHEN** the user presses `Ctrl+R` with no completed tasks
- **THEN** no modal opens and a "No completed tasks to prune" status note is shown

### Requirement: Mouse wheel scrolls the task list

The task list SHALL respond to mouse wheel scroll events within its rect by moving the cursor: scroll-up moves the cursor DOWN one row, scroll-down moves it UP one row, skipping project/section headers and separators identically to keyboard navigation. This mapping is deliberately inverted relative to a plain content-pane scroll — the cursor is what the gesture drags, so it moves in the same direction as the operator's fingers (trackpad "natural" scrolling) rather than the direction a viewport would pan. A left-click within the task list's rect SHALL focus it. Mouse events outside the task list's rect SHALL NOT be consumed.

#### Scenario: Scroll up moves the cursor to the next task

- **WHEN** the operator scrolls the mouse wheel up while the pointer is over the task list
- **THEN** the cursor moves to the next task row, the same target `CursorDown` would select

#### Scenario: Scroll down moves the cursor to the previous task

- **WHEN** the operator scrolls the mouse wheel down while the pointer is over the task list
- **THEN** the cursor moves to the previous task row, the same target `CursorUp` would select

#### Scenario: A click focuses the task list

- **WHEN** the operator left-clicks within the task list's rect
- **THEN** the task list receives focus

#### Scenario: Mouse events outside the rect are ignored

- **WHEN** a mouse event's position falls outside the task list's current rect
- **THEN** the event is not consumed and the cursor does not move

