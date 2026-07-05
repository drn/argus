# Daemon Lifecycle

## ADDED Requirements

### Requirement: Host-suspend detection and running-task notification

The daemon SHALL run a background watchdog whenever it is serving that, on a fixed tick cadence, compares the current WALL-CLOCK time against the previous tick's wall-clock time. A gap far exceeding the tick interval SHALL be treated as evidence that the host was suspended (laptop sleep, hibernate, or VM pause) and resumed, because such an event freezes and resumes every process on the host — the daemon's own tick loop, the coordinator's session, and every worker's session — together.

The comparison SHALL use wall-clock time, not the process monotonic clock, because the monotonic clock does not advance during host sleep and would under-report the gap to roughly the tick interval, defeating detection.

On detecting a suspend gap, the daemon SHALL post exactly one system note into the inbox of every currently-running task, using the same system-sender / note-kind / inbox-insert path as the daemon-bounce signal. The note SHALL carry the approximate gap duration and guidance that the gap is a host-suspend artifact during which every session was paused equally, and that concurrent worker silence spanning the gap MUST NOT be treated as staleness or a stuck/dead agent. The watchdog SHALL run independent of whether Hera coordination is enabled, since any agent reasoning about elapsed time — not only Hera coordinators — can misjudge the silence, and the note is harmless for a non-coordinator to receive.

The notification SHALL fire at most once per suspend event: the watchdog advances its baseline to the current tick on every tick, so the tick immediately following a suspend observes a normal-cadence gap and stays silent, requiring no additional de-duplication state. The watchdog SHALL NOT notify on its first comparison after start, which it guarantees by stamping the baseline before its first tick so the first comparison is against a real, recent timestamp rather than a zero value. Running tasks that no longer exist or are archived SHALL be skipped.

This detection is advisory only: the daemon SHALL NOT auto-correct, retry, cancel, time out, or otherwise alter any task or role state in response to a detected suspend.

#### Scenario: Normal-cadence tick sends nothing

- **WHEN** the wall-clock gap between two consecutive watchdog ticks is within the normal tick interval
- **THEN** the daemon posts no system note and advances its baseline to the current tick

#### Scenario: Large wall-clock gap notifies every running task

- **WHEN** the wall-clock gap between two consecutive watchdog ticks exceeds the suspend threshold
- **THEN** the daemon posts exactly one host-suspend system note, carrying the approximate gap, into the inbox of every currently-running task

#### Scenario: Notification is one-shot per suspend

- **WHEN** a suspend gap has just been detected and notified, and the next tick fires on a normal cadence
- **THEN** the daemon posts no further note for that suspend, without any per-event de-duplication state

#### Scenario: First comparison after start sends nothing

- **WHEN** the watchdog performs its first comparison after the daemon starts, with the baseline just stamped
- **THEN** the daemon posts no system note

#### Scenario: Archived or missing running task is skipped

- **WHEN** a suspend gap is detected and a task reported as running no longer exists or is archived
- **THEN** the daemon skips that task and still notifies the remaining running tasks

#### Scenario: Detection does not mutate state

- **WHEN** a suspend gap is detected
- **THEN** the daemon changes no task status, role status, or plan node — it only posts the advisory note
