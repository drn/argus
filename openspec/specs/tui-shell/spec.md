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

The shell SHALL expose three top-level tabs in order — Tasks, Hera, Settings — and SHALL switch among them in response to global numeric keys. Tab switching SHALL update both the header's active tab and the status bar's tab context, and SHALL move focus to the newly activated view. Plain Left/Right SHALL remain available to the active view rather than switching tabs. When the persisted `ui.cross_tab_arrows` option is enabled (the default), modified Left/Right (`ModCtrl` or `ModAlt`, representing Cmd+arrow) SHALL traverse one continuous chain: Tasks ⇄ Hera rail ⇄ Hera coordinator pane ⇄ Hera agent pane ⇄ Settings left pane ⇄ Settings right pane. Hops that enter a view from its left neighbor SHALL focus its leftmost pane; hops that enter from its right neighbor SHALL focus its rightmost present pane. Cmd+Left on Tasks and Cmd+Right on the Settings right pane SHALL be the chain's ends; the latter SHALL be consumed so it cannot alter a setting. The chain SHALL NOT fire in agent view or any modal mode, while the Tasks list, Hera rail, or a Settings inline edit holds the keyboard, or while a Hera pane is fullscreen.

The active tab SHALL persist locally across an argus restart and SHALL be restored on launch in place of always defaulting to Tasks, so a user who quits while on the Hera or Settings tab reopens on that same tab. In `--remote` mode, where there is no local persistence seam, the active tab SHALL NOT persist and the shell SHALL always start on the Tasks tab.

#### Scenario: Numeric keys select tabs by position

- **WHEN** the user presses `1`, `2`, or `3` while not in the agent view
- **THEN** the active tab becomes Tasks, Hera, or Settings respectively and that view is shown with focus

#### Scenario: Plain arrows remain local to the active view

- **WHEN** the user presses unmodified Left or Right in a top-level view
- **THEN** the shell does not switch tabs and the active view may handle the key

#### Scenario: Cmd+Right enters Hera from Tasks

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right on the Tasks list while it is not filtering
- **THEN** Hera becomes active with its rail focused

#### Scenario: Cmd+Left returns to Tasks from the Hera rail

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Left while Hera's rail is focused and not filtering
- **THEN** Tasks becomes active with its list focused

#### Scenario: Cmd+Right from Hera's rightmost pane enters Settings

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right while Hera's agent pane is focused (or its coordinator pane when the agent pane is absent) and no pane is fullscreen
- **THEN** Settings becomes active with its left pane focused

#### Scenario: Cmd+Left from Settings' left pane returns to Hera's rightmost pane

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Left while Settings' left pane is focused and not editing
- **THEN** Hera becomes active with its agent pane focused, or its coordinator pane when the agent pane is absent

#### Scenario: Settings panes step left and right

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right on Settings' left pane, or modified Left on its right pane
- **THEN** focus moves to the right or left pane respectively and the tab does not change

#### Scenario: Cmd+Right at the end of the chain is a no-op

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right while Settings' right pane is focused
- **THEN** nothing changes and the focused setting is not modified

#### Scenario: Disabled option preserves current behavior

- **WHEN** `ui.cross_tab_arrows` is disabled and the user presses any chain chord
- **THEN** the shell does not switch tabs or panes through the chain and the event continues to the active view

#### Scenario: Hera internal ladder is unchanged

- **WHEN** `ui.cross_tab_arrows` is enabled and modified Left or Right is pressed while a Hera pane is focused and the key is not a chain hop
- **THEN** the Hera focus ladder handles the key and the active tab does not change

#### Scenario: Fullscreen suppresses the chain

- **WHEN** `ui.cross_tab_arrows` is enabled and a Hera pane is fullscreen
- **THEN** modified Left/Right behave exactly as they do without the chain and the active tab does not change

#### Scenario: Filters and inline edits suppress the chain

- **WHEN** `ui.cross_tab_arrows` is enabled and the Tasks list or Hera rail is filtering, or a Settings inline edit is active
- **THEN** the active tab and focus do not change and the filter or edit field keeps control of the key

