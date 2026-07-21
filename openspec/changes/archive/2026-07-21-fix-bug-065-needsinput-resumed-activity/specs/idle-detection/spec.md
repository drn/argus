# Idle / Needs-Input Detection

## MODIFIED Requirements

### Requirement: Needs-input flag clears on user input or archive, never on signal decay

The system SHALL keep a session flagged as waiting for user input — including a
flag raised by the trailing-question heuristic (the last transcript line ends in
`?`) — for as long as the signal remains present, with NO time-based or
idle-based decay. The flag SHALL clear only when ONE OF the following holds:
(a) the user delivers new input to that session after the flag was raised, (b)
the session's task is archived, or (c) the session demonstrates sustained
resumed activity (defined below). Input delivered to a DIFFERENT session SHALL
NOT clear this one.

Clear-on-input SHALL be deterministic and MUST NOT depend on the prompt or
question text scrolling out of the recent-output tail: the system records, per
flagged task, the session's last-input timestamp observed when the task first
entered the needs-input set, and removes the task from the set — and suppresses
re-adding it on the same tick — once the session's last-input timestamp advances
past that recorded baseline, even if the question text still matches in the tail.
Clear-on-input SHALL count only genuine user-delivered input, never
system-injected delivery (e.g. a coordinator's relayed message) — so an
unrelated system nudge to a genuinely still-parked agent MUST NOT clear the
flag through this path.

The system SHALL ALSO clear the flag when the session demonstrates SUSTAINED
resumed activity since the flag was raised, independent of whether any input
was ever recorded as user-delivered. Sustained resumed activity is defined as
the session showing Claude's active-generation/tool-execution affordance for a
bounded number of CONSECUTIVE detection ticks with no intervening tick lacking
it. A single tick without the affordance SHALL reset this streak to zero
immediately (no grace tolerance for an isolated miss) — the system SHALL
require the FULL consecutive-tick run to complete before treating the session
as resumed, so a brief acknowledgment burst that re-parks at a blocking prompt
before the streak completes SHALL NOT clear the flag. This clear path exists
specifically to resolve a flag whose triggering block was answered via
system-delivered input (which clear-on-input above deliberately does not
count): a coordinator relaying the human's real decision does not, by itself,
count as user input, but the worker's demonstrated resumption of real,
sustained work SHALL still resolve the flag.

The system SHALL record, per task, the session's last-input timestamp observed
at the moment ANY real clear fires — whether via user input, archive, or
demonstrated resumed activity — (the "cleared marker"), and SHALL carry that
marker forward across ticks for as long as the task's session remains running
— independent of whether the task is a needs-input candidate on any given
tick. If a task is re-presented as a candidate on a LATER tick while its
cleared marker is still current (the session's last-input timestamp has not
advanced past the marker), the system SHALL treat this as a stale re-detection
and SHALL NOT re-add the task to the needs-input set and SHALL NOT recapture a
new baseline for it, REGARDLESS of which clear path produced the marker. The
cleared marker's suppression ends, without any explicit expiry, the moment the
session's last-input timestamp genuinely advances past it — at which point a
subsequent candidacy re-arms normally, capturing a fresh baseline exactly like
a task's first-ever candidacy. The cleared marker SHALL be dropped when the
task's session stops running or the task is archived, so a later restart or
un-archive re-arms cleanly.

Clear-on-archive SHALL remove an archived task from the needs-input set
regardless of its detection signal, so it stops surfacing `?` and stops rolling
up to ancestor coordinators, and SHALL drop both the recorded baseline and the
cleared marker.

The sticky carry-forward pass — which keeps a task already in the needs-input
set flagged across ticks while its session remains running — SHALL NOT require
re-matching the detection signal against the CURRENT tick's tail to stay
flagged. A task already in the set, whose session is still running, SHALL
remain in the set unconditionally until removed by clear-on-input,
clear-on-archive, clear-on-resumed-activity, or the session no longer running.
The recent-output tail scanned on any given tick MAY fail to re-show the
signal for reasons unrelated to the user having answered (e.g. the signal has
scrolled behind other content still within the bounded/expanded window); such
a miss SHALL NOT be treated as equivalent to a genuine answer.

This clear logic SHALL be applied identically by the daemon-side detector and
the TUI-side detector. The trailing-question entry heuristic, the idle gate, and
the content-stability / emulated-screen guards are unchanged; this requirement
governs only when an already-detected signal is removed from the published set
and when a subsequent candidacy for the same task is allowed to re-arm it.

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

#### Scenario: A fresh question after a response re-arms the flag once the session leaves the running set

- **WHEN** a session's flag was cleared by user input, the task's session then
  stops running (or the task cycles out of the tracked set entirely), and later
  a new candidacy for the same task ID arrives with no further input
- **THEN** the system reports the agent is waiting for input again, capturing a
  fresh baseline

#### Scenario: A stale re-candidacy at the same input timestamp does not re-stick the flag (BUG-063)

- **WHEN** a session's flag was correctly cleared by user input, the task's
  session REMAINS running throughout, a later tick presents no candidacy for it
  at all (a gap), and a STILL LATER tick re-presents the same task as a
  candidate while the session's last-input timestamp is unchanged since the
  clear
- **THEN** the system does NOT re-add the task to the needs-input set, and this
  holds across any number of subsequent ticks until either the session's
  last-input timestamp genuinely advances or the task leaves the running set

#### Scenario: A genuinely newer input after a stale-suppressed clear re-arms normally

- **WHEN** a task's needs-input flag is being suppressed by a cleared marker (as
  above) and the session then receives genuinely new user input
- **THEN** the next candidacy for that task re-arms the flag, capturing a fresh
  baseline at the new input timestamp

#### Scenario: A previously-flagged, still-running task stays flagged when the tail no longer shows the signal

- **WHEN** a task is already in the needs-input set, its session is still
  running, and the current tick's recent-output tail no longer shows the
  needs-input signal, but no user input was delivered and the task was not
  archived
- **THEN** the task remains in the needs-input set

#### Scenario: A previously-flagged task still drops when its session stops running

- **WHEN** a task is in the needs-input set and its session is no longer running
- **THEN** the task is removed from the needs-input set

#### Scenario: System-delivered input alone does not clear the flag (BUG-034 regression guard)

- **WHEN** a session is flagged waiting for input and receives ONLY
  system-injected input (e.g. a coordinator's relayed message, delivered via
  reliable pane delivery) with no subsequent sustained resumed activity
- **THEN** the session remains flagged waiting for input

#### Scenario: Sustained resumed activity clears the flag despite no recorded user input (BUG-065)

- **WHEN** a session is flagged waiting for input, its session's last-input
  timestamp never advances past the flag's baseline (e.g. the unblocking
  message arrived via system-injected delivery only), and the session then
  shows Claude's active-generation/tool-execution affordance for the full
  required run of consecutive detection ticks with no interruption
- **THEN** the system removes the session from the needs-input set on
  reaching that consecutive-tick run, and records a cleared marker exactly as
  the user-input clear path does

#### Scenario: A brief resumed-activity burst that re-parks does not clear the flag (BUG-065 regression guard)

- **WHEN** a session is flagged waiting for input and shows the
  active-generation/tool-execution affordance for FEWER than the required
  consecutive-tick run before reverting to showing the identical blocking
  signal that originally raised the flag
- **THEN** the session remains flagged waiting for input
