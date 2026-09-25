# Task List View

## MODIFIED Requirements

### Requirement: Task detail panel

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

### Requirement: Substring filter narrows visible tasks

Pressing `/` SHALL activate a filter input mode. Typed text SHALL filter tasks by case-insensitive substring; whitespace splits the text into terms, and every term MUST match the task's name, its project name, or (when the task has a nonzero recorded agent PID) the decimal string of that PID, for the task to remain visible. While a filter is active, all matching projects and sections SHALL be shown expanded regardless of normal collapse state. Escape SHALL clear the filter; Enter SHALL confirm the filter (keeping the text but exiting input mode).

#### Scenario: Filter matches name or project per term

- **WHEN** the filter text is "forge download" and a task named "Download-this-video" lives in project "forge"
- **THEN** the task remains visible because each term matches the name or project

#### Scenario: Filter is case-insensitive

- **WHEN** the filter text differs only in letter case from a task name or project
- **THEN** the task still matches

#### Scenario: Filter matches a PID substring

- **WHEN** the filter text is "7557" and a task has a recorded agent PID of 17557
- **THEN** the task remains visible

#### Scenario: A never-started task does not match on PID

- **WHEN** the filter text is "0" and a task has no recorded agent PID (zero)
- **THEN** that task does not match on the strength of the PID field alone

#### Scenario: Escape clears an active filter

- **WHEN** the filter input is active and the user presses Escape
- **THEN** the filter text is cleared and filter input mode exits

#### Scenario: Enter confirms the filter

- **WHEN** the filter input is active with text and the user presses Enter
- **THEN** the filter text is retained and filter input mode exits

#### Scenario: Filtered view expands all groups

- **WHEN** a filter is active
- **THEN** every project group containing a match is shown expanded, including archived groups