#### Scenario: Switching to a tab refreshes its content

- **WHEN** the user switches to the Hera tab or the Settings tab
- **THEN** the shell refreshes that view's content before showing it

#### Scenario: Last active tab is restored on launch

- **WHEN** the user quits argus while the Hera tab is active and then relaunches argus
- **THEN** the shell opens directly on the Hera tab, refreshed, with focus moved to it — the same outcome pressing `2` would produce

#### Scenario: First run with no persisted tab defaults to Tasks

- **WHEN** no tab state has ever been persisted (first run) or argus is running in `--remote` mode
- **THEN** the shell opens on the Tasks tab

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

The shell SHALL drive spinner repaints only while a spinner glyph actually rendered on screen right now would animate, and SHALL suppress periodic spinner repaints otherwise. On the base Tasks-tab view, this SHALL be scoped to the currently visible (filtered, on-screen) task row set — a running-but-idle or running-but-filtered-out task SHALL NOT keep the periodic redraw alive. Every other mode (the Hera tab, the fullscreen agent view, any modal/picker) SHALL fall back to the fleet-wide check (any running task anywhere is not idle), unchanged from prior behavior. The gate MAY lag a tab switch by up to one tick; this staleness SHALL be bounded and self-healing.

#### Scenario: No spinner repaints when all tasks are idle

- **WHEN** there are no running tasks, or every running task is idle
- **THEN** the shell does not enqueue spinner-driven redraws

#### Scenario: Spinner repaints while a visible task is actively running (Tasks tab)

- **WHEN** the Tasks tab is active and at least one task in its currently visible row set is running and not idle
- **THEN** the shell enqueues periodic redraws to animate the spinner

#### Scenario: A running-but-hidden task does not keep the Tasks-tab spinner alive

- **WHEN** the Tasks tab is active and the only running-and-not-idle task is hidden from the visible row set (filtered out, or a hera-managed task with hera-managed tasks hidden)
- **THEN** the shell does not enqueue spinner-driven redraws for that task

#### Scenario: Non-Tasks-tab modes keep the original fleet-wide check

- **WHEN** the Hera tab, the fullscreen agent view, or any modal/picker is active
- **THEN** the shell's redraw gate considers every running-and-not-idle task in the whole fleet, regardless of what is or isn't rendered on screen, unchanged from prior behavior

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

### Requirement: Status bar summarizes every task

When no transient notice is displayed, the status bar SHALL render counts for
`active`, `pending`, `review`, and `done` from the full task snapshot. Each
persisted task, including an archived task, SHALL contribute to exactly one
count according to its stored status: `in_progress` to `active`, `pending` to
`pending`, `in_review` to `review`, and `complete` to `done`. An `in_progress`
task SHALL count as active independently of the running-session snapshot.

#### Scenario: Mixed statuses and archived tasks

- **WHEN** the task snapshot contains tasks in all four statuses, including an archived task
- **THEN** the status bar shows all four counts and their sum equals the number of tasks in the snapshot

#### Scenario: Session liveness does not change the status count

- **WHEN** an `in_progress` task is absent from the running-session snapshot
- **THEN** it contributes to the active count until its stored status changes

#### Scenario: Transient notice expires

- **WHEN** a transient notice expires
- **THEN** the status bar returns to the four-status task summary

#### Scenario: Count summary competes with key hints

- **WHEN** the terminal width cannot fit the full count summary and every key hint
- **THEN** the status bar keeps the complete count summary, omits whole intermediate hints, and retains the final help or quit hint when it fits

### Requirement: Status-bar transient notices auto-expire

