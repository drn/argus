## ADDED Requirements

### Requirement: Global keyboard shortcuts and overflow actions

The system SHALL provide keyboard shortcuts for switching the active detail tab, opening a shortcuts-help sheet, and triggering the existing destroy, fork, open-repo, and open-PR actions, and SHALL provide a keyboard shortcut that selects the next task whose session needs input. The system SHALL add a "Prune stale worktrees" item to the toolbar overflow menu that triggers the existing prune action.

#### Scenario: Switch tabs via shortcut

- **WHEN** the user presses the tab-switch shortcut for a given tab index
- **THEN** the detail pane's active tab changes to the corresponding tab (Terminal/Diff/Files/Info)

#### Scenario: Open shortcuts help via shortcut

- **WHEN** the user presses the help shortcut
- **THEN** a sheet listing the app's keyboard shortcuts is presented

#### Scenario: Trigger destroy via shortcut

- **WHEN** the user presses the destroy shortcut with a task selected
- **THEN** the same confirmation dialog used by the existing mouse-driven destroy action is shown before any destructive request is issued

#### Scenario: Trigger fork via shortcut

- **WHEN** the user presses the fork shortcut with a task selected
- **THEN** the existing fork action is triggered identically to its mouse-driven equivalent

#### Scenario: Open repo via shortcut

- **WHEN** the user presses the open-repo shortcut with a task selected
- **THEN** the task's worktree opens in Finder/editor identically to its mouse-driven equivalent

#### Scenario: Open PR via shortcut from either context

- **WHEN** the user presses the open-PR shortcut, whether the app's global scope or the agent/terminal view is focused
- **THEN** the task's PR opens in the browser identically to its mouse-driven equivalent

#### Scenario: Jump to next needs-input task via shortcut

- **WHEN** the user presses the jump-to-needs-input shortcut
- **THEN** the rail selection moves to the next task whose session needs input, matching the TUI's jump behavior

#### Scenario: Prune stale worktrees from overflow menu

- **WHEN** the user selects "Prune stale worktrees" from the toolbar overflow menu
- **THEN** the existing prune action runs identically to how it runs from the TUI

### Requirement: Task rail quick actions

The system SHALL show status-advance and status-revert as right-click context-menu items on a task rail row, and SHALL provide keyboard shortcuts for the existing archive action and a new pin action.

#### Scenario: Status advance/revert via context menu

- **WHEN** the user right-clicks a task row
- **THEN** the context menu includes status-advance and status-revert items that perform the same transition as the TUI's `s`/`S` keys

#### Scenario: Archive via shortcut

- **WHEN** the user presses the archive shortcut with a task selected
- **THEN** the existing archive action is triggered identically to its mouse-driven equivalent

#### Scenario: Pin via shortcut

- **WHEN** the user presses the pin shortcut with a task selected
- **THEN** the task is pinned/unpinned via the daemon's raw-task round-trip (no dedicated pin endpoint exists), reflected in the UI identically to a hypothetical mouse-driven equivalent

### Requirement: Task rail filter access

The system SHALL provide a keyboard shortcut that focuses the sidebar's filter field, and SHALL show a persistent, visible toggle in the sidebar's filter bar for showing or hiding hera-managed tasks.

#### Scenario: Focus filter field via shortcut

- **WHEN** the user presses the filter shortcut
- **THEN** keyboard focus moves to the sidebar's filter text field

#### Scenario: Toggle hera-managed visibility via persistent control

- **WHEN** the user changes the hera-managed visibility toggle in the sidebar's filter bar
- **THEN** hera-managed tasks are shown or hidden in the rail according to the toggle's state, and the toggle's current state is visible without opening any menu

### Requirement: Terminal view keyboard shortcuts

The system SHALL intercept a fixed allowlist of Cmd- and Shift-modified key chords in the Terminal tab — Cmd+Up/Down (previous/next task), Cmd+Left/Right (detail-tab cycling — no split-pane view exists in the mac app; this is the resolved analog to the TUI's pane-focus chord), Shift+Up/Down/PageUp/PageDown/End (scrollback), and a copy-visible-output shortcut — before they reach the terminal surface, and SHALL forward every other keystroke to `POST /api/tasks/{id}/input` unchanged, identically to current behavior.

#### Scenario: Switch tasks via Cmd+Up/Down without PTY leak

- **WHEN** the terminal has focus and the user presses Cmd+Up or Cmd+Down
- **THEN** the rail selection moves to the previous/next task and no bytes for that keystroke are sent to `POST /input`

#### Scenario: Cycle the detail tab via Cmd+Left/Right without PTY leak

- **WHEN** the terminal has focus and the user presses Cmd+Left or Cmd+Right
- **THEN** the active detail tab (Terminal/Diff/Files/Info) cycles to the previous/next tab and no bytes for that keystroke are sent to `POST /input`

#### Scenario: Scroll via Shift chords without PTY leak

- **WHEN** the terminal has focus and the user presses Shift+Up, Shift+Down, Shift+PageUp, Shift+PageDown, or Shift+End
- **THEN** the terminal's scrollback position changes accordingly and no bytes for that keystroke are sent to `POST /input`

#### Scenario: Copy visible output via shortcut

- **WHEN** the terminal has focus and the user presses the copy-output shortcut
- **THEN** the terminal's currently visible output is copied to the system clipboard

#### Scenario: Unclaimed keystrokes still reach the PTY unchanged

- **WHEN** the terminal has focus and the user presses any key not in the intercepted allowlist
- **THEN** the corresponding bytes are forwarded to `POST /input` unchanged, exactly as before this change

### Requirement: Claude session switcher

The system SHALL provide a toolbar button in the Terminal tab that opens a picker sheet listing the current task's available Claude sessions, mirroring the TUI's session-switch action.

#### Scenario: Open session picker and switch sessions

- **WHEN** the user selects the session-switcher toolbar button and chooses a session from the picker sheet
- **THEN** the terminal view attaches to the selected Claude session for that task
