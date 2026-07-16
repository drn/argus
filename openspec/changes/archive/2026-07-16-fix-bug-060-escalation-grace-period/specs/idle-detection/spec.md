# Idle / Needs-Input Detection

## MODIFIED Requirements

### Requirement: Bounded consecutive-tick escalation when the fingerprint never converges

The system SHALL bound the worst-case delay before a genuinely parked session
is recognized, for the case where its content fingerprint never converges
tick-to-tick (e.g. unrelated per-tick-varying text elsewhere in the tail keeps
shifting the fingerprint even though the agent is actually parked at a prompt).

Both the content-stability needs-input pass and the content-idle
classification SHALL apply this escalation: if the RAW tail (or, for an
alt-screen session, the emulated screen) shows the UNAMBIGUOUS
selection-prompt signal with the "working" affordance ABSENT, and that exact
combination — signal present, working affordance absent — holds continuously
across a bounded number of consecutive detection ticks (a named constant in
the 5-10 tick range, tuned to trade off worst-case delay against
false-positive risk), the session SHALL be treated as needs-input /
content-idle even though its full-tail content fingerprint has not held
steady across those same ticks.

This escalation is a SEPARATE path layered on top of the existing 2-tick
fingerprint-convergence match — it does not alter fingerprinting itself, does
not loosen the shared chrome-recognition allowlist
(`fingerprintVolatileLine` / `decorationLine`), and does not apply to the
free-text trailing-question signal (only the unambiguous selection-prompt
shape is strong enough to escalate on its own).

A single non-qualifying tick that interrupts an otherwise-ongoing streak SHALL
be held in a one-tick GRACE period rather than immediately discarding the
streak: a genuinely, continuously-parked session can still produce an
isolated single-tick miss (the agent's own fullscreen redraw can blink the
selection-cursor glyph off for one frame as an animation, or a read of the
session's recent output can occasionally land on a partial/torn frame mid
redraw, independent of whether the agent is actually parked). The very next
tick either resumes the streak in full — the interruption was transient — or,
if it ALSO fails to qualify, confirms a genuine break: the consecutive-tick
counter resets to zero only after TWO CONSECUTIVE non-qualifying ticks. A
session already past the escalation threshold SHALL remain flagged
needs-input / content-idle through a single grace-held miss — it does not
flicker the indicator off for that one tick and back on the next.

A transient or coincidental match that does not hold for the full escalation
window SHALL still not escalate: a busy/streaming session showing the
selection shape for only an isolated tick or two, surrounded by non-qualifying
ticks, never accumulates enough consecutive credit to reach the threshold —
each such isolated match is itself surrounded by two or more non-qualifying
ticks, which confirm the non-parked state before any credit could build past
one or two ticks.

Derived from: `internal/agent/needsinput.go` (`EscalateParkedSelection`,
`NeedsInputEscalationTicks`), `internal/tui/app.go`
(`detectNeedsInputSticky`'s escalation persistence), `internal/agent/needsinput.go`
(`ContentIdle`'s escalation persistence).

#### Scenario: Selection prompt with no working affordance escalates after N ticks despite a non-converging fingerprint

- **WHEN** a running session shows the selection-prompt signature with the
  working affordance absent, continuously for at least N consecutive detection
  ticks, but unrelated per-tick-varying content elsewhere in the tail keeps its
  full content fingerprint from ever matching the previous tick's
- **THEN** the system reports the agent is waiting for input and classifies it
  content-idle once the escalation window elapses

#### Scenario: An isolated single-tick miss does not reset an ongoing streak (BUG-060)

- **WHEN** a running session's tail has matched the selection-prompt shape
  (with the working affordance absent) for one or more consecutive ticks, then
  misses on exactly one tick (the selection shape momentarily absent, or the
  working affordance momentarily present), then matches again on the very next
  tick
- **THEN** the streak resumes in full from where it left off — it is NOT
  discarded and does not restart from one

#### Scenario: An already-escalated session stays flagged through a single grace-held miss (BUG-060)

- **WHEN** a running session has already reached the escalation threshold and
  its next tick is an isolated miss, immediately followed by a tick that
  matches again
- **THEN** the session remains reported as needs-input / content-idle
  throughout — it does not visibly clear and re-flag across that single miss

#### Scenario: Two consecutive misses confirm a genuine break

- **WHEN** a running session's tail fails to match the selection-prompt shape
  (with the working affordance absent) for TWO OR MORE CONSECUTIVE ticks —
  e.g. the agent resumes genuinely generating, or the selection widget
  scrolls out of view for a sustained period
- **THEN** the consecutive-tick counter resets to zero for real, and any
  future streak must accumulate from scratch

#### Scenario: Sparse isolated matches amid an otherwise-busy session never escalate

- **WHEN** a running session's tail matches the selection-prompt shape only
  occasionally (e.g. the agent scrolls a `❯ 1.`-looking line past while still
  genuinely generating), with two or more non-qualifying ticks between each
  isolated match
- **THEN** the system does not escalate — each isolated match is surrounded
  by enough non-qualifying ticks to confirm the session is not genuinely
  parked before any meaningful credit could accumulate

#### Scenario: Escalation does not change fingerprint-converging behavior

- **WHEN** a running session's content fingerprint converges across two
  consecutive ticks (the existing content-stability match)
- **THEN** the session is flagged exactly as before, unaffected by the
  escalation path
