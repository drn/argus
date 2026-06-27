# Idle / Needs-Input Detection

## ADDED Requirements

### Requirement: Needs-input is detected for a never-idle session via content stability

The system SHALL flag a running session as waiting for user input even when it
never enters the idle set, provided the prompt signature is present in its recent
output AND its meaningful content is unchanged across consecutive detection ticks.
"Meaningful content" SHALL exclude animation/redraw chrome — spinner and timing
decoration lines, the rendered input/cursor prompt line, blank lines, and ANSI
escape sequences — and SHALL be robust to a session repainting the same frame a
varying number of times (e.g. an alt-screen prompt). This closes the gap where a
session parked at a prompt emits a steady trickle of redraw bytes that keep its
raw-output clock fresh, so it never goes idle and the idle-gated detector never
scans it.

A session whose meaningful content CHANGES between ticks SHALL NOT be flagged by
this pass: a still-streaming agent that transiently shows the prompt signature is
not blocked. The idle-gated detection and the sticky carry-forward pass remain
unchanged; this content-stability pass is additive.

#### Scenario: Never-idle session parked at a prompt is flagged once content is stable

- **WHEN** a running session shows the prompt signature and only its animation
  chrome (spinner, cursor blink, repaint) has changed since the previous tick
- **THEN** the system reports the agent is waiting for input

#### Scenario: Streaming session showing the signature transiently is not flagged

- **WHEN** a running session shows the prompt signature but its meaningful
  transcript content has changed since the previous tick
- **THEN** the system reports the agent is not waiting for input

#### Scenario: First observation records but does not flag

- **WHEN** a never-idle session showing the prompt signature is observed for the
  first time (no prior tick to compare against)
- **THEN** the system does not yet flag it, and records its content fingerprint
  so the next tick can compare

#### Scenario: Repaint count does not destabilize the decision

- **WHEN** a parked session's recent output contains the same static frame
  repainted a different number of times between ticks
- **THEN** the content is treated as stable and the agent is reported waiting for input

### Requirement: Content fingerprint excludes animation chrome and collapses repaint frames

The system SHALL expose a content fingerprint over a session's recent output that
is identical for two output tails differing only in animation/redraw chrome, and
different for tails differing in meaningful transcript content. The fingerprint
MUST strip ANSI sequences, drop spinner/timing decoration lines and the rendered
input/cursor prompt line, ignore blank lines, and de-duplicate repeated lines so
that repainted frames collapse rather than inflating or shifting the fingerprint.

#### Scenario: Animation-only difference fingerprints identically

- **WHEN** two snapshots of the same parked prompt differ only in the spinner
  glyph, the timing seconds, and cursor-positioning escapes
- **THEN** their content fingerprints are equal

#### Scenario: New transcript content fingerprints differently

- **WHEN** a later snapshot contains a new transcript line not present in the earlier one
- **THEN** their content fingerprints differ
