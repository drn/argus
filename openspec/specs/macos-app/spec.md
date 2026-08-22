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

