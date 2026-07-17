## ADDED Requirements

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
shape is strong enough to escalate on its own). The consecutive-tick counter
resets to zero the moment either half of the combination stops holding
(selection signal disappears, or the working affordance appears) — a
transient or coincidental match that does not persist for the full window
SHALL NOT escalate.

#### Scenario: Selection prompt with no working affordance escalates after N ticks despite a non-converging fingerprint

- **WHEN** a running session shows the selection-prompt signature with the
  working affordance absent, continuously for at least N consecutive detection
  ticks, but unrelated per-tick-varying content elsewhere in the tail keeps its
  full content fingerprint from ever matching the previous tick's
- **THEN** the system reports the agent is waiting for input and classifies it
  content-idle once the escalation window elapses

#### Scenario: Transient selection-shape match does not escalate

- **WHEN** a running session's tail matches the selection-prompt shape (with
  the working affordance absent) for fewer than N consecutive ticks before the
  match stops holding — e.g. the agent scrolls a `❯ 1.`-looking line past
  while still genuinely generating
- **THEN** the system does not escalate, and the consecutive-tick counter
  resets

#### Scenario: Working affordance present never escalates

- **WHEN** a running session's tail matches the selection-prompt shape but the
  working affordance is present on any tick within the window
- **THEN** the consecutive-tick counter resets on that tick and escalation
  does not fire

#### Scenario: Escalation does not change fingerprint-converging behavior

- **WHEN** a running session's content fingerprint converges across two
  consecutive ticks (the existing content-stability match)
- **THEN** the session is flagged exactly as before, unaffected by the
  escalation path
