## MODIFIED Requirements

### Requirement: One active project expanded at a time

The following focus-fold behavior and its scenarios SHALL apply in both Tasks layouts when `ui.tasks_expand_all` is false (the default). When true, every admitted active project SHALL remain expanded until the preference is toggled back, as specified in tasks-project-expansion. In the optional rail, project headers SHALL also be selectable and Space/Enter SHALL focus and expand that project. Pinned SHALL remain fully expanded in either mode.

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

When `ui.tasks_rail` is false, the following task-only cursor navigation and its scenarios SHALL apply. When true, cursor movement SHALL also land on project headers and the Archive fold header, skip separators and non-foldable section labels, and notify the page when a header selection clears the task binding.

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

When `ui.tasks_rail` is false, the following automatic archive behavior and its scenarios SHALL apply. When true, Archive SHALL start collapsed, SHALL toggle with Space or Enter on its header, and SHALL retain its expansion when the cursor leaves. Its project folders SHALL follow the remembered Tasks expansion mode: only the focused project in focus-fold mode, every admitted archived project in all-open mode.

The Archive section SHALL auto-expand when the cursor enters it and auto-collapse when the cursor leaves it. Within the expanded archive, exactly one archived project SHALL be expanded at a time in focus-fold mode, tracked independently from the active-section expansion; all archived projects SHALL remain expanded in all-open mode.

#### Scenario: Archive expands when cursor enters

- **WHEN** the cursor navigates into the Archive section
- **THEN** the archive expands to reveal its archived project groups

#### Scenario: Archive collapses when cursor leaves

- **WHEN** the cursor navigates out of the Archive section
- **THEN** the archive collapses back to its header

### Requirement: Task selection and status actions

When `ui.tasks_rail` is false, the following Enter behavior and its scenarios SHALL apply. When true, Enter on a task SHALL focus its terminal using the persistent-task-rail lifecycle rules, including read-only viewing of complete or archived tasks. Status, archive, pin, rename, and copy actions SHALL remain the same in either layout.

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

### Requirement: Task detail panel

The following detail-panel behavior SHALL apply in the default layout (`ui.tasks_rail` false). With the rail enabled, the existing agent content SHALL replace the list's separate metadata panel; this requirement SHALL NOT force an extra detail pane into the rail layout.

The detail panel SHALL display metadata for the selected task: name, status (annotated "(running)" or "(idle)" for in-progress), and any present fields among project, branch, backend, sandbox flag, PID, worktree (truncated to fit), created date, elapsed time, and prompt. The PID field SHALL be shown only when the task has a nonzero recorded agent PID, and its value is the last PID recorded for that task's most recent session — it is not itself a liveness indicator (the running/idle status annotation remains the source of truth for whether the process is alive). When no task is selected, it SHALL display "No task selected" and SHALL not error.

#### Scenario: No task selected shows placeholder

- **WHEN** the detail panel has no task set
- **THEN** it renders "No task selected"

#### Scenario: In-progress status annotates running vs idle

- **WHEN** the selected task is in-progress and its session is running
- **THEN** the status line reads "(running)", and reads "(idle)" when the session is not running

#### Scenario: Absent fields are omitted

- **WHEN** the selected task has an empty branch, backend, or worktree
- **THEN** that field's row is not rendered

#### Scenario: PID row shown when known

- **WHEN** the selected task has a nonzero agent PID
- **THEN** the panel shows a "PID" row with that value

#### Scenario: PID row omitted when never started

- **WHEN** the selected task has never been started and has no recorded agent PID
- **THEN** the panel does not render a PID row

#### Scenario: A stale PID from an exited session is still shown

- **WHEN** the selected task's session has exited but its last recorded agent PID is still nonzero
- **THEN** the panel still shows that PID value, alongside the "(idle)" or non-in-progress status that indicates the process is not necessarily still running

### Requirement: Task output preview panel

The following snapshot-preview behavior SHALL apply in the default layout (`ui.tasks_rail` false). With the rail enabled, browsing SHALL bind the existing terminal pane according to the persistent-task-rail requirements instead of rendering this separate snapshot panel.

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

The following full-page empty-state and three-panel layout behavior SHALL apply in the default layout (`ui.tasks_rail` false). With the rail enabled, an empty rail SHALL remain visible beside a create/select hint, and the `n` action SHALL remain available.

When there are no tasks at all, the task page SHALL display a centered banner with the hint "Press [n] to create your first task" instead of the three-panel layout. When tasks exist, the three-panel layout SHALL be shown and the hint SHALL not appear.

#### Scenario: Banner shown with no tasks

- **WHEN** the task page is drawn and there are no tasks
- **THEN** the empty-state hint text is rendered

#### Scenario: Layout shown with tasks present

- **WHEN** the task page is drawn and at least one task exists
- **THEN** the three-panel layout is rendered and the empty-state hint is absent
