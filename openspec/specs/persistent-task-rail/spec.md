# Persistent task rail

## Purpose

Keep a project-grouped Tasks navigator visible beside the selected agent when the optional TUI layout is enabled, while preserving the existing default layout and session lifecycle checks.

## Requirements

### Requirement: Optional persistent Tasks layout

When `ui.tasks_rail` is true, the Tasks tab SHALL show a project-grouped rail alongside the selected task's terminal content and SHALL keep the tab header visible. The rail SHALL remain visible during terminal interaction and agent zoom. When false or absent, the existing list, agent-entry, header, and exit behavior SHALL remain unchanged.

#### Scenario: Default layout remains unchanged

- **WHEN** the preference is absent or false and a task is opened
- **THEN** the existing full-page agent view replaces the task list and Ctrl+Q returns to the existing list layout

#### Scenario: Rail remains visible

- **WHEN** the preference is true and the user enters or zooms a task terminal
- **THEN** the project task rail remains on the left and the selected task content occupies the right area

### Requirement: Rail width and terminal geometry

The rail SHALL use 34 columns clamped to the available content width, matching Projects. The terminal SHALL use the remaining area, with PTY geometry derived from its actual inner rectangle and existing minimum-size rules. At widths of 34 columns or fewer, content SHALL be hidden, keyboard focus SHALL stay on the rail, and no zero-sized PTY resize SHALL be issued.

#### Scenario: Window resize preserves valid focus and dimensions

- **WHEN** a terminal-focused Tasks view shrinks to 30 columns and later grows to 120 columns
- **THEN** the rail remains usable, focus returns to the rail while content is hidden, and the content reappears with a valid pane size without restarting its session

### Requirement: Browsing binds without starting sessions

Moving the rail cursor to a task SHALL display its existing session or recorded output without starting or restarting it. Enter on a task SHALL focus its terminal using the existing task start/resume and Hera close-out guards. Completed and archived tasks SHALL be viewable without implicit restart. Selecting a folder header, removing the selected task, or hiding all tasks SHALL clear stale terminal bindings and display a selection or empty-state hint. Periodic refreshes of an unchanged selection/session SHALL NOT reset its terminal.

#### Scenario: Browse a stopped task

- **WHEN** the cursor moves to a stopped task
- **THEN** its recording or placeholder is shown and no session starts

#### Scenario: Explicit entry retains lifecycle safeguards

- **WHEN** Enter is pressed on an eligible stopped task or a task awaiting Hera close-out
- **THEN** the existing start/resume or close-out checks govern entry, and merely browsing never bypasses those checks

#### Scenario: Selection disappears

- **WHEN** the selected task is deleted or excluded by a filter
- **THEN** the pane follows the new visible task selection or clears to a hint, and stale output/input bindings cannot remain active

### Requirement: Rail and terminal focus routing

Rail focus SHALL retain Tasks mutations and substring-filter behavior. Enter on a task SHALL enter its terminal; Tab from the rail SHALL enter an already-bound visible terminal without starting it. Terminal Tab and Shift+Tab SHALL reach the agent. Ctrl+Q SHALL return focus to the rail without hiding the task pane or stopping its session. Modified Left/Right SHALL traverse the rail and visible content regions in the same direction as Projects, skipping hidden panels. Existing modified Up/Down task navigation SHALL select only tasks admitted by Tasks filters. Cross-tab arrows, when enabled, SHALL continue from the rightmost content region into Projects and return from Projects to Tasks content.

#### Scenario: Terminal autocomplete remains usable

- **WHEN** the terminal is focused and Tab or Shift+Tab is pressed
- **THEN** the input is forwarded to the task's PTY rather than moving focus

#### Scenario: Return to rail

- **WHEN** Ctrl+Q is pressed in the selected terminal
- **THEN** focus moves to the rail, its task pane remains visible, and ordinary navigation changes the previewed task

#### Scenario: Zoom keeps rail accessible

- **WHEN** Ctrl+Z hides the right area's git/file panels and modified Left is pressed in the terminal
- **THEN** the rail is still visible and gains focus without focusing a zero-width panel

#### Scenario: Mouse and paste target the intended region

- **WHEN** the user clicks a non-interactive content panel, clicks the rail, or pastes into a focused terminal
- **THEN** focus remains on a visible interactive region, the rail click focuses the rail, and terminal paste reaches only the bound task

### Requirement: Filters, status, and section parity

The rail SHALL use the existing task-list partition and filter predicates, including `H`, pinned-over-archive precedence, task Hera-role/PR indicators, needs-input priority, and running spinners. Project header status SHALL aggregate only admitted tasks in that section. A hidden task SHALL NOT be reachable through rail adjacency navigation. Changing `H` or `/` SHALL reconcile the terminal to the visible selection. Existing filter Enter SHALL confirm input first; a subsequent Enter on a task SHALL enter its terminal.

#### Scenario: Hide Hera-managed tasks

- **WHEN** `H` excludes the selected Hera worker while a plain task remains
- **THEN** the worker disappears, the pane follows the visible selection, and modified task navigation cannot revisit the hidden worker

#### Scenario: Folded project shows attention

- **WHEN** a folded project contains an admitted task needing input and another running task
- **THEN** the header shows the existing needs-input indicator ahead of its running spinner

### Requirement: Live preference transitions preserve sessions

Enabling or disabling the preference SHALL update the Tasks layout without restarting the TUI or starting/stopping task sessions. Enabling SHALL preserve the selected task. Disabling from Settings SHALL restore the normal list landing when returning to Tasks; reconciling an active task terminal with the preference disabled SHALL retain that task in the normal agent view. Focus claims SHALL follow actual terminal focus, and background data refresh SHALL NOT steal rail focus.

#### Scenario: Change preference in Settings

- **WHEN** the user enables the rail, returns to Tasks, disables it in Settings, and returns again
- **THEN** the first return shows the rail with the previous task selected, the second shows the legacy list, and neither toggle restarts an agent

#### Scenario: Refresh does not steal focus

- **WHEN** new output or task metadata arrives while the rail has focus
- **THEN** the rail retains keyboard focus and the current terminal is not falsely marked as receiving user focus
