# Daemon Client

## Purpose

The daemon client is the TUI-side bridge to a long-lived daemon process that owns agent PTY sessions. It connects over a Unix socket, exposes the same session-management surface the TUI uses for in-process sessions, and proxies session control (start, stop, resize, input) over JSON-RPC. For each session it maintains a local copy of recent output, fed by a separate streaming connection, so the TUI can render terminal output even though the real PTY lives in the daemon. This lets agent sessions survive TUI restarts while the UI code stays agnostic about whether it is talking to a local runner or a remote daemon.
## Requirements
### Requirement: Establishing a daemon connection

The client SHALL connect to the daemon by dialing its Unix socket and selecting the request/response protocol. A connection attempt that cannot reach the socket SHALL fail with an error rather than returning a usable client.

#### Scenario: Socket does not exist

- **WHEN** Connect is called with a socket path that has no listening daemon
- **THEN** it SHALL return an error and no client

#### Scenario: Successful connection to a live daemon

- **WHEN** Connect is called against a running daemon socket
- **THEN** it SHALL return a usable client whose session queries answer correctly (e.g. an unknown task reports no session)

### Requirement: RPC calls are bounded by a timeout

Every RPC the client issues SHALL complete within a bounded time so the TUI never hangs if the daemon becomes unresponsive. When a call exceeds its deadline, the client SHALL return a timeout error. Long-running operations that legitimately exceed the default deadline SHALL be allowed a larger deadline.

#### Scenario: Daemon never responds

- **WHEN** an RPC is issued against a connection whose far end never replies
- **THEN** the call SHALL return a timeout error once the deadline elapses, instead of blocking indefinitely

#### Scenario: Long-running self-update

- **WHEN** the client issues the self-update RPC (which shells out to a build)
- **THEN** it SHALL use an extended deadline rather than the short default

### Requirement: Starting a session

The client SHALL request the daemon to start a session for a task, conveying the task identity, prompt, project, backend, worktree, branch, requested terminal size, and whether to resume. On success it SHALL return a session handle reporting a non-zero process id and SHALL begin streaming the session's output locally. If the RPC fails or the daemon reports an error for the start, the client SHALL return an error and no handle.

#### Scenario: Successful start

- **WHEN** Start is called for a valid backend
- **THEN** it SHALL return a session handle whose PID is non-zero, and the session's output SHALL become available locally as the process produces it

#### Scenario: Daemon rejects the start

- **WHEN** Start is called with a backend the daemon cannot run
- **THEN** it SHALL return an error and no usable handle

#### Scenario: Daemon unreachable during start

- **WHEN** Start is called but the RPC connection is closed
- **THEN** it SHALL return an error and no handle

### Requirement: Looking up an existing session

The client SHALL return a handle for a task if it already tracks one locally, or if the daemon reports a live session for that task. It SHALL return nothing when the daemon reports no live session, or when the lookup RPC fails.

#### Scenario: Locally tracked session

- **WHEN** Get is called for a task the client already has a session for
- **THEN** it SHALL return that existing handle

#### Scenario: Session known only to the daemon

- **WHEN** Get is called for a task the client has no local handle for but the daemon reports alive
- **THEN** it SHALL create and return a handle for that task

#### Scenario: No live session

- **WHEN** Get is called for a task the daemon reports as not alive
- **THEN** it SHALL return nothing

#### Scenario: Lookup RPC fails

- **WHEN** Get is called but the lookup RPC fails
- **THEN** it SHALL return nothing

### Requirement: Querying running and idle sessions

The client SHALL be able to report which task sessions the daemon considers running and which it considers idle, including a combined query returning both in one call. When the underlying query fails, the client SHALL report no sessions rather than partial or stale data.

#### Scenario: No sessions exist

- **WHEN** the running/idle queries are issued against a daemon with no sessions
- **THEN** each query SHALL return an empty result

#### Scenario: A live session exists

- **WHEN** a session is running on the daemon
- **THEN** the running query SHALL include that task's id

#### Scenario: Query RPC fails

- **WHEN** the running/idle query RPC fails
- **THEN** the client SHALL return no task ids

### Requirement: Stopping sessions

The client SHALL stop a single session by task id, propagating any daemon-reported error. It SHALL also support stopping all sessions as a best-effort operation that ignores errors, suitable for shutdown.

#### Scenario: Stop an unknown session

- **WHEN** stopping a task that has no session on the daemon
- **THEN** the client SHALL return an error

#### Scenario: Stop a running session

