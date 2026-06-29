# Agent Execution

## Purpose

Agent Execution governs how Argus turns a task into a running LLM coding agent: resolving which backend command to launch, building the shell command (with prompt, session pinning, and resume flags), spawning the process attached to a pseudo-terminal, and managing that process's lifecycle. It owns the single-reader model that tees PTY output to a replayable ring buffer and to any number of live consumers (TUI, daemon stream, SSE), plus attach/detach, idle detection, resize, and post-exit session-ID capture. The goal is that one task's agent runs in isolation, its output is never lost or duplicated across reconnecting viewers, and a viewer joining late sees a coherent stream.
## Requirements
### Requirement: Backend resolution precedence

The system SHALL resolve the backend for a task using the precedence task backend, then project backend, then the configured default backend. When no backend can be resolved, or the resolved name is absent from the configured backends, the system SHALL return an error rather than launching.

#### Scenario: Task backend wins over project and default
- **WHEN** a task names a backend that exists in config, even though its project also names one
- **THEN** the task's backend is selected

#### Scenario: Project backend used when task has none
- **WHEN** a task names no backend but belongs to a project that names one
- **THEN** the project's backend is selected

#### Scenario: Default used when neither task nor project specifies one
- **WHEN** a task names no backend and its project (if any) names none
- **THEN** the configured default backend is selected

#### Scenario: Unknown or unset backend errors
- **WHEN** the resolved backend name is empty, or names a backend not present in config
- **THEN** an error is returned and no command is produced

### Requirement: Command construction with prompt and worktree isolation

The system SHALL build the agent command as a shell invocation whose working directory is the task's worktree. The prompt, when present, SHALL be passed using the backend's configured prompt flag, or — when no prompt flag is configured — as a positional argument guarded by a `--` end-of-options separator so prompts beginning with `-` are not parsed as flags. The system SHALL refuse to build a command for a task with no worktree, and SHALL refuse when the worktree directory does not exist or is unreachable, returning an actionable error and a nil command.

#### Scenario: Prompt passed via configured prompt flag
- **WHEN** the backend defines a prompt flag and the task has a prompt
- **THEN** the command appends the prompt flag followed by the safely quoted prompt

#### Scenario: Prompt passed positionally with separator when no flag
- **WHEN** the backend defines no prompt flag and the task has a prompt
- **THEN** the command appends `--` followed by the safely quoted prompt

#### Scenario: Missing worktree is rejected
- **WHEN** a task has no worktree set
- **THEN** command construction fails with an error stating no worktree is set

#### Scenario: Nonexistent worktree directory is rejected pre-launch
- **WHEN** the task's worktree path does not exist
- **THEN** command construction fails with an error naming the missing path, and returns a nil command and nil cleanup

#### Scenario: Working directory and task environment are set
- **WHEN** a command is built for a task with an ID
- **THEN** the command's working directory is the worktree and the environment exports the task ID for sub-agent tooling

### Requirement: Forced terminal capability environment

The system SHALL force terminal-capability environment variables on every spawned agent so color rendering is independent of what the parent process inherited, since the agent's controlling terminal is Argus's truecolor emulator rather than the launching shell.

#### Scenario: Color env forced even when parent has none
- **WHEN** a command is built while the parent environment has no terminal-capability variables set
- **THEN** the command's environment advertises a 256-color, truecolor-capable terminal

### Requirement: Session pinning and resume by backend family

The system SHALL pin a new session's ID for backends that accept a start-time session identifier (Claude-style), and SHALL omit it for backends that do not (codex, pi). On resume, the system SHALL reconnect to an existing conversation using the backend's own resume mechanism, and SHALL drop the prompt because the conversation is reloaded. When resume is requested for a Claude-style or pi backend but no session ID is known, the system SHALL start a fresh session rather than emit an empty resume flag.

