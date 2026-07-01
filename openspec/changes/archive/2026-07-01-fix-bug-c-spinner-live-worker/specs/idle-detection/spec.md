# Idle / Needs-Input Detection

## ADDED Requirements

### Requirement: A live Hera role's working spinner is gated on a running, non-idle session, not bound-task status

The system SHALL animate the Hera rail working spinner for ANY role that holds a LIVE binding whose session is RUNNING and NOT idle, REGARDLESS of that role's bound-task workflow status. The activity predicate (`RoleView.IsActive`) that sources the spinner MUST be liveness AND session-running AND not-session-idle; the bound task being `in_progress` MUST NOT be an additional gate. A hera worker deliberately remains in `in_review` (with `meta:hera.ready_to_close` set) while its session lingers alive for the coordinator to close it out (#707); if that still-alive worker keeps producing output it MUST animate the spinner, not fall through to the static review glyph.

This is the display sibling of the needs-input un-gating: the working spinner was the last rail signal still gated on task status.

The two stale-session concerns the task-status gate previously guarded SHALL be preserved without it:

- A stopped / dead / days-old session MUST NOT spin. A hera binding does NOT end when its agent session exits — bindings end only on task-delete, reparent, detach, or the daemon-startup missing-task sweep — so a dead worker's role stays `Live` with its task row lingering, and liveness ALONE cannot exclude it. The protection is therefore the SESSION-RUNNING signal (the per-tick running set): a dead session is absent from it, so the role is not active. (Gating on liveness alone would spin a dead worker, since a dead session is neither in the running set nor in the idle set.)
- A parked continuously-repainting (fullscreen) agent MUST NOT spin forever. It is protected by the content-aware idle signal: the session-idle set unions raw-byte idle with the content-idle classification (a stable emulated screen with the "working" affordance absent), so a live-but-idle role is not active. The idle set is a subset of the running set, so "running AND not idle" is exactly "running and producing output".

The coordinator status LABEL derived from this predicate SHALL follow the same contract: a stale `working` role-status backed by a live, running, content-active session honestly reads "working" regardless of task status; a live-but-session-idle one reads "live"; a live-but-not-running (dead, binding lingering) one reads "live"; a role with no live binding reads "stopped".

#### Scenario: Live worker in in_review with a running session animates the spinner

- **WHEN** a hera worker's bound task has rolled to `in_review` (its binding still live), its session is running, and it is not idle (actively producing output)
- **THEN** the role is active and the rail renders the animated working spinner, advancing with the frame counter

#### Scenario: Live but session-idle role does not spin

- **WHEN** a role holds a live binding and a running session but the session is in the content-aware idle set (a parked fullscreen agent, content stable)
- **THEN** the role is not active and the rail renders a static glyph, for any bound-task status

#### Scenario: Live but not-running role (dead worker, binding lingering) does not spin

- **WHEN** a role's session has exited (dropped from the running set) but its binding still lingers (`Live` remains true because bindings do not end on session exit), even with a stale `working` status and an `in_review` or `in_progress` task
- **THEN** the role is not active and the rail renders a static glyph

#### Scenario: Live active coordinator in in_review labels "working"

- **WHEN** a coordinator holds a live, running, non-idle binding with a stale `working` role-status and a bound task in `in_review`
- **THEN** the coordinator status label reads "working" (with the terminal task state appended), not "live"
