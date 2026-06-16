# TUI Shell

## Purpose

The TUI Shell is the top-level terminal application frame for Argus. It owns the persistent layout (header tab bar, body, status bar), routes every global keystroke to the correct destination, switches between top-level tabs and full-screen views, surrenders the keyboard to plugin-registered views, and provides the cross-cutting visual primitives (theme palette, status icons) and configurable spinner animation. It also exposes the persistence interface (Store) that the rest of the TUI consumes without knowing whether it is backed by local SQLite or a remote HTTP API.

## Requirements

### Requirement: Top-level layout

The shell SHALL present a fixed vertical layout: a single-row header (tab bar) at the top, a body region that swaps between views, and a single-row status bar at the bottom. The body region SHALL display exactly one view at a time.

#### Scenario: Persistent frame around the active view

- **WHEN** the application is running and any view is active
- **THEN** the header occupies the top row, the status bar occupies the bottom row, and the currently selected view fills the region between them

#### Scenario: Header restored on return to a root view

- **WHEN** the user leaves the agent view and returns to the task list
- **THEN** the header row is restored to one row high and reflects the Tasks tab as active

### Requirement: Top-level tab navigation

The shell SHALL expose three top-level tabs in order — Tasks, Hera, Settings — and SHALL switch among them in response to global keys. Tab switching SHALL update both the header's active tab and the status bar's tab context, and SHALL move focus to the newly activated view.

#### Scenario: Numeric keys select tabs by position

- **WHEN** the user presses `1`, `2`, or `3` while not in the agent view
- **THEN** the active tab becomes Tasks, Hera, or Settings respectively and that view is shown with focus

#### Scenario: Arrow keys step between adjacent tabs

- **WHEN** the user presses Left or Right while not in the agent view and the active view does not itself consume the key
- **THEN** the active tab moves to the previous or next adjacent tab, clamped at the Tasks and Settings ends

#### Scenario: Switching to a tab refreshes its content

- **WHEN** the user switches to the Hera tab or the Settings tab
- **THEN** the shell refreshes that view's content before showing it

### Requirement: Application quit

The shell SHALL provide global keys to terminate the application from the top-level views.

#### Scenario: Quit from the task list

- **WHEN** the user presses `q` while on the task list and not filtering or editing
- **THEN** the application event loop stops and the program exits

#### Scenario: Ctrl+C quits outside the agent view

- **WHEN** the user presses Ctrl+C while not in the agent view
- **THEN** the application event loop stops

#### Scenario: Ctrl+C is forwarded to the agent inside the agent view

- **WHEN** the user presses Ctrl+C while in the agent view and a live session is attached
- **THEN** the keystroke is sent to the agent process as an interrupt rather than quitting the application

### Requirement: Help overlay

The shell SHALL open a help overlay on demand from the top-level views and SHALL not open it from within the agent view.

#### Scenario: Open help from a top-level view

- **WHEN** the user presses `?` while not in the agent view and not filtering or editing
- **THEN** a help overlay is shown over the current view

### Requirement: Manual screen refresh

The shell SHALL repair screen damage on explicit user request without otherwise re-emitting the full screen during normal updates.

#### Scenario: Ctrl+L forces a full repaint outside the agent view

- **WHEN** the user presses Ctrl+L while not in the agent view
- **THEN** the shell performs a full screen re-emit to clear any stale cells

### Requirement: Agent view entry and exit routing

The shell SHALL treat the agent view as a distinct mode in which global tab/quit/help keys are suppressed in favor of agent-specific handling, and SHALL reset shell state when the agent view is exited.

#### Scenario: Returning to Tasks tab while in the agent view exits it

- **WHEN** the active tab is switched to Tasks while the agent view is active
- **THEN** the shell exits the agent view, resets the active tab to Tasks, shows the task list, and moves focus to it

#### Scenario: Exiting the agent view resets the active tab

- **WHEN** the user exits the agent view
- **THEN** the active tab is reset to Tasks regardless of which tab was active when the agent view was entered, and the agent session is detached

### Requirement: Plugin-view keyboard surrender

When a plugin-registered full-screen view is active, the shell SHALL forward all keystrokes to the plugin and reserve no key for its own navigation, except a double-Ctrl+Q failsafe and a single-key dismissal of a plugin-triggered help overlay.

#### Scenario: All keys forwarded while a plugin holds the keyboard

- **WHEN** a plugin view is active and the user presses any key other than the failsafe sequence
- **THEN** the keystroke is forwarded to the plugin and the shell takes no navigation action

