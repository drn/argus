# macos-app Specification

## Purpose

A native macOS SwiftUI application (Conductor-class: sidebar task rail + tabbed
detail pane) that drives the same argus daemon as the TUI and the web PWA,
speaking only the existing REST + SSE API on `:7743`. Ships as a pure SwiftPM
package (`ArgusKit` library + `ArgusMac` executable) with no Xcode project and
no new daemon endpoints.

## Requirements

### Requirement: App shell & task rail

The system SHALL present a sidebar task rail listing tasks grouped into
sections (active, in review, complete, archived), each row showing a status
icon and a needs-input indicator, and a detail pane with Terminal, Diff,
Files, and Info tabs for the selected task.

#### Scenario: Sections reflect task state

- **WHEN** the app loads the task list from `GET /api/tasks`
- **THEN** each task is placed into exactly one rail section (active / in
  review / complete / archived) matching its `status` and `archived` fields,
  and archived tasks are hidden unless the archived section is expanded

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
drive live task-list updates without polling, and SHALL raise a native macOS
notification plus increment a dock badge count when a `session.needs_input`
or `session.idle` event names a task that is not currently focused in the UI.

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
  produces the `ArgusMac` executable

#### Scenario: mac-app produces a launchable bundle

- **WHEN** `make mac-app` runs after a successful `mac-build`
- **THEN** it assembles `ArgusMac.app` with an ad-hoc code signature and the
  bundle launches from Finder or `open` without a Gatekeeper "unidentified
  developer" prompt requiring override

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