#### Scenario: New Claude-style session pins the ID
- **WHEN** a new (non-resume) session is built for a Claude-style backend that has a session ID
- **THEN** the command includes a start-time session-id flag carrying that ID

#### Scenario: New codex/pi session never pins the ID
- **WHEN** a new session is built for a codex or pi backend even though a session ID is present
- **THEN** the command omits any start-time session-id flag

#### Scenario: Resume reconnects and drops the prompt
- **WHEN** a resume command is built for a backend with a known session ID
- **THEN** the command uses that backend's resume form carrying the ID and does not append the prompt

#### Scenario: Resume with no session ID starts fresh
- **WHEN** resume is requested for a Claude-style or pi backend but no session ID is set
- **THEN** the command is the plain base command with no resume flag

### Requirement: Post-exit session-ID capture for capture-style backends

The system SHALL recover a session identifier after exit for backends that mint their ID externally (codex, pi) by reading the backend's own state for the task's worktree, returning the most recently updated matching session. For backends that pin their ID at start (Claude-style) or are unrecognized, the system SHALL report no captured ID without error. A captured codex ID SHALL be validated as a UUID before being returned.

#### Scenario: Codex ID recovered from its state for the worktree
- **WHEN** capture runs for a codex-backed task whose worktree has a recorded session
- **THEN** the most-recently-updated session ID for that worktree is returned

#### Scenario: Pi ID recovered from the newest session file
- **WHEN** capture runs for a pi-backed task whose worktree has session files
- **THEN** the UUID from the newest matching session file is returned

#### Scenario: Claude-style and unknown backends capture nothing
- **WHEN** capture runs for a Claude-style or unrecognized backend
- **THEN** an empty ID is returned with no error

#### Scenario: Malformed captured codex ID is rejected
- **WHEN** the recorded codex session ID is not a valid UUID
- **THEN** capture returns an error rather than the malformed value

### Requirement: Single-session-per-task management

The runner SHALL manage at most one live session per task ID. Starting a session for a task that already has one SHALL fail. The runner SHALL expose whether a task has a live session and SHALL return the live handle when one exists rather than spawning a duplicate.

#### Scenario: Duplicate start rejected
- **WHEN** a start is requested for a task that already has a live session
- **THEN** the start fails with an error and no second process is spawned

#### Scenario: Reattach returns the existing session
- **WHEN** a start-or-reattach is requested for a task with a live session
- **THEN** the existing live handle is returned and flagged as reattached rather than a new one being spawned

### Requirement: Process exit notification and last output

When a managed session's process exits, the runner SHALL invoke its finish callback exactly once with the task ID, the exit error, whether the stop was explicitly requested, and the final buffered output. The session's exit signal SHALL only fire after all pending PTY output has been drained into the ring buffer, so the final output is complete.

#### Scenario: Natural exit reports not-stopped with output
- **WHEN** an agent process exits on its own after producing output
- **THEN** the finish callback fires with stopped=false and the last buffered output available

#### Scenario: Output complete at exit signal
- **WHEN** a short-lived process emits output and exits
- **THEN** the buffered output read after the exit signal contains everything the process produced

### Requirement: Stop semantics

The runner SHALL stop a live session by signaling termination and SHALL mark the stop as explicit so the finish callback reports it. Stopping a task with no live session SHALL return a not-found error. Stopping a session whose process has already exited SHALL be a no-op success. Stop-all SHALL terminate every live session and unblock any in-flight pre-launch work.

#### Scenario: Stop unknown task errors
- **WHEN** stop is requested for a task with no live session
- **THEN** a session-not-found error is returned

#### Scenario: Stop already-exited session is a no-op
- **WHEN** stop is requested for a session whose process has already exited
- **THEN** it returns success without error

### Requirement: PTY output teed to ring buffer and live writers

A single reader SHALL be the sole consumer of the PTY, writing every chunk to a fixed-size ring buffer and teeing it to all registered writers. A writer whose write fails SHALL be removed automatically without disrupting other writers. The ring buffer SHALL retain a bounded recent window and expose a monotonic total-bytes count that never decreases.