#### Scenario: Double Ctrl+Q failsafe returns control to the shell

- **WHEN** a plugin view is active and the user presses Ctrl+Q twice within the failsafe window
- **THEN** the shell deactivates the plugin view and reclaims the keyboard instead of forwarding the second Ctrl+Q

#### Scenario: Plugin help overlay consumes the next key to dismiss

- **WHEN** a plugin-triggered help overlay is visible and the user presses any key
- **THEN** the shell consumes that single key to dismiss the overlay and returns control to the plugin

### Requirement: Plugin-view hotkey activation

The shell SHALL activate a plugin-registered view when its registered hotkey is pressed from the task list. Hotkeys SHALL be recognized in the `ctrl+<letter>` form, case-insensitively.

#### Scenario: Registered hotkey opens its plugin view

- **WHEN** the user presses a non-rune key that matches a registered plugin hotkey while on the task list
- **THEN** the corresponding plugin view is activated

#### Scenario: Hotkey string parsing

- **WHEN** a hotkey string such as `ctrl+l` or `CTRL+L` is parsed
- **THEN** it resolves to the matching control-letter key, while malformed forms (empty, multi-letter, non-letter, or non-`ctrl+` prefixes) are rejected

### Requirement: Plugin top-level view registry

The shell SHALL persist plugin-registered top-level views keyed by (scope, title) and SHALL enforce that each registration has a non-empty title and callback URL and is unique per (scope, title) pair.

#### Scenario: Registration rejects missing title or callback URL

- **WHEN** a view is registered with an empty/whitespace title or an empty callback URL
- **THEN** the registration is rejected with the corresponding error and nothing is persisted

#### Scenario: Duplicate (scope, title) rejected

- **WHEN** a view is registered for a (scope, title) pair that already exists
- **THEN** the registration is rejected as already-registered

#### Scenario: Same title under different scopes allowed

- **WHEN** two views share a title but belong to different scopes
- **THEN** both registrations succeed and both appear in the registry listing

#### Scenario: Scope revocation cascades

- **WHEN** a scope is revoked
- **THEN** every view owned by that scope is removed and views owned by other scopes are retained

### Requirement: Configurable spinner animation

The shell SHALL provide a set of named spinner styles, each with an ordered frame sequence and a per-frame tick interval, and SHALL select frames by animation tick with wraparound. The active style SHALL be set from configuration at startup and SHALL be cyclable at runtime in both directions with wraparound.

#### Scenario: Frame selection wraps around the sequence

- **WHEN** a tick index greater than or equal to the frame count is requested for a spinner
- **THEN** the returned frame is the one at the tick index modulo the frame count

#### Scenario: Unknown style falls back to the default

- **WHEN** a spinner is requested for an unknown style name
- **THEN** the default (Progress) spinner is returned

#### Scenario: Cycling styles wraps in both directions

- **WHEN** the next style after the last is requested, or the previous style before the first
- **THEN** the cycle wraps to the first or last style respectively, and an unrecognized current style yields the default

### Requirement: Spinner animation only while work is active

The shell SHALL drive spinner repaints only while at least one task is actively running and not idle, and SHALL suppress periodic spinner repaints when all tasks are idle.

#### Scenario: No spinner repaints when all tasks are idle

- **WHEN** there are no running tasks, or every running task is idle
- **THEN** the shell does not enqueue spinner-driven redraws

#### Scenario: Spinner repaints while a task is actively running

- **WHEN** at least one running task is not idle
- **THEN** the shell enqueues periodic redraws to animate the spinner

### Requirement: Persistence interface abstraction

The shell SHALL consume persistence exclusively through a Store interface so that local (direct database) and remote (HTTP-backed) backends are interchangeable without changes to the rest of the TUI.

#### Scenario: Local backend satisfies the Store interface

- **WHEN** the application is built
- **THEN** the local database type satisfies the Store interface, enforced as a compile-time assertion

### Requirement: Theme palette and status icons

The shell SHALL define a single shared palette of colors, text styles, and status icons used across the TUI, including distinct colors and styles for each task status and a distinct color and icon for the "agent blocked on user prompt" state.

#### Scenario: Distinct styling per task status

- **WHEN** a task status (pending, in-progress, in-review, complete) is rendered
- **THEN** it is drawn with the status-specific color defined by the shared theme

#### Scenario: Needs-input state is visually distinct

- **WHEN** an idle task is blocked on a user prompt
- **THEN** it is rendered with the dedicated needs-input color and icon
