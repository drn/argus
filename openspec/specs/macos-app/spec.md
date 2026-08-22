# macos-app Specification

## Purpose

A native macOS SwiftUI application (Conductor-class: sidebar task rail + tabbed
detail pane) that drives the same argus daemon as the TUI and the web PWA,
speaking only the existing REST + SSE API on `:7743`. Ships as a pure SwiftPM
package (`ArgusKit` library + `Argus` executable) with no Xcode project and
no new daemon endpoints.
## Requirements
### Requirement: App shell & task rail

The system SHALL present a sidebar task rail listing non-archived tasks
grouped into per-project folder sections (mirroring the TUI task list's
project grouping and ordering), each row showing a status icon and a
needs-input indicator, with archived tasks in a separate collapsed section at
the bottom, and a detail pane with Terminal, Diff, Files, and Info tabs for
the selected task.

#### Scenario: Folders mirror the TUI task list

- **WHEN** the app loads the task list from `GET /api/tasks`
- **THEN** each non-archived task is placed under its project folder
  (folders sorted alphabetically, tasks in created order, empty project →
  "(no project)"), the row's status icon carries the task-state signal, and
  archived tasks are hidden in a collapsed bottom section (itself grouped by
  project) unless expanded

#### Scenario: Needs-input surfaces in the rail

- **WHEN** a task's live session is idle-gated into needs-input (per the
  daemon's existing idle detector, reflected in the task list response or a
  `session.needs_input` event)
- **THEN** the task's rail row shows a needs-input indicator without requiring
  the user to open the detail pane

#### Scenario: Detail tabs switch without losing terminal state

- **WHEN** the user switches from the Terminal tab to the Diff tab and back
- **THEN** the terminal view's scrollback and live connection are preserved
  (no PTY stream reconnect on tab switch)

### Requirement: Live terminal streaming