- **WHEN** stopping a task with a live session
- **THEN** the client SHALL return no error and the session SHALL become not-alive

#### Scenario: Stop-all with no sessions

- **WHEN** StopAll is called with no sessions present
- **THEN** it SHALL complete without error or panic

### Requirement: Output streaming into a local buffer

For each session, the client SHALL open a separate streaming connection to the daemon and copy received bytes into a local fixed-size buffer that backs the TUI's view of recent output. When reconnecting a stream, the client SHALL tell the daemon how many bytes it has already received so only the missed delta is replayed, avoiding duplicate content.

#### Scenario: Output becomes available locally

- **WHEN** a session produces output on the daemon
- **THEN** that output SHALL appear in the session handle's recent-output buffer

#### Scenario: Reconnect requests only the missed delta

- **WHEN** the stream reconnects after some bytes were already buffered locally
- **THEN** the stream subscription SHALL request output starting from the count already received rather than from the beginning

### Requirement: Stream-loss versus process-exit distinction

When a session's stream ends, the client SHALL ask the daemon whether the underlying process actually exited. A confirmed exit SHALL be reported as a session exit; a dropped stream while the process is still alive SHALL trigger limited retries; and an unreachable daemon or exhausted retries SHALL be reported as a stream-loss rather than an exit, so the task is not wrongly marked complete.

#### Scenario: Process actually exited

- **WHEN** a session's process exits on the daemon
- **THEN** the registered exit callback SHALL fire for that task

#### Scenario: Daemon unreachable while streaming

- **WHEN** the stream cannot be maintained and the daemon is unreachable
- **THEN** the exit callback SHALL fire with the stream-lost indicator set, not a normal exit

#### Scenario: Session closed externally during streaming

- **WHEN** the session is closed externally (e.g. client shutdown) rather than by process exit
- **THEN** the exit callback SHALL report stream-loss with the stopped flag unset

#### Scenario: Liveness check when stream ends

- **WHEN** the stream ends and the daemon reports the session is not alive
- **THEN** the client SHALL treat it as a process exit; if the daemon is unreachable it SHALL instead treat it as stream-loss

### Requirement: Forwarding terminal input

A session handle SHALL forward written input to the daemon for delivery to the PTY, reporting the number of bytes accepted, and SHALL track the wall-clock time of the most recent input. Input written after a session is closed SHALL not block indefinitely. Consecutive inputs MAY be coalesced into fewer RPCs, but coalescing SHALL stop at a bracketed-paste end boundary so two back-to-back paste cycles are never merged into a single write.