#### Scenario: Multiple writers all receive output
- **WHEN** several writers are registered before output is produced
- **THEN** each registered writer receives the produced output

#### Scenario: Failing writer is dropped
- **WHEN** a registered writer returns an error on write
- **THEN** that writer is removed from the set while others keep receiving output

#### Scenario: Removed writer stops receiving output
- **WHEN** a writer is removed and more output is produced
- **THEN** the removed writer receives no further bytes

### Requirement: Replay on attach without gaps or duplicates

When a writer attaches, the system SHALL replay the buffered history the writer has not yet seen and then deliver live output. An offset-aware attach SHALL replay exactly the bytes from the caller's offset to the current high-water mark with no gap and no duplicate, even under concurrent attaches; an offset already at or beyond the high-water mark SHALL skip replay and deliver only live output. If replay fails, the writer SHALL NOT be registered.

#### Scenario: Caught-up attach skips replay
- **WHEN** a writer attaches at an offset equal to the current total bytes
- **THEN** it receives no replay of prior bytes and only subsequent live output

#### Scenario: Partial-replay attach gets exactly the suffix
- **WHEN** a writer attaches at an offset some bytes behind the high-water mark
- **THEN** it receives exactly those missing bytes as replay followed by live output

#### Scenario: Replay failure prevents registration
- **WHEN** a writer's replay write returns an error
- **THEN** the writer is not added to the live writer set

### Requirement: Attach exclusivity and detach

A session SHALL allow only one full interactive attach at a time; a second attach SHALL fail. Attach SHALL block until detach, process exit, or an I/O error, returning no error on detach and the process error on exit. Detach SHALL be safe to call when not attached and safe to call more than once.

#### Scenario: Second attach rejected
- **WHEN** a session is already attached and a second attach is requested
- **THEN** the second attach fails with an already-attached error

#### Scenario: Detach returns attach cleanly
- **WHEN** an attached session is detached
- **THEN** the blocking attach returns with no error

#### Scenario: Attach to exited process returns its result
- **WHEN** attach is requested for a session whose process has already exited
- **THEN** attach returns promptly with the process's exit result

### Requirement: PTY sizing and resize

A session SHALL start with the requested PTY dimensions, falling back to a default size when zero is supplied. Resize SHALL update the live PTY dimensions; after the process has exited, resize SHALL be a no-op success and SHALL preserve the last size the agent actually rendered for. The system SHALL expose both the current size and the immutable initial start size, and SHALL persist the session's size so a dead session's output can be re-emulated at the width it was formatted for.

#### Scenario: Zero size falls back to default
- **WHEN** a session is started with zero rows or cols
- **THEN** the PTY is sized to the default dimensions

#### Scenario: Resize updates live dimensions
- **WHEN** a live session is resized
- **THEN** the reported current size reflects the new dimensions

#### Scenario: Initial size is immutable across resize
- **WHEN** a session is resized after start
- **THEN** the reported initial size still equals the start dimensions

#### Scenario: Resize after exit is a safe no-op
- **WHEN** resize is called after the process has exited and the PTY is closed
- **THEN** it returns success without error

### Requirement: Idle detection

A session SHALL be reported idle only while it is alive and has produced no output for at least the idle threshold. A session that has never produced output SHALL NOT be reported idle (it is still starting), and an exited session SHALL NOT be reported idle. The runner SHALL distinguish, in one snapshot, which sessions are running versus idle.

#### Scenario: Quiet live session becomes idle
- **WHEN** a live session has produced output but none for longer than the idle threshold
- **THEN** it is reported idle

#### Scenario: Freshly started session is not idle
- **WHEN** a session has just started and produced no output yet
- **THEN** it is not reported idle

#### Scenario: Dead session is not idle
- **WHEN** a session's process has exited
- **THEN** it is not reported idle

