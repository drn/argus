# Idle / Needs-Input Detection

## ADDED Requirements

### Requirement: Content-aware idle for continuously-repainting (fullscreen) agents

The system SHALL recognize a session as idle even when its raw PTY output never
quiesces, provided its VISIBLE screen has stopped changing. A fullscreen
(alt-screen) agent parked at its prompt emits continuous repaint/animation bytes
(cursor blink, spinner timing line, alt-screen redraws), so a raw-byte idle
clock never fires for it. The system SHALL therefore compute a "content-idle"
signal: a running session that is NOT already raw-idle is content-idle when its
animation-stripped EMULATED-screen fingerprint has been UNCHANGED for at least
the idle threshold AND the agent's "working" affordance (the interrupt hint Claude
renders while generating, e.g. "esc to interrupt") is ABSENT.

The fingerprint MUST be taken over the reconstructed (vt-emulated) screen, not
the raw byte stream, because a fullscreen agent's raw bytes never stabilize while
it repaints. The "working"-affordance gate is load-bearing: content stability
ALONE is not sufficient (a busy agent stalled on a spinner frame for a tick is
content-stable yet still working), so a session showing the interrupt affordance
MUST NOT be treated as content-idle regardless of stability. Computation MUST run
off the hot paint path (on the periodic watcher/TUI tick), reusing the shared
screen-renderer and fingerprint machinery — no second emulator path.

Content-idle is an ADDITIVE signal combined with the existing raw-byte idle
classification; a session that already quiesces (a non-fullscreen agent) is
classified IDENTICALLY to before. The session's own raw-byte `IsIdle()`
predicate is unchanged.

#### Scenario: Parked fullscreen agent becomes content-idle

- **WHEN** a running session that never reaches the raw-idle set shows a stable
  emulated screen (only spinner/cursor animation changes) with no "working"
  affordance, across at least the idle threshold
- **THEN** the system classifies it as content-idle

#### Scenario: Working fullscreen agent is not content-idle

- **WHEN** a running session's emulated screen shows the "working" affordance
  ("esc to interrupt"), even if the rest of the screen is momentarily stable
- **THEN** the system does NOT classify it as content-idle

#### Scenario: Streaming fullscreen agent is not content-idle

- **WHEN** a running session's emulated screen content changes from one tick to
  the next
- **THEN** the system does NOT classify it as content-idle (the stability timer
  resets on every content change)

#### Scenario: Already-idle and non-fullscreen sessions are unaffected

- **WHEN** a session is already raw-idle
- **THEN** the content-idle pass skips it (it is already idle) and a
  non-fullscreen agent that quiesces is classified exactly as before

### Requirement: Idle-push fires once on the content-idle transition

The system SHALL fold the content-idle set into the idle set used for the
busy→idle idle-push transition and the `session.idle` event, so a fullscreen
agent that goes content-idle fires an idle notification. Firing MUST remain
exactly-once per work cycle: after a push fires for a task, no further push fires
for it until new input arrives, so a content-idle signal that flaps (or is
re-asserted every tick while the agent stays parked) MUST NOT produce repeated
pushes. Non-fullscreen agents — already in the raw-idle set — MUST see no change
in idle-push behavior.

#### Scenario: Fullscreen content-idle fires one push

- **WHEN** a fullscreen session that received user input goes content-idle and
  remains content-idle across many ticks
- **THEN** the system fires exactly one idle push for that work cycle

#### Scenario: No push without an input cycle

- **WHEN** a session goes content-idle but no input has ever been delivered to it
- **THEN** the system fires no idle push (the input-presence gate is unchanged)