The status bar SHALL display a transient notice set via `SetError` (rendered in
the error colour) or `SetInfo` (rendered dimmed) on its left side, taking
precedence over the default `N active  M pending  R review  K done` task counts. The
notice SHALL appear immediately when set. Each notice SHALL auto-expire after a
fixed time-to-live (`StatusNoticeTTL`, 15 seconds) measured from when it was
set, after which the status bar SHALL revert to its default task counts WITHOUT
any user input or explicit clear call. Setting a new notice SHALL reset the
expiry window so the fresh notice always receives a full TTL and notices never
clear early. The revert SHALL be realized during a normal redraw via tcell's
per-cell diff and SHALL NOT force a full-screen `screen.Sync()`; the
application's periodic redraw guarantees the revert is painted within roughly
one tick of expiry even on an otherwise-static screen. Explicit `ClearError` /
`ClearInfo` SHALL still clear immediately.

#### Scenario: Error notice reverts to task counts after the TTL

- **WHEN** an error notice is set via `SetError` and `StatusNoticeTTL` has elapsed without any clear call
- **THEN** the status bar renders the default `N active  M pending  R review  K done` counts and no longer shows the error text

#### Scenario: Info notice reverts to task counts after the TTL

- **WHEN** an informational notice is set via `SetInfo` and `StatusNoticeTTL` has elapsed
- **THEN** the status bar renders the default task counts and no longer shows the info text

#### Scenario: Notice shows immediately and persists until the TTL

- **WHEN** a notice is set and less than `StatusNoticeTTL` has elapsed
- **THEN** the notice is rendered on the status bar's left side (error in the error colour, info dimmed)

#### Scenario: A new notice resets the expiry window

- **WHEN** a second notice is set partway through the first notice's TTL
- **THEN** the second notice remains visible for a full `StatusNoticeTTL` from when it was set, not from when the first notice was set

### Requirement: Startup requires a controlling terminal

The shell SHALL verify that a real controlling terminal is available before constructing its tcell screen. If no controlling terminal is available, `Run()` SHALL return a clear, descriptive error and SHALL NOT proceed to construct or configure a tcell screen.

#### Scenario: No controlling terminal available

- **WHEN** the application is launched with no controlling terminal (e.g. a detached or headless process)
- **THEN** `Run()` returns an error describing the missing terminal, and the application never constructs a tcell screen or calls `EnableMouse`/`EnablePaste`

#### Scenario: A controlling terminal is available

- **WHEN** the application is launched from a normal interactive terminal
- **THEN** the terminal check succeeds and the application proceeds to construct its tcell screen exactly as before

### Requirement: Status bar surfaces cached usage-probe readings

The status bar SHALL permanently render the daemon's cached Claude weekly-usage and Codex usage percentages alongside the default task-count summary (e.g. `cla 42% · cdx 76%`), regardless of whether anything currently needs operator attention. A reading that is absent (never probed) or stale (older than that probe's own staleness window) SHALL render as a literal `—`, visually distinct from a percentage, rather than a number that could be hours old and indistinguishable from a fresh one. The TUI SHALL poll the daemon for these readings on a fixed interval (`usageRecheckInterval`) no more frequent than the daemon's own probe cadence requires, reusing the daemon-connection source its periodic binary-skew recheck already holds.

In `--remote` mode and the in-process-runner fallback (no daemon connection to poll), both sides SHALL permanently render as unknown (`—`) rather than erroring or blocking — there is no daemon-process-local cache to ask in either mode.

#### Scenario: Fresh readings on both sides render as percentages

- **WHEN** the daemon reports both the Claude and Codex usage readings as fresh
- **THEN** the status bar renders both as percentages, e.g. `cla 42% · cdx 76%`

#### Scenario: A stale or never-probed reading renders as unknown, not a percentage

- **WHEN** the daemon reports a reading as absent or stale for either side
- **THEN** that side of the status bar renders `—`, never a percentage, even if a previous percentage had been displayed

#### Scenario: The readout does not depend on daemon-side logging

- **WHEN** the daemon's own probe-related log lines are structurally unobservable (the daemon process never initializes its debug logger)
- **THEN** the status bar readout is unaffected, since it is relayed directly from the daemon's in-memory cache via RPC, not derived from logs

#### Scenario: No daemon connection leaves both sides unknown

- **WHEN** the TUI is running in `--remote` mode or the in-process-runner fallback, with no local daemon connection to poll
- **THEN** both sides of the readout permanently render `—`

