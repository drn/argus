## MODIFIED Requirements

### Requirement: Events integration

The system SHALL consume `GET /api/events/stream` (named SSE events, resumed
from a since-cursor, with resync handling on cursor-invalid responses) to
drive live task-list updates without polling, and SHALL raise a native macOS
notification plus increment a dock badge count when a `session.needs_input`
or `session.idle` event names a task that is not currently focused in the UI.
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