### Requirement: Input forwarding records activity only on success

The system SHALL forward raw input bytes to the agent's PTY and SHALL record the wall-clock time of input only when the write succeeds, so a failed write (e.g. after the PTY is closed) does not advance the last-input timestamp. The last-input time SHALL read as zero before any successful input.

#### Scenario: Successful input advances last-input time
- **WHEN** input is successfully written to a live session
- **THEN** the last-input time becomes non-zero

#### Scenario: No input means zero last-input time
- **WHEN** no input has been written to a session
- **THEN** the last-input time reads as zero

### Requirement: In-place rerender restart

The runner SHALL support stopping a live session and queuing a restart at new PTY dimensions, resuming the same conversation so the agent re-emits at the new width. While the restart is in flight the task SHALL be reported as still alive (pending restart) so consumers do not tear down state, and SHALL NOT be reported idle. Queuing a rerender restart SHALL require a live session and SHALL refuse if one is already pending for that task.

#### Scenario: No live session cannot be kicked
- **WHEN** a rerender restart is requested for a task with no live session
- **THEN** a session-not-found error is returned

#### Scenario: Duplicate rerender rejected
- **WHEN** a rerender restart is requested while one is already pending for the task
- **THEN** an error is returned and no second restart is queued

#### Scenario: Pending restart counts as running, not idle
- **WHEN** a task has a queued rerender restart between exit and respawn
- **THEN** it is reported among running tasks and is not reported as idle

### Requirement: Stale-session reconciliation at startup

At startup, before any new sessions are started, the system SHALL flip every task persisted as in-progress to in-review, because such rows describe sessions that died with the prior process and the user should decide whether to resume or discard. Tasks in any other status SHALL be left unchanged.

#### Scenario: In-progress rows become in-review
- **WHEN** reconciliation runs and a task is persisted as in-progress
- **THEN** that task is updated to in-review

#### Scenario: Other statuses untouched
- **WHEN** reconciliation runs over tasks that are pending or complete
- **THEN** those tasks keep their status and are not counted as reconciled

### Requirement: Per-backend credential environment mapping

The system SHALL allow a backend definition to carry a credential environment
mapping from a target environment-variable name (set in the spawned agent's
child process) to a source descriptor resolved at spawn time. When building the
agent command, after assembling the inherited environment, forced terminal
variables, and task ID, the system SHALL resolve each mapping's source through
a pluggable secret-resolver seam and, for every source that resolves, append
`TARGET=value` to the child environment. A source that does not resolve SHALL
leave the target variable unset and SHALL be logged as a non-sensitive warning
that names only the variable, never the resolved value. The secret-resolver
seam SHALL default to reading the daemon's own process environment by the
source name, and SHALL be replaceable without modifying the command builder so
a future credential resolver (e.g. an `op`/1Password resolver) can be wired in.
The mapping SHALL hold only the target-to-source descriptor and SHALL NOT carry
a secret value; no resolved value SHALL be persisted or logged.

#### Scenario: Resolved source injected into child environment

- **WHEN** a backend defines a mapping `OPENAI_API_KEY -> HERA_OPENAI` and the
  secret resolver resolves `HERA_OPENAI` to a value
- **THEN** the built command's environment contains `OPENAI_API_KEY` set to that
  value

#### Scenario: Unresolved source leaves the variable unset and warns without the value

- **WHEN** a backend defines a mapping whose source does not resolve
- **THEN** the target variable is absent from the built command's environment
- **AND** a warning is logged that names the variable but contains no value

#### Scenario: Mapping carries no secret value

- **WHEN** a backend's credential mapping is stored or read back
- **THEN** it contains only target-to-source descriptors and no resolved secret
  value

#### Scenario: Resolver is pluggable

- **WHEN** an alternate secret resolver is installed in place of the default
  process-environment resolver
- **THEN** subsequent command builds resolve sources through the alternate
  resolver without any change to the command builder

