# TUI Shell

## ADDED Requirements

### Requirement: Status-bar transient notices auto-expire

The status bar SHALL display a transient notice set via `SetError` (rendered in
the error colour) or `SetInfo` (rendered dimmed) on its left side, taking
precedence over the default `N active  M pending  K done` task counts. The
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
- **THEN** the status bar renders the default `N active  M pending  K done` counts and no longer shows the error text

#### Scenario: Info notice reverts to task counts after the TTL

- **WHEN** an informational notice is set via `SetInfo` and `StatusNoticeTTL` has elapsed
- **THEN** the status bar renders the default task counts and no longer shows the info text

#### Scenario: Notice shows immediately and persists until the TTL

- **WHEN** a notice is set and less than `StatusNoticeTTL` has elapsed
- **THEN** the notice is rendered on the status bar's left side (error in the error colour, info dimmed)

#### Scenario: A new notice resets the expiry window

- **WHEN** a second notice is set partway through the first notice's TTL
- **THEN** the second notice remains visible for a full `StatusNoticeTTL` from when it was set, not from when the first notice was set
