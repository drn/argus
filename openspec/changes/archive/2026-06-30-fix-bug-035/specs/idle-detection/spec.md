# Idle / Needs-Input Detection (delta)

## MODIFIED Requirements

### Requirement: Selection-prompt UI is recognized regardless of wording or surrounding markup

The system SHALL treat the agent's numbered-selection widget as a needs-input signal whenever the visible text shows the selection cursor immediately followed (with zero or more spaces/tabs) by ANY numbered option (a number and a period), not only the first option — so a permission cursor a user has navigated down to option 2 or 3 is still caught. Detection MUST be based on this shared UI shape, not on any specific prompt wording, so permission prompts, edit confirmations, plan-mode confirms, and open-ended multiple-choice questions are all caught. Detection MUST survive interleaved color/escape sequences and cursor-positioning codes that produce a visible gap without any literal space byte.

The system SHALL ALSO treat the AskUserQuestion chooser footer as a needs-input signal. The chooser renders plain options whose selection cursor does NOT follow the numbered-option shape, so the footer is the robust matcher: it is present regardless of which option is highlighted. Footer detection MUST match an Enter-action affordance and an Esc-action affordance on the SAME line, tolerant of the action wording (e.g. "select", "confirm", "choose"), letter case, and the navigation hints/separators (`·`, `↑/↓`) rendered between them. The two affordances appearing on SEPARATE lines, or either affordance alone, MUST NOT fire.

#### Scenario: Permission prompt with a numbered selection

- **WHEN** the recent output ends with a selection cursor followed by a numbered list of options
- **THEN** the system reports the agent is waiting for input

#### Scenario: Permission prompt with the cursor navigated to a later option

- **WHEN** the selection cursor sits on option 2 or 3 (not the first option)
- **THEN** the system reports the agent is waiting for input

#### Scenario: Open-ended question with a numbered selection but no fixed phrasing

- **WHEN** the selection widget appears without any "Do you want to" phrasing
- **THEN** the system reports the agent is waiting for input

#### Scenario: AskUserQuestion chooser footer fires regardless of highlighted option

- **WHEN** the recent output shows the chooser footer with an Enter-action affordance and an Esc-action affordance on one line, separated by navigation hints
- **THEN** the system reports the agent is waiting for input

#### Scenario: Selection markup split by color escapes

- **WHEN** the cursor and the first option are separated only by color escape sequences and a literal space
- **THEN** the system reports the agent is waiting for input

#### Scenario: Selection markup with cursor-positioning instead of a space

- **WHEN** the cursor and the first option are separated by a cursor-positioning escape and no literal space byte exists between them
- **THEN** the system reports the agent is waiting for input

#### Scenario: A bare selection cursor without a numbered option does not fire

- **WHEN** the output contains the selection cursor glyph but it is not followed by a numbered option
- **THEN** the system reports the agent is not waiting for input

#### Scenario: A plain numbered list without the selection cursor does not fire

- **WHEN** the output contains a numbered list with no selection cursor preceding the first item
- **THEN** the system reports the agent is not waiting for input

#### Scenario: A lone footer affordance or split footer does not fire

- **WHEN** the output contains only one of the two chooser affordances, or both on separate lines
- **THEN** the system reports the agent is not waiting for input

### Requirement: Needs-input is detected for a never-idle session via content stability

The system SHALL flag a running session as waiting for user input even when it
never enters the idle set, provided ONE of the following awaiting-input signals
is present in its recent (raw or emulated-screen) output AND its meaningful
content is unchanged across consecutive detection ticks:

1. An UNAMBIGUOUS selection-prompt signal — the numbered-selection cursor (on
   any option) or the chooser footer. This signal needs no further gating.
2. A FREE-TEXT trailing question (the last transcript line above the input
   prompt ends in a question mark) WHEN the agent's "working" affordance is
   ABSENT from the screen.

The agent's "working" affordance is the interrupt hint the agent renders WHILE
it is generating or executing (e.g. "esc to interrupt" / "ctrl+c to interrupt")
and REMOVES the moment it returns to the idle input prompt. Its ABSENCE is the
load-bearing discriminator for signal (2): a busy agent whose narration happens
to end in `?` and that briefly stalls on a spinner frame is content-stable AND
ends in `?`, so content stability ALONE is NOT a sufficient guard for the
free-text question — the working-affordance-absent gate is REQUIRED. When the
working affordance is present, the free-text question SHALL NOT be flagged by
this pass.

"Meaningful content" SHALL exclude animation/redraw chrome — spinner and timing
decoration lines, the rendered input/cursor prompt line, blank lines, and ANSI
escape sequences — and SHALL be robust to a session repainting the same frame a
varying number of times (e.g. an alt-screen prompt). This closes the gap where a
session parked at a prompt emits a steady trickle of redraw bytes that keep its
raw-output clock fresh, so it never goes idle and the idle-gated detector never
scans it — and the further gap (BUG-035) where a fullscreen agent parked at a
free-text question was caught by neither the idle pass (never idle) nor the
selection-only stability pass.

A session whose meaningful content CHANGES between ticks SHALL NOT be flagged by
this pass: a still-streaming agent that transiently shows a signal is not
blocked. The idle-gated detection and the sticky carry-forward pass remain
unchanged (they still honor the trailing-question heuristic behind the idle
gate); this content-stability pass is additive.

#### Scenario: Never-idle session parked at a selection prompt is flagged once content is stable

- **WHEN** a running session shows the selection-prompt signature and only its
  animation chrome (spinner, cursor blink, repaint) has changed since the
  previous tick
- **THEN** the system reports the agent is waiting for input

#### Scenario: Never-idle session parked at a free-text question with no working affordance is flagged

- **WHEN** a running session's last transcript line ends in a question mark, the
  agent's working affordance is absent from the screen, and only its animation
  chrome has changed since the previous tick
- **THEN** the system reports the agent is waiting for input

#### Scenario: Content-stable working agent ending in a question is not flagged

- **WHEN** a running session's meaningful content is stable across ticks and its
  last transcript line ends in a question mark, but the agent's "working"
  affordance (interrupt hint) is present on the screen
- **THEN** the content-stability pass does not flag it (the working agent is
  still generating, not awaiting input)

#### Scenario: Streaming session showing a signal transiently is not flagged

- **WHEN** a running session shows an awaiting-input signal but its meaningful
  transcript content has changed since the previous tick
- **THEN** the system reports the agent is not waiting for input

#### Scenario: First observation records but does not flag

- **WHEN** a never-idle session showing an awaiting-input signal is observed for
  the first time (no prior tick to compare against)
- **THEN** the system does not yet flag it, and records its content fingerprint
  so the next tick can compare

#### Scenario: Repaint count does not destabilize the decision

- **WHEN** a parked session's recent output contains the same static frame
  repainted a different number of times between ticks
- **THEN** the content is treated as stable and the agent is reported waiting for input
