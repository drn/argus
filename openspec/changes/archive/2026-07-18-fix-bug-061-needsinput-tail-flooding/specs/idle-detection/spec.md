## MODIFIED Requirements

### Requirement: Detection scans only a bounded recent tail

The system SHALL only inspect a bounded window at the end of the output buffer. Signals appearing only in output older than this window SHALL NOT be detected, while a signal landing at the very end of the buffer MUST be detected even when far more older output precedes it.

When the fixed-size window at the very end of the buffer is dominated by a long run of some short byte sequence repeating many times in place (e.g. an agent's blinking cursor/status-glyph redraw, which can recur indefinitely even while the agent is genuinely parked awaiting input), the system SHALL expand its read backward, in bounded steps up to a hard ceiling, until it finds a window containing real (non-repeating) content, or the ceiling is reached, or the underlying source is exhausted. The final window handed to detection SHALL be anchored on the last real content found this way (not on the literal end of the buffer) and SHALL still be no larger than the base window size. This closes the gap where a signal that once appeared inside the window becomes PERMANENTLY unreachable — not merely delayed — once enough repeating redraw output accumulates after it, since a flat "last N bytes" cut has no way to recover content that has scrolled out.

A buffer whose window is NOT dominated by such a repeating run SHALL be scanned exactly as before, with no expansion and no added cost.

#### Scenario: Signal at the end of a large buffer is detected

- **WHEN** the selection prompt appears at the very end of a buffer larger than the scan window
- **THEN** the system reports the agent is waiting for input

#### Scenario: Signal older than the scan window is not detected

- **WHEN** the selection prompt appears only at the start of a buffer and is followed by more output than the scan window, and that trailing output is NOT a repeating redraw run
- **THEN** the system reports the agent is not waiting for input

#### Scenario: Signal recoverable behind a long repeating redraw run is still detected

- **WHEN** the selection prompt appears earlier in the buffer and is followed, all the way to the end of the buffer, by a short byte sequence repeating far more times than fits in the base scan window (e.g. a blinking cursor/status glyph that never stops)
- **THEN** the system expands its read backward until it reaches the prompt and reports the agent is waiting for input

#### Scenario: A signal beyond the hard expansion ceiling is not detected

- **WHEN** the selection prompt sits behind a repeating redraw run so long that recovering it would require reading further back than the configured expansion ceiling
- **THEN** the system gives up at the ceiling and reports the agent is not waiting for input, rather than reading unbounded amounts of data

#### Scenario: An ordinary, non-repeating tail is scanned without expansion

- **WHEN** the buffer's base window contains ordinary varying content (not a long repeating run)
- **THEN** the system scans exactly that window with no backward expansion

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

The sticky carry-forward pass — which keeps a task already in the needs-input
set flagged across ticks while its session remains running — SHALL NOT require
re-matching the detection signal against the CURRENT tick's tail to stay
flagged. A task already in the set, whose session is still running, SHALL
remain in the set unconditionally until removed by clear-on-input,
clear-on-archive, or the session no longer running. The recent-output tail
scanned on any given tick MAY fail to re-show the signal for reasons unrelated
to the user having answered (e.g. the signal has scrolled behind other content
still within the bounded/expanded window); such a miss SHALL NOT be treated as
equivalent to a genuine answer.

This clear logic SHALL be applied identically by the daemon-side detector and
the TUI-side detector. The trailing-question entry heuristic, the idle gate, and
the content-stability / emulated-screen guards are unchanged; this requirement
governs only when an already-detected signal is removed from the published set.

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

#### Scenario: A previously-flagged, still-running task stays flagged when the tail no longer shows the signal

- **WHEN** a task is already in the needs-input set, its session is still
  running, and the current tick's recent-output tail no longer shows the
  needs-input signal, but no user input was delivered and the task was not
  archived
- **THEN** the task remains in the needs-input set

#### Scenario: A previously-flagged task still drops when its session stops running

- **WHEN** a task is in the needs-input set and its session is no longer running
- **THEN** the task is removed from the needs-input set
