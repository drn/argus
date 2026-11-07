## ADDED Requirements

### Requirement: The idle pass reuses its last reading while a session's log is unchanged

The TUI's needs-input scan SHALL NOT re-read or re-emulate an idle session whose on-disk log is unchanged since its last reading, and SHALL reuse that reading instead, on the same terms as the other scan passes. It SHALL read fresh on the first tick a session becomes idle and when no prior reading exists. Tick counters SHALL still advance on every scan.

#### Scenario: An idle session with an unchanged log is not re-read

- **WHEN** a session was idle last tick and is idle this tick, and its log's size and modification time are unchanged
- **THEN** the idle pass reuses last tick's reading without reading the log or rendering

#### Scenario: A session that has just gone idle is read fresh

- **WHEN** a session is idle this tick but was not idle last tick
- **THEN** the idle pass reads and evaluates its tail on this tick

#### Scenario: A flagged session stays flagged across unchanged ticks

- **WHEN** an idle session showing a prompt is flagged and its log does not change for several ticks
- **THEN** it stays flagged, with the same result as re-reading every tick

#### Scenario: A changed log is re-read

- **WHEN** an idle session's log changes between ticks
- **THEN** the session is read and evaluated afresh on its next due tick

### Requirement: A scan reads and renders each session's tail at most once

The TUI's needs-input scan SHALL read each session's tail at most once per scan, and SHALL strip ANSI from it and render it through the terminal emulator at most once per scan, sharing those results across every detector that evaluates that session. Detector results SHALL be identical to evaluating each detector independently over the same tail. A detector that matches on the raw tail SHALL NOT itself cause a render.

#### Scenario: Several detectors share one render

- **WHEN** a session's log has changed and several detectors evaluate it in one scan
- **THEN** its tail is read once and rendered no more than once

#### Scenario: Results match independent evaluation

- **WHEN** a recorded session tail is evaluated through the shared path and through independent detector calls
- **THEN** every detector returns the same result both ways

#### Scenario: A raw match does not cause a render

- **WHEN** a raw-first detector matches on a session's raw tail and no other detector has asked for the rendered screen
- **THEN** that detector returns its result without using the terminal emulator

#### Scenario: Blocked-role auto-clear uses the scan's reading

- **WHEN** a role is marked blocked and its task was evaluated by the scan this tick
- **THEN** the auto-clear uses that reading and does not render the tail again

### Requirement: The idle pass skips archived sessions

The TUI's needs-input scan SHALL NOT read or render an archived session in the idle pass, since an archived task is always dropped from the needs-input set.

#### Scenario: An archived idle session is not read

- **WHEN** an archived task's session is idle
- **THEN** the idle pass does not read its log, and the task is not flagged

### Requirement: An out-of-rotation session keeps its last log snapshot

When a fleet exceeds the batch size, a session outside the due batch SHALL keep the log snapshot from its last actual reading, without recording a new one, so that when its batch next comes due an unchanged log is recognized and not re-read.

#### Scenario: An unchanged session is not re-read when its batch comes due

- **WHEN** a fleet exceeds the batch size and a session's log does not change across a full rotation
- **THEN** on its next due tick it is recognized as unchanged and not re-read

#### Scenario: A change during rotation is still caught

- **WHEN** a session's log changes while it is outside the due batch
- **THEN** the change is detected on its next due tick
