# Idle / Needs-Input Detection

## Purpose

When an agent session stops generating and waits for the user, the orchestrator must recognize that the session is blocked so the UI can surface it and so destructive maintenance actions never silently dismiss a pending question. This capability defines how recent agent output is inspected to decide whether the agent is waiting on user input, and how that decision gates a width-driven session restart ("rerender kick") that would otherwise discard an in-flight prompt.

## Requirements

### Requirement: Needs-input detection from recent output

The system SHALL determine whether an agent is blocked waiting for the user by inspecting only the most recent window of its output. Detection MUST fire on either of two signals: the agent's numbered-selection prompt UI, or the agent's last visible transcript line ending with a question mark. Empty output SHALL never be treated as needs-input.

#### Scenario: Empty buffer is not blocked

- **WHEN** the output buffer is empty
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Plain output is not blocked

- **WHEN** the recent output contains only ordinary work output with no selection prompt and no trailing question
- **THEN** the system reports the agent is not waiting for input

### Requirement: Selection-prompt UI is recognized regardless of wording or surrounding markup

The system SHALL treat the agent's numbered-selection widget as a needs-input signal whenever the visible text shows the selection cursor immediately followed (with zero or more spaces/tabs) by the first numbered option. Detection MUST be based on this shared UI shape, not on any specific prompt wording, so permission prompts, edit confirmations, plan-mode confirms, and open-ended multiple-choice questions are all caught. Detection MUST survive interleaved color/escape sequences and cursor-positioning codes that produce a visible gap without any literal space byte.

#### Scenario: Permission prompt with a numbered selection

- **WHEN** the recent output ends with a selection cursor followed by a numbered list of options
- **THEN** the system reports the agent is waiting for input

#### Scenario: Open-ended question with a numbered selection but no fixed phrasing

- **WHEN** the selection widget appears without any "Do you want to" phrasing
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

### Requirement: Trailing-question detection is anchored to the input prompt box

The system SHALL treat the agent as waiting for input when the last non-blank line of the transcript above the rendered input prompt box ends with a question mark (ASCII `?` or full-width `？`). The search MUST be anchored to the prompt-box opener so that hint lines rendered below the box are excluded. Blank lines between the transcript and the prompt box MUST be skipped. When no prompt box is present in the recent output, the trailing-question signal SHALL NOT fire.

#### Scenario: Question above the prompt box fires

- **WHEN** the last content line above the input prompt box ends with a question mark
- **THEN** the system reports the agent is waiting for input

#### Scenario: Statement above the prompt box does not fire

- **WHEN** the last content line above the input prompt box does not end with a question mark
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Full-width question mark fires

- **WHEN** the last content line above the prompt box ends with a full-width question mark
- **THEN** the system reports the agent is waiting for input

#### Scenario: Hint lines below the prompt box are ignored

- **WHEN** a statement sits above the prompt box and only a hint line containing a question mark sits below it
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Trailing whitespace after the question mark still fires

- **WHEN** the last content line ends with a question mark followed by trailing whitespace
- **THEN** the system reports the agent is waiting for input

#### Scenario: Blank lines between transcript and prompt box are skipped

- **WHEN** multiple blank lines separate the trailing question from the prompt box
- **THEN** the system reports the agent is waiting for input

#### Scenario: No prompt box present skips the question heuristic

- **WHEN** the output contains a trailing question but no rendered input prompt box
- **THEN** the system reports the agent is not waiting for input

### Requirement: Detection scans only a bounded recent tail

The system SHALL only inspect a bounded window at the end of the output buffer. Signals appearing only in output older than this window SHALL NOT be detected, while a signal landing at the very end of the buffer MUST be detected even when far more older output precedes it.

#### Scenario: Signal at the end of a large buffer is detected

- **WHEN** the selection prompt appears at the very end of a buffer larger than the scan window
- **THEN** the system reports the agent is waiting for input

#### Scenario: Signal older than the scan window is not detected

- **WHEN** the selection prompt appears only at the start of a buffer and is followed by more output than the scan window
- **THEN** the system reports the agent is not waiting for input

### Requirement: Session-level blocked check over the live output ring

The system SHALL expose a session-level check that reports whether a given session is blocked on a user prompt by applying needs-input detection to that session's recent output tail. A nil session SHALL never be reported as blocked. This check is intended to be paired with an idle check by the caller, because a prompt the agent is still streaming past is not blocking.

#### Scenario: Nil session is not blocked

- **WHEN** the blocked check is given no session
- **THEN** the system reports the session is not blocked

#### Scenario: Session with plain output is not blocked

- **WHEN** a session's recent output contains only ordinary work output
- **THEN** the system reports the session is not blocked

#### Scenario: Session showing a selection overlay is blocked

- **WHEN** a session's recent output shows the numbered-selection prompt UI
- **THEN** the system reports the session is blocked

### Requirement: Rerender kick is gated on idle and needs-input state

The system SHALL decide whether to stop-and-resume a session to re-render its scrollback at a new terminal width based on, in order: whether a kick is possible at all, whether the width change is large enough to matter, whether the agent is idle, and whether the agent is blocked on a user prompt. A kick SHALL be skipped entirely when there is no session to resume or a kick is already in flight. A kick SHALL be skipped when the width change is below the configured margin. When the change is large enough but the agent is not idle, the decision SHALL be to defer because the agent is busy. When the change is large enough and the agent is idle but blocked on a user prompt, the decision SHALL be to defer because of the prompt, so that resuming the session does not silently dismiss the pending question. Only when all gates pass SHALL the decision be to kick.

#### Scenario: No session to resume skips the kick

- **WHEN** there is no session to resume (or a kick is already pending)
- **THEN** the decision is to skip

#### Scenario: Below-margin width change skips the kick

- **WHEN** the difference between the panel width and the session's initial width is smaller than the margin
- **THEN** the decision is to skip

#### Scenario: Busy agent defers the kick

- **WHEN** the width change exceeds the margin but the agent is not idle
- **THEN** the decision is to defer because the agent is busy

#### Scenario: Idle agent blocked on a prompt defers the kick

- **WHEN** the width change exceeds the margin and the agent is idle but is blocked on a user prompt
- **THEN** the decision is to defer because of the prompt

#### Scenario: All gates pass triggers the kick

- **WHEN** the width change exceeds the margin, the agent is idle, and it is not blocked on a prompt
- **THEN** the decision is to stop and resume the session

### Requirement: Width-margin threshold treats unknown initial width as sane

The system SHALL compute whether a width change is large enough to justify a kick from the absolute difference between the panel width and the session's recorded initial width, using a fixed minimum margin. An unknown initial width (reported as zero or less) SHALL be treated as already sane so that it never triggers a surprise restart.

#### Scenario: Unknown initial width never exceeds the threshold

- **WHEN** the session's initial width is unknown
- **THEN** the system reports the margin threshold is not exceeded

#### Scenario: Width difference at or above the margin exceeds the threshold

- **WHEN** the absolute difference between panel width and initial width is at least the margin
- **THEN** the system reports the margin threshold is exceeded