The system SHALL render a task's live PTY output by reading `GET
/api/tasks/{id}/output` for the `X-Output-Total` resume offset, then opening
`GET /api/tasks/{id}/stream` (SSE, base64-encoded frames) with `?since=` set
to that offset so replay has no gap and no overlap; it SHALL send keystrokes
via `POST /api/tasks/{id}/input`, propagate terminal resizes via `POST
/api/tasks/{id}/resize`, handle SSE `exit` events (carrying a `rerendering`
flag) and `clipboard` events, and reconnect the stream with exponential
backoff on drop.

#### Scenario: Resume with no gap or overlap

- **WHEN** the terminal view opens for a task with existing output
- **THEN** the app reads `X-Output-Total` from `/output`, opens `/stream`
  with `?since=<that value>`, and the rendered scrollback contains every byte
  exactly once — no duplicated frame, no missing frame

#### Scenario: Exit event triggers rerender, not disconnect

- **WHEN** the SSE stream emits an `exit` event with `rerendering: true`
- **THEN** the terminal view keeps the connection semantics consistent with a
  restart in place (re-renders from the daemon's replay) rather than treating
  it as a terminal failure requiring a user-visible error

#### Scenario: Stream drop reconnects with backoff

- **WHEN** the SSE connection drops (network blip, daemon bounce)
- **THEN** the app retries with exponential backoff and resumes from the
  last-seen offset via `?since=`, without duplicating already-rendered output

### Requirement: Task lifecycle over REST

The system SHALL support creating a task (project, backend, prompt, optional
name via `POST /api/tasks`), and stopping, restarting, resuming, archiving,
unarchiving, renaming, deleting, and forking an existing task via their
respective `/api/tasks/{id}/*` endpoints, with a confirmation prompt before
any destructive action (stop, delete).

#### Scenario: Create task with optional name

- **WHEN** the user submits the new-task form with project, backend, and
  prompt but no name
- **THEN** `POST /api/tasks` is called without a `name` field and the daemon's
  existing auto-naming applies; when a name is provided it is sent verbatim

#### Scenario: Destructive actions require confirmation

- **WHEN** the user chooses Stop or Delete on a task
- **THEN** the app shows a native confirmation dialog before issuing the
  corresponding `POST /api/tasks/{id}/stop` or `DELETE /api/tasks/{id}`
  request; a cancelled dialog issues no request

### Requirement: Git surfaces

The system SHALL provide a unified-diff viewer (`GET
/api/tasks/{id}/git/diff`), a git status view (`GET /api/tasks/{id}/git/status`),
a worktree file tree (`GET /api/tasks/{id}/files`), and PR/branch links per
task (`GET /api/tasks/{id}/links`).

#### Scenario: Diff view renders unified diff

- **WHEN** the user opens the Diff tab for a task with uncommitted changes
- **THEN** the app fetches `GET /api/tasks/{id}/git/diff` and renders the
  unified diff with added/removed line styling

#### Scenario: PR link surfaces from links endpoint

- **WHEN** a task has an open PR recorded by the daemon
- **THEN** `GET /api/tasks/{id}/links` supplies the PR URL and the Info tab
  renders it as a clickable link

### Requirement: Events integration

The system SHALL consume `GET /api/events/stream` (named SSE events, resumed
from a since-cursor, with resync handling on cursor-invalid responses) to
drive live task-list updates without polling, and — subject to the user's
notification preferences in Settings — SHALL raise a native macOS
notification plus increment a dock badge count when a `session.needs_input`
event names a task that is not currently focused in the UI, and SHALL raise a
native macOS notification when a `session.idle` event names a task that is
not currently focused in the UI. The needs-input notification preference
SHALL default to enabled (opt-out). The idle notification preference SHALL
default to disabled (opt-in) — idle events fire far more often than
needs-input across a fleet of concurrently-running tasks, so an enabled-by-
default idle notification floods the user; anyone who wants idle banners can
still turn them on in Settings.

To avoid flooding the user with one notification per task during a burst of
real needs-input transitions (e.g. catching up on an SSE reconnect backlog,
or several tasks legitimately hitting needs-input around the same time), the
system SHALL apply a client-side burst policy to needs-input notifications
specifically: while arrivals for distinct tasks stay under a small threshold
within a short rolling window, each posts its own notification immediately;
once that threshold is exceeded, further arrivals within the same burst
SHALL be coalesced into a single, updating summary notification instead of
one notification per task. This burst policy is layered on top of (and does
not replace) the existing per-task dedupe that prevents restacking a second
notification for a task that already has one pending.

#### Scenario: Task list updates without polling

- **WHEN** a task transitions status on the daemon (e.g. `in_progress` →
  `in_review`)
- **THEN** the events stream delivers the corresponding named event and the
  rail re-sorts/re-labels the task without the app issuing a fresh
  `GET /api/tasks` poll

#### Scenario: Needs-input raises a notification when unfocused

- **WHEN** a `session.needs_input` event names a task that is not the
  currently-selected task in the app
- **THEN** the app raises a native macOS notification and increments the dock
  badge count; no notification fires if that task is already focused

#### Scenario: Idle notifications are off by default

- **WHEN** the user has never changed the idle-notification setting in
  Settings
- **THEN** a `session.idle` event naming an unfocused task raises no native
  macOS notification

#### Scenario: Idle notifications fire once explicitly enabled

- **WHEN** the user turns on the idle-notification toggle in Settings
- **THEN** a subsequent `session.idle` event naming a task that is not
  currently focused in the UI raises a native macOS notification

#### Scenario: Resync on invalid cursor

- **WHEN** the events stream reports the client's since-cursor is no longer
  valid (evicted from the daemon's ring)
- **THEN** the app performs a full `GET /api/tasks` resync and resumes the
  events stream from the daemon's current cursor

#### Scenario: Sparse needs-input arrivals each post individually

- **WHEN** needs-input events for different tasks arrive spaced apart such
  that the burst threshold is never exceeded within the rolling window
- **THEN** each task's own native notification posts individually and
  immediately, with no added delay

#### Scenario: A burst of needs-input arrivals coalesces into one summary

- **WHEN** needs-input events for more distinct tasks than the burst
  threshold arrive within the rolling window (e.g. catching up on an SSE
  reconnect backlog)
- **THEN** the arrivals up to the threshold post their own notifications as
  usual, and every arrival beyond the threshold is coalesced into a single
  summary notification naming/counting the affected tasks instead of posting
  one notification each

### Requirement: Hera roster (read-only)

The system SHALL display the hera role roster for a task or orchestrator by
reading `GET /api/hera`, with no mutation actions (spawn worker, send
message, plan mutation) exposed in this app, matching the web app's current
scope.

#### Scenario: Roster renders without mutation controls

- **WHEN** the user opens the Hera tab for a task bound to an orchestrator
- **THEN** the app renders the role tree from `GET /api/hera` and presents no
  buttons or menu items that would spawn a worker, send a hera message, or
  mutate a plan node

### Requirement: Schedules and settings

The system SHALL list schedules and support running one immediately via
`GET /api/schedules` and `POST /api/schedules/{id}/run`, and SHALL provide a
settings surface for the server URL and auth token, defaulting to
`http://localhost:7743` and the token at `~/.argus/api-token`.

#### Scenario: Run-now triggers immediate execution

- **WHEN** the user selects "Run now" on a schedule row
- **THEN** the app issues `POST /api/schedules/{id}/run` and reflects the
  resulting task appearing in the rail

#### Scenario: Settings default to local daemon

- **WHEN** the app launches with no prior settings configured
- **THEN** it connects to `http://localhost:7743` using the token read from
  `~/.argus/api-token`, and the settings surface lets the user override either
  value for a remote daemon

### Requirement: Build & run without Xcode

The system SHALL build and test as a pure SwiftPM package (`swift build`,
`swift test`, wired to `make mac-build` / `make mac-test`), SHALL run
directly via `make mac-run`, and SHALL assemble a runnable, ad-hoc-codesigned
`.app` bundle via `make mac-app` — with no `.xcodeproj` or `.xcworkspace`
checked into the repo.

#### Scenario: Make targets build without Xcode project files

- **WHEN** `make mac-build` runs in an environment with only the Swift
  toolchain (no Xcode project generation step)
- **THEN** the build succeeds via `swift build --package-path macos` and
  produces the `Argus` executable

#### Scenario: mac-app produces a launchable bundle

- **WHEN** `make mac-app` runs after a successful `mac-build`
- **THEN** it assembles `Argus.app` with an ad-hoc code signature and the
  bundle launches from Finder or `open` without a Gatekeeper "unidentified
  developer" prompt requiring override

### Requirement: App branding

The system SHALL present the Argus shield/eye mark as the app's icon in the
Dock, Finder, and Cmd+Tab switcher, both for the packaged `.app` bundle (via
`Info.plist`'s `CFBundleIconFile`) and for the bare `swift run` executable
(via `NSApplication.shared.applicationIconImage`, set at launch from a
resource bundled with the SwiftPM target) — no user-visible surface SHALL
fall back to AppKit's generic executable icon.

#### Scenario: Packaged bundle carries the icon

- **WHEN** `make mac-app` assembles `Argus.app`
- **THEN** `Contents/Resources/AppIcon.icns` exists and `Info.plist`
  declares it via `CFBundleIconFile`, so Finder and the Dock show the Argus
  mark without launching the app

#### Scenario: Bare executable also shows the icon

- **WHEN** the app is launched via `swift run Argus` (no `.app` bundle)
- **THEN** the Dock icon shows the Argus mark rather than AppKit's generic
  fallback, because `AppDelegate.applicationDidFinishLaunching` sets
  `NSApplication.shared.applicationIconImage` from the bundled resource

### Requirement: Three-surface frontend parity policy

CLAUDE.md SHALL document a parity rule: any user-facing feature change must be
evaluated against all three frontends (TUI, web app, macOS app), where parity
is scoped to the REST-exposed surface, and any intentional gap between
frontends SHALL be recorded as an explicit named follow-up rather than left
undocumented.

#### Scenario: New REST-exposed feature triggers a parity check

- **WHEN** a change adds a new REST-exposed, user-facing capability (e.g. a
  new task action)
- **THEN** the change's proposal notes whether the TUI, web app, and macOS app
  each adopt it, and any frontend that does not gets an explicit follow-up
  reference rather than silent omission

#### Scenario: Non-REST capability is exempt

- **WHEN** a change is TUI-only by nature (e.g. a keybinding rebind) with no
  REST-exposed surface
- **THEN** the parity rule does not require web or macOS app changes, since
  parity is scoped to the REST-exposed surface only

### Requirement: Terminal input Ctrl+Z guard

The system SHALL remove the Ctrl+Z byte (`0x1A`, ASCII SUB) from all keyboard
input captured in the terminal surface before forwarding it to the daemon's
`POST /api/tasks/{id}/input` endpoint. All other bytes — including other control
characters such as Ctrl+C (`0x03`), Ctrl+Y (`0x19`), and ESC (`0x1B`) — SHALL be
forwarded unchanged and in their original order.

A literal Ctrl+Z keypress SHALL be swallowed (nothing is forwarded); the app
SHALL NOT remap Ctrl+Z to another action, because the SwiftUI surface has no
terminal pane-zoom / fullscreen affordance analogous to the TUI's Ctrl+Z remap.

This guard exists because Claude Code's CLI runs its own background-session
supervisor: a literal Ctrl+Z byte reaching it reparents the agent session out of
argus's process tree permanently, orphaning it. The TUI already guards the same
footgun by never forwarding Ctrl+Z to the PTY; this requirement establishes
parity for the macOS app.

The decision logic SHALL be a pure, dependency-free helper
(`ArgusKit.TerminalInput.sanitize`) so it is unit-testable without SwiftTerm or
AppKit, and the terminal input delegate (`Argus.TerminalCoordinator.send`)
SHALL call it at the single outbound chokepoint and SHALL log when a byte is
dropped.

#### Scenario: A lone Ctrl+Z keypress is swallowed

- **WHEN** the user presses Ctrl+Z while the terminal has focus and SwiftTerm
  delivers a `0x1A` byte to the input delegate
- **THEN** no bytes are forwarded to `POST /input` (the keystroke is dropped) and
  the agent session is neither suspended nor reparented out of argus's process
  tree

#### Scenario: Ctrl+Z embedded in a payload is stripped, the rest forwarded

- **WHEN** an outbound input payload contains a `0x1A` byte among other bytes
- **THEN** the `0x1A` byte(s) are removed and the remaining bytes are forwarded
  to `POST /input` unchanged and in their original order

#### Scenario: Other control bytes reach the PTY untouched

- **WHEN** the user presses a control key other than Ctrl+Z (e.g. Ctrl+C `0x03`,
  Ctrl+Y `0x19`, or ESC `0x1B`)
- **THEN** the corresponding byte is forwarded to `POST /input` unchanged — the
  guard is surgical to Ctrl+Z, not a blanket control-byte filter

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

