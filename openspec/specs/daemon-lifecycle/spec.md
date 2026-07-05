# Daemon Lifecycle

## Purpose

The daemon is the long-lived process that owns agent PTY sessions so they survive TUI restarts and serve web/PWA clients with no TUI attached. It exposes a single Unix-domain socket that multiplexes request/response RPC and raw output streaming, guarantees that exactly one daemon owns the socket at a time, reconciles orphaned task state on startup, shuts down gracefully on signal, and provides a TUI-independent entry point for creating tasks and starting their agent sessions.
## Requirements
### Requirement: Single-socket protocol dispatch

The daemon SHALL accept all client connections on one Unix-domain socket and route each connection by its first byte: `R` selects JSON-RPC request/response, `S` selects raw output streaming. A connection whose first byte is neither SHALL be rejected and closed without further processing.

#### Scenario: RPC connection
- **WHEN** a client connects and sends the byte `R` followed by a JSON-RPC request
- **THEN** the daemon serves it as a JSON-RPC call and returns the response on the same connection

#### Scenario: Stream connection
- **WHEN** a client connects and sends the byte `S` followed by a stream header
- **THEN** the daemon treats the connection as an output stream for the named task

#### Scenario: Unknown prefix
- **WHEN** a client connects and sends a first byte other than `R` or `S`
- **THEN** the daemon closes the connection without serving RPC or stream

### Requirement: Singleton ownership of the socket

The daemon SHALL ensure that at most one daemon owns the socket. On startup it SHALL terminate any prior daemon recorded in the PID file and acquire an exclusive advisory lock before binding; if another live daemon already holds the lock, the new daemon SHALL exit cleanly with an already-running signal rather than binding the socket.

#### Scenario: Lock already held
- **WHEN** the daemon starts but another process already holds the singleton lock
- **THEN** it returns an already-running result, does not bind the socket, and signals readiness so any shutdown waiters do not block

#### Scenario: Lock contended then released
- **WHEN** one holder releases the singleton lock
- **THEN** a subsequent acquire succeeds

#### Scenario: Prior daemon in PID file
- **WHEN** the PID file names a live prior daemon process at startup
- **THEN** the new daemon signals that process to terminate (escalating to force-kill if it does not exit within the grace window) before taking over

### Requirement: PID file and stale-file hygiene

The daemon SHALL record its own PID in a PID file beside the socket and SHALL only remove the socket and PID file on shutdown if the PID file still names its own process, so that a newer daemon's files are never deleted by an older instance.

#### Scenario: Cleanup when still owner
- **WHEN** the daemon shuts down and the PID file still contains its PID
- **THEN** it removes both the socket and PID file

#### Scenario: Cleanup skipped when superseded
- **WHEN** the daemon shuts down but the PID file now names a different process
- **THEN** it leaves the socket and PID file in place

#### Scenario: Missing or unparsable PID file
- **WHEN** the PID file is absent or its contents are not a valid integer
- **THEN** the PID read yields zero and no prior process is signaled

### Requirement: Startup reconciliation of orphaned tasks

On startup, before accepting connections, the daemon SHALL sweep the database for tasks left in the in-progress state by a previous run and reconcile them, since the new daemon owns no sessions for them. A reconciliation failure SHALL be logged and SHALL NOT prevent the daemon from serving.

#### Scenario: Orphan sweep before accept
- **WHEN** the daemon starts and the database contains in-progress tasks with no live session
- **THEN** those rows are reconciled out of the in-progress state before the listener accepts the first connection

### Requirement: Session-exit status transition

When a session exits, the daemon SHALL flip its task out of the in-progress state: a naturally-exited session transitions the task to complete, a stopped (interrupted) session transitions it to in-review. The transition SHALL be a no-op if the task is not in the in-progress state, and SHALL be skipped when a kick-restart is already queued for that task.

#### Scenario: Natural exit completes the task
- **WHEN** an in-progress task's session exits naturally
- **THEN** the task transitions to complete

#### Scenario: Stopped session moves to review
- **WHEN** an in-progress task's session is stopped
- **THEN** the task transitions to in-review

#### Scenario: Already-terminal task untouched
- **WHEN** a session exits for a task that is no longer in-progress
- **THEN** the task status is left unchanged

#### Scenario: Pending restart suppresses transition
- **WHEN** a session exits while a kick-restart is queued for the same task
- **THEN** the status transition is skipped so it does not race the imminent restart

### Requirement: Cached exit info on session end

When a session exits, the daemon SHALL cache its exit information (error string, stopped flag, last output, pending-restart flag) so a client can query it once after the stream closes. The cached info SHALL be consumed on read, returning empty thereafter.