Every forwarded input SHALL carry an explicit origin (human or system-injected — see the `agent-execution` capability's input-forwarding requirement) across the RPC as a request field. Coalescing SHALL ALSO stop at an origin boundary: two consecutive queued writes with different origins SHALL NOT be merged into a single RPC, since origin is a per-request attribute and merging would misattribute one write's origin to the other's bytes. An item that cannot be merged for this reason SHALL be carried forward to the next RPC rather than dropped or silently absorbed.

The origin field SHALL be safely additive on the wire: its absence (an older peer's request) SHALL be interpreted identically to the human origin, which is the only behavior any pre-existing peer ever exhibited, so a version-mismatched daemon/supervisor pair SHALL continue to function without error — a system-origin write simply degrades to human-origin semantics on a hop where the far side predates this field, rather than being rejected or corrupting the connection.

#### Scenario: Write reports byte count and advances last-input time

- **WHEN** input is written to a live session handle
- **THEN** the call SHALL report the number of bytes written and the handle's last-input time SHALL advance from its zero value

#### Scenario: Coalescing plain input

- **WHEN** multiple plain (non-paste) inputs of the SAME origin are pending
- **THEN** they SHALL be combined into a single buffer with nothing left pending

#### Scenario: Flush at paste boundary

- **WHEN** a buffer already ends with a bracketed-paste end sequence and more input is pending
- **THEN** coalescing SHALL stop and the still-pending input SHALL remain for a later flush

#### Scenario: Two back-to-back pastes stay separate

- **WHEN** two complete bracketed-paste cycles are queued back to back
- **THEN** each cycle SHALL be drained as its own buffer rather than merged into one

#### Scenario: Origin boundary stops coalescing

- **WHEN** a queued input has a different origin than the batch currently being assembled
- **THEN** coalescing SHALL stop before that item, it SHALL be carried forward rather than dropped, and it SHALL be sent as its own subsequent RPC

#### Scenario: A version-mismatched peer defaults the origin to human, not an error

- **WHEN** an input-forwarding request arrives without an origin field (an older peer)
- **THEN** the receiving side SHALL treat it as human-origin input and process it exactly as it would have before this field existed

### Requirement: Session state and liveness reporting

A session handle SHALL report its process id, liveness, idle state, current and initial PTY size, working directory, and recent-output metrics, refreshing daemon-sourced values on demand. When the daemon cannot be reached for a refresh, the handle SHALL fail silently rather than panic, leaving prior cached values intact.

#### Scenario: Reporting PTY size and working directory

- **WHEN** a session was started with a given terminal size and worktree
- **THEN** the handle SHALL report that current and initial PTY size and a non-empty working directory

#### Scenario: Liveness reflects done state

- **WHEN** the session's done channel has not been closed
- **THEN** the handle SHALL report alive, and its done channel SHALL not be readable

#### Scenario: Refresh against an unreachable daemon

- **WHEN** a state refresh is attempted but the RPC connection is closed
- **THEN** the refresh SHALL silently swallow the error without panicking

### Requirement: Clipboard staging proxy

The client SHALL fetch and clear agent-staged clipboard text for a task via the daemon. A fetch SHALL report unavailability when nothing is staged or the RPC fails, and a clear SHALL propagate any daemon-reported or transport error.

#### Scenario: Nothing staged

- **WHEN** clipboard text is requested for a task with nothing staged
- **THEN** the client SHALL report not-available with empty text

#### Scenario: Staged text is returned then cleared

- **WHEN** text has been staged for a task
- **THEN** a fetch SHALL return that text as available, and after a clear a subsequent fetch SHALL report not-available

#### Scenario: Transport failure on clipboard ops

- **WHEN** the RPC connection is closed during a clipboard fetch or clear
- **THEN** the fetch SHALL report not-available and the clear SHALL return an error

### Requirement: Client shutdown stops streaming and retries

Closing the client SHALL signal all stream-connection goroutines to stop retrying and SHALL close every tracked session, so no stale stream goroutine continues to dial the socket after shutdown (e.g. across a daemon restart). When the close is part of a daemon bounce (restarting the daemon process, not just tearing down the local connection), the caller SHALL first request a graceful remote shutdown via the `Shutdown()` RPC so the connected daemon process actually exits, rather than only closing the local connection and relying on the replacement daemon's own startup-time singleton-lock takeover to force the old process out. A `Shutdown()` RPC failure (e.g. the connection is already dead) SHALL NOT block the bounce — the caller proceeds to close the local connection and start a replacement as before.

#### Scenario: Close signals stream goroutines

- **WHEN** the client is closed
- **THEN** the shutdown signal SHALL become observable and in-flight stream connection loops SHALL stop and return

#### Scenario: Stream loop exits when client already closed

- **WHEN** a stream connection loop runs after the client has been closed
- **THEN** it SHALL exit immediately without attempting to dial the daemon

#### Scenario: Daemon bounce requests a real remote shutdown

- **WHEN** the TUI bounces the daemon (directly, or as part of a supervisor restart)
- **THEN** it SHALL call the connected client's `Shutdown()` RPC before closing the local connection and starting a replacement daemon

#### Scenario: Remote shutdown failure does not block the bounce

- **WHEN** the daemon bounce's `Shutdown()` RPC call fails (e.g. the connection is already shut down)
- **THEN** the bounce SHALL proceed to close the local connection and attempt to start a replacement daemon, unchanged from today

### Requirement: Daemon auto-start refuses to run under test binaries

The client SHALL provide a way to auto-start the daemon when none is running. To avoid re-executing the test process as a daemon (a fork-bomb and a hazard to real on-disk state), auto-start SHALL refuse with a distinct error when invoked from a Go test binary.

#### Scenario: Auto-start invoked under go test

- **WHEN** auto-start is invoked from a test binary
- **THEN** it SHALL return the test-binary refusal error and SHALL NOT fork a daemon process

### Requirement: Waiting for daemon shutdown

The client SHALL provide a helper that waits until the daemon socket no longer exists, up to a timeout, so callers can confirm a daemon has fully shut down.

#### Scenario: Socket already gone

- **WHEN** the wait helper is called for a socket path that does not exist
- **THEN** it SHALL return promptly

#### Scenario: Socket persists until timeout

- **WHEN** the wait helper is called for a path that continues to exist
- **THEN** it SHALL poll until the timeout elapses before returning

