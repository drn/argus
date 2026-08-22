## MODIFIED Requirements

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