#### Scenario: Consume-once exit info
- **WHEN** a client queries exit info for a finished session
- **THEN** the cached info is returned and removed, and a subsequent query returns empty

### Requirement: Post-exit session ID capture

After a session exits with no recorded session ID and a known worktree, the daemon SHALL attempt a backend-specific capture of the session UUID and persist it, so headless and web-only users can later resume backends (such as codex and pi) that mint their IDs only after exit. The capture SHALL be a no-op when the task is missing, already has a session ID, has no worktree, the backend yields no ID, or the capture fails — and SHALL never corrupt the task row.

#### Scenario: Capture and persist for late-minting backend
- **WHEN** a task with a worktree and no session ID exits and its backend exposes a session file
- **THEN** the daemon captures the UUID and writes it back to the task row

#### Scenario: Skip when ID already present
- **WHEN** the task already has a session ID
- **THEN** the daemon leaves the session ID unchanged

#### Scenario: Capture error leaves row intact
- **WHEN** the backend-specific capture fails
- **THEN** the daemon logs and returns without modifying the task row

### Requirement: Stream subscription and incremental replay

On a stream connection the client SHALL send a header naming the task and an offset of already-received bytes; the daemon SHALL replay only the output past that offset before attaching the connection for live output, then keep the connection open until the session exits, the client disconnects, or the daemon shuts down. If the named session is absent, the daemon SHALL close the stream — except during a queued kick-restart, where it SHALL wait briefly for the new session to appear.

#### Scenario: Subscribe with offset
- **WHEN** a stream client sends a header naming a live session with a byte offset
- **THEN** the daemon replays only output beyond that offset and then streams live bytes

#### Scenario: Session absent
- **WHEN** a stream client names a task that has no session and no queued restart
- **THEN** the daemon closes the stream connection

#### Scenario: Wait through kick-restart gap
- **WHEN** a stream client connects while a kick-restart for the task is in flight and the new session has not yet appeared
- **THEN** the daemon waits up to a bounded interval for the new session and attaches once it appears, abandoning the wait if the restart is dropped or the daemon shuts down

### Requirement: Pending-restart liveness reporting

While a kick-restart is queued for a task but its new session has not yet been created, the daemon SHALL report that task as alive in single-task status and in the session list, so client reconcilers do not prematurely mark a mid-restart task complete.

#### Scenario: Status reports alive during gap
- **WHEN** a client requests session status for a task with a queued restart and no current session
- **THEN** the response reports the task as alive

#### Scenario: List includes synthetic gap entry
- **WHEN** a client lists sessions and a task has a queued restart with no current session
- **THEN** the listing includes that task marked alive

### Requirement: Headless task creation and session start

The daemon SHALL provide a TUI-independent entry point that creates a task, its worktree, and starts its agent session transactionally — used by the HTTP API, MCP server, and scheduler. Any failure during creation SHALL unwind all prior side effects so no orphan worktree, branch, or task row remains. When the task lists unresolved dependencies, the task SHALL be persisted in the pending state with no agent process spawned.

#### Scenario: Single task created and started
- **WHEN** a headless caller requests a new task with no dependencies
- **THEN** the worktree is created, the task is persisted, and its agent session is started

#### Scenario: Dependent task deferred
- **WHEN** a headless caller requests a task that depends on other tasks
- **THEN** the task is persisted as pending and no agent process is started

### Requirement: RPC session control

The daemon SHALL expose RPC methods to start, stop, and stop-all sessions, write input to and resize a session's PTY, and query liveness — reporting a populated handle on success and an error string when the target session is not found.

#### Scenario: Start then list then stop
- **WHEN** a client starts a session, lists sessions, then stops it
- **THEN** the start returns a non-zero PID, the session appears in the listing, and after the stop it no longer appears

#### Scenario: Input or resize to missing session
- **WHEN** a client writes input to or resizes a session that does not exist
- **THEN** the response carries a not-found error rather than succeeding

#### Scenario: Liveness ping
- **WHEN** a client calls the responsiveness ping
- **THEN** the daemon replies OK

### Requirement: Graceful shutdown

The daemon SHALL shut down gracefully on receiving SIGTERM or SIGINT, or on an RPC shutdown request, by stopping the accept loop and then, on the serving goroutine, stopping all sessions and dependent services before removing its files and releasing the lock. Shutdown SHALL be idempotent.

#### Scenario: RPC shutdown returns and stops serving
- **WHEN** a client invokes the shutdown RPC
- **THEN** the daemon acknowledges, stops accepting connections, and the serve call returns without error

#### Scenario: Repeated shutdown is safe
- **WHEN** shutdown is requested more than once
- **THEN** the second request is a no-op

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

