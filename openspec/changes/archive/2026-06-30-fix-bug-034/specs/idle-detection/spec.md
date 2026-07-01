# Idle / Needs-Input Detection (delta)

## ADDED Requirements

### Requirement: Needs-input flag clears on user input or archive, never on signal decay

The system SHALL keep a session flagged as waiting for user input — including a
flag raised by the trailing-question heuristic (the last transcript line ends in
`?`) — for as long as the signal remains present, with NO time-based or
idle-based decay. The flag SHALL clear only when EITHER (a) the user delivers new
input to that session after the flag was raised, OR (b) the session's task is
archived. Input delivered to a DIFFERENT session SHALL NOT clear this one.

Clear-on-input SHALL be deterministic and MUST NOT depend on the prompt or
question text scrolling out of the recent-output tail: the system records, per
flagged task, the session's last-input timestamp observed when the task first
entered the needs-input set, and removes the task from the set — and suppresses
re-adding it on the same tick — once the session's last-input timestamp advances
past that recorded baseline, even if the question text still matches in the tail.
The recorded baseline SHALL be dropped when the task leaves the set (its signal
disappears), so that a fresh question raised after the user's response re-arms
the flag.

Clear-on-archive SHALL remove an archived task from the needs-input set
regardless of its detection signal, so it stops surfacing `?` and stops rolling
up to ancestor coordinators.

This clear logic SHALL be applied identically by the daemon-side detector and
the TUI-side detector. The trailing-question entry heuristic, the idle gate, the
sticky carry-forward pass, and the content-stability / emulated-screen guards
are unchanged; this requirement governs only when an already-detected signal is
removed from the published set.

#### Scenario: Free-text question is flagged and persists indefinitely without input

- **WHEN** an agent ends a turn on a free-text question and no input is delivered
  to its session across many detection ticks
- **THEN** the system keeps reporting the agent is waiting for input on every
  tick (no time-based or idle-based decay)

#### Scenario: User input clears the flag even while the question still matches the tail

- **WHEN** a session is flagged waiting for input and the user then delivers new
  input to that session, and the question text still matches in the recent-output
  tail
- **THEN** the system removes the session from the needs-input set on the next
  tick and does not re-add it while the same input remains the latest

#### Scenario: Input to a different session does not clear this one

- **WHEN** a session A is flagged waiting for input and the user delivers input
  only to a different session B
- **THEN** session A remains flagged waiting for input

#### Scenario: Archiving a flagged task clears its flag

- **WHEN** a session is flagged waiting for input and its task is archived
- **THEN** the system removes it from the needs-input set regardless of its
  detection signal

#### Scenario: A fresh question after a response re-arms the flag

- **WHEN** a session's flag was cleared by user input, the agent then produces
  output that no longer shows any needs-input signal, and later ends a new turn
  on another question
- **THEN** the system reports the agent is waiting for input again
