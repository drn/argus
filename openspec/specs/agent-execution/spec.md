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

The system SHALL pin a new session's ID for backends that accept a start-time session identifier (Claude-style), and SHALL omit it for backends that do not (codex, pi, opencode). On resume, the system SHALL reconnect to an existing conversation using the backend's own resume mechanism, and SHALL drop the prompt because the conversation is reloaded. The opencode resume form appends `--session <id>` (the same shape as pi). When resume is requested for a Claude-style, pi, or opencode backend but no session ID is known, the system SHALL start a fresh session rather than emit an empty resume flag.

#### Scenario: New Claude-style session pins the ID
- **WHEN** a new (non-resume) session is built for a Claude-style backend that has a session ID
- **THEN** the command includes a start-time session-id flag carrying that ID

#### Scenario: New codex/pi/opencode session never pins the ID
- **WHEN** a new session is built for a codex, pi, or opencode backend even though a session ID is present
- **THEN** the command omits any start-time session-id flag

#### Scenario: Resume reconnects and drops the prompt
- **WHEN** a resume command is built for a backend with a known session ID
- **THEN** the command uses that backend's resume form carrying the ID and does not append the prompt

#### Scenario: opencode resumes with its session flag
- **WHEN** a resume command is built for an opencode backend with a known session ID
- **THEN** the command appends `--session` followed by the safely quoted session ID and omits the prompt

#### Scenario: Resume with no session ID starts fresh
- **WHEN** resume is requested for a Claude-style, pi, or opencode backend but no session ID is set
- **THEN** the command is the plain base command with no resume flag

### Requirement: Post-exit session-ID capture for capture-style backends

The system SHALL recover a session identifier after exit for backends that mint their ID externally (codex, pi, opencode) by reading the backend's own state for the task's worktree, returning the most recently updated matching session. For backends that pin their ID at start (Claude-style) or are unrecognized, the system SHALL report no captured ID without error. A captured codex ID SHALL be validated as a UUID before being returned, and a captured opencode ID SHALL be validated against the `ses_` identifier format before being returned.

For opencode the system SHALL resolve the data directory from `XDG_DATA_HOME` (falling back to `~/.local/share`) under `opencode`, and SHALL locate the session whose recorded working directory equals the task's worktree, choosing the most recently updated one. It SHALL read the current SQLite store (`opencode.db`, table `session`) first and fall back to the legacy JSON session files when the SQLite store is absent or yields no match. When no matching session is found in either store, the system SHALL report no captured ID (fail open) so the conversation simply starts fresh rather than failing the launch.

#### Scenario: Codex ID recovered from its state for the worktree
- **WHEN** capture runs for a codex-backed task whose worktree has a recorded session
- **THEN** the most-recently-updated session ID for that worktree is returned

#### Scenario: Pi ID recovered from the newest session file
- **WHEN** capture runs for a pi-backed task whose worktree has session files
- **THEN** the UUID from the newest matching session file is returned

#### Scenario: opencode ID recovered from the SQLite store for the worktree
- **WHEN** capture runs for an opencode-backed task whose worktree has a row in the opencode SQLite session store
- **THEN** the id of the most-recently-updated session whose directory equals the worktree is returned

#### Scenario: opencode ID recovered from legacy JSON when no SQLite store
- **WHEN** capture runs for an opencode-backed task and only the legacy JSON session files exist
- **THEN** the id of the most-recently-updated JSON session whose directory equals the worktree is returned

#### Scenario: opencode capture fails open when nothing matches
- **WHEN** capture runs for an opencode-backed task whose worktree has no recorded session in either store
- **THEN** no captured ID is produced and the launch is unaffected (the next start is a fresh session)

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

### Requirement: Orphaned Claude Code background-session reaping on stop

When the runner stops a task's session, it SHALL additionally check, in a background goroutine that does not delay the stop call's return, whether Claude Code itself is still hosting a live background session under that task's worktree directory — a session that was detached (backgrounded) to Claude Code's own per-user supervisor process and is therefore unreachable by the signal the runner just sent to its own tracked process. Any such session SHALL be stopped via Claude Code's own CLI.

Only sessions Claude Code reports as backgrounded, and currently alive, SHALL be stopped by this check; the task's own tracked interactive session SHALL never be targeted by it. The check SHALL identify a background session by the short id Claude Code's CLI assigns it, never by a full session UUID.

Every failure in this check — the CLI being unavailable, a listing failure, or a stop failure — SHALL be logged and SHALL NOT alter the runner's stop call's return value or error, and SHALL NOT block or delay it.

#### Scenario: No background session under the worktree

- **WHEN** a task's session is stopped and Claude Code reports no background session under that task's worktree
- **THEN** the stop call returns exactly as it did before this check existed, and nothing further happens

#### Scenario: A backgrounded, still-alive session is stopped

- **WHEN** a task's session is stopped and Claude Code reports a background session under that task's worktree that is still alive
- **THEN** the runner additionally stops that background session via Claude Code's CLI, using its short id, and logs the outcome

#### Scenario: The task's own tracked session is never targeted

- **WHEN** Claude Code reports the task's own interactive session under that task's worktree
- **THEN** the check does not attempt to stop it, since it is not reported as backgrounded

#### Scenario: An already-exited background entry is skipped

- **WHEN** Claude Code reports a background session under the task's worktree that is no longer alive
- **THEN** the check does not attempt to stop it

#### Scenario: The check never delays or fails the stop call

- **WHEN** the Claude Code CLI is unavailable, or listing or stopping a background session fails
- **THEN** the failure is logged and the runner's stop call's return value and timing are unaffected

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

Every input write SHALL carry an explicit, mandatory origin — a genuine human keystroke, or input the system itself injected (reliable-notify delivery, a coordinator bounce instruction, a live emulator's auto-answered terminal capability query). There SHALL be no default origin: the write operation's signature requires the caller to state one, so a caller cannot silently fall back to the wrong classification. A human-origin write SHALL advance both the last-input timestamp and a separate last-user-input timestamp; a system-origin write SHALL advance only the last-input timestamp, never the last-user-input timestamp, so system-injected input can never be mistaken for the user answering a prompt.

#### Scenario: Successful input advances last-input time
- **WHEN** input is successfully written to a live session
- **THEN** the last-input time becomes non-zero

#### Scenario: No input means zero last-input time
- **WHEN** no input has been written to a session
- **THEN** the last-input time reads as zero

#### Scenario: Human-origin input advances both timestamps
- **WHEN** input is successfully written with human origin
- **THEN** both the last-input time and the last-user-input time become non-zero

#### Scenario: System-origin input advances only the last-input time
- **WHEN** input is successfully written with system origin
- **THEN** the last-input time becomes non-zero and the last-user-input time remains unchanged from before the write

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
the secrets-resolution registry (see `secrets-resolution`) and, for every
source that resolves, append `TARGET=value` to the child environment. A source
that does not resolve SHALL leave the target variable unset and SHALL be
logged as a non-sensitive warning that names only the variable, never the
resolved value. A bare string or `env://`-prefixed source SHALL resolve
against the daemon's own process environment, unchanged from prior behavior;
a `keychain://`- or `op://`-prefixed source SHALL dispatch to the
corresponding resolver in the registry. Resolution SHALL happen fresh, in
whichever process actually calls the command builder (the daemon in
in-process mode, or the session-supervisor process when supervisor mode is
enabled) — never assumed to have propagated from another process's
environment via fork inheritance. The mapping SHALL hold only the
target-to-source descriptor and SHALL NOT carry a secret value; no resolved
value SHALL be persisted or logged.

#### Scenario: Resolved source injected into child environment

- **WHEN** a backend defines a mapping `OPENAI_API_KEY -> HERA_OPENAI` and the
  secret resolver resolves `HERA_OPENAI` to a value
- **THEN** the built command's environment contains `OPENAI_API_KEY` set to that
  value

#### Scenario: Scheme-prefixed source dispatches through the registry

- **WHEN** a backend defines a mapping whose source is
  `keychain://some-service` or `op://vault/item/field`
- **THEN** the command builder resolves it through the secrets-resolution
  registry's matching resolver rather than reading it as a bare environment
  variable name

#### Scenario: Unresolved source leaves the variable unset and warns without the value

- **WHEN** a backend defines a mapping whose source does not resolve
- **THEN** the target variable is absent from the built command's environment
- **AND** a warning is logged that names the variable but contains no value

#### Scenario: Mapping carries no secret value

- **WHEN** a backend's credential mapping is stored or read back
- **THEN** it contains only target-to-source descriptors and no resolved secret
  value

#### Scenario: Resolution happens in whichever process builds the command

- **WHEN** supervisor mode is enabled and the session-supervisor process
  calls the command builder
- **THEN** the source is resolved directly inside the session-supervisor
  process, not assumed to already be present via inheritance from the
  daemon or any other process that may have forked it

#### Scenario: Resolver is pluggable

- **WHEN** an alternate secret resolver is installed in place of the default
  process-environment resolver
- **THEN** subsequent command builds resolve sources through the alternate
  resolver without any change to the command builder

### Requirement: Archetype carried at task creation

The system SHALL accept an optional `archetype` at the single fresh-task creation chokepoint
(`agent.CreateAndStart`) and persist it on the created task, so that any spawn path — interactive
new-task creation, hera worker spawn, and freelance creation — can set a task's archetype uniformly.
When no archetype is supplied, the task SHALL carry an empty archetype and profile-based resolution
SHALL NOT apply.

#### Scenario: Archetype persisted on the task

- **WHEN** a task is created through `CreateAndStart` with `archetype = "security_review"`
- **THEN** the created task carries `security_review` as its archetype

#### Scenario: Absent archetype leaves the task unmarked

- **WHEN** a task is created with no archetype
- **THEN** the task's archetype is empty and no profile is consulted for its model resolution

### Requirement: Profile environment exported alongside the task ID

When a profile resolves for a spawned agent, the system SHALL export `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`,
and `ARGUS_MODEL` into the agent command's environment, in addition to the existing task-ID export, so
in-repo skills can read the active profile and archetype. When no profile resolves, these variables
SHALL be omitted.

#### Scenario: Profile env present when a profile resolves

- **WHEN** a command is built for a task whose archetype resolves a valid bound profile
- **THEN** the command environment exports `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, and `ARGUS_MODEL`

#### Scenario: Profile env absent without a profile

- **WHEN** a command is built for a task that carries no archetype or whose profile does not resolve
- **THEN** the command environment contains none of the profile variables

### Requirement: Resume-time session-ID refresh for Claude backends

Before resuming a Claude-style backend, the system SHALL re-derive the most-recently-updated transcript UUID for the task's worktree and persist it as the task's session ID, so the resume targets the newest in-place session rather than a stale earlier one. This is the resume-time analog of post-exit capture, required because sessions that idle or lose their stream (hera workers) never reach the post-exit capture and so accrue newer transcripts while their recorded session ID stays pinned to the create-time value.

The refresh SHALL apply only to Claude-style backends; for codex, pi, opencode, and unrecognized backends it SHALL be a no-op so their resume semantics are unchanged. The refresh SHALL be a no-op — leaving the recorded session ID intact and never blanking it — when the task has no worktree, has no prior session ID, the worktree has no transcript, or the newest transcript equals the recorded ID. When it does change the ID, it SHALL both update the in-memory task (so the immediate resume uses the new ID) and persist the change to the task row.

#### Scenario: Claude resume targets the newest transcript

- **WHEN** a Claude-backed task with a recorded session ID is about to resume and its worktree holds a newer transcript than the recorded ID
- **THEN** the task's session ID is refreshed to the newest transcript UUID and the resume uses that ID

#### Scenario: Non-Claude backends are untouched

- **WHEN** the resume-time refresh runs for a codex, pi, opencode, or unrecognized backend
- **THEN** the recorded session ID is left unchanged and no transcript scan influences the resume

#### Scenario: Zero-transcript worktree falls back to the existing ID

- **WHEN** the resume-time refresh runs for a Claude-backed task whose worktree holds no transcript
- **THEN** the recorded session ID is left intact (never blanked) and the resume proceeds with it

#### Scenario: No prior session ID is not fabricated

- **WHEN** the resume-time refresh runs for a task that has no recorded session ID
- **THEN** no session ID is derived or written and the launch behaves as a fresh start

### Requirement: Builtin routing content auto-injected via system prompt file

The system SHALL make argus's builtin hera-coordination and argus-task-management routing content available to every spawned Claude-backend session by materializing it to a stable file under `~/.argus` and appending Claude Code's `--append-system-prompt-file <path>` flag to the command. This SHALL apply unconditionally to every session kind (coordinator, worker, freelance) and SHALL NOT be gated on `cfg.Hera.Enabled` or any per-project/per-task configuration — the injected content is self-gating at read time (each section opens with an `ARGUS_TASK_ID`/`$PWD` sandbox-residency check), so appending it to a non-argus-context spawn is inert. Materialization failure SHALL be logged and SHALL NOT block session launch; the flag is appended only when materialization succeeds.

#### Scenario: Claude backend receives the routing system-prompt flag

- **WHEN** a command is built for a Claude backend and routing-content materialization succeeds
- **THEN** the command appends `--append-system-prompt-file` followed by the materialized path

#### Scenario: Non-Claude backends are unaffected

- **WHEN** a command is built for a non-Claude backend (codex, pi, opencode, or a bare custom command)
- **THEN** no `--append-system-prompt-file` flag is appended, regardless of whether routing-content materialization would have succeeded

#### Scenario: Materialization failure does not block launch

- **WHEN** the routing content cannot be materialized (e.g. a filesystem error)
- **THEN** command construction still succeeds without the `--append-system-prompt-file` flag, and the failure is logged

### Requirement: Forced build/test cache environment redirect

The system SHALL force `GOCACHE` and `PLAYWRIGHT_BROWSERS_PATH` onto every spawned agent's environment, pointed outside `~/Library/{Application Support,Containers,Caches}`, so that Go builds and Playwright browser downloads triggered by an agent (or any process it forks) do not write under the macOS TCC-gated tree and trigger the "access data from other apps" prompt. This applies unconditionally, with no per-project or per-backend opt-out, mirroring the existing forced `TERM`/`COLORTERM` environment.

#### Scenario: Go build cache redirected for every spawned agent
- **WHEN** a command is built for any task, regardless of backend or worktree
- **THEN** the command's environment sets `GOCACHE` to a path under `~/.argus/cache/` rather than the tool's own default under `~/Library/Caches`

#### Scenario: Playwright browser cache redirected for every spawned agent
- **WHEN** a command is built for any task, regardless of backend or worktree
- **THEN** the command's environment sets `PLAYWRIGHT_BROWSERS_PATH` to a path under `~/.argus/cache/` rather than the tool's own default under `~/Library/Caches`

#### Scenario: Redirect applies even when the parent environment already sets these variables
- **WHEN** the parent process environment already defines `GOCACHE` or `PLAYWRIGHT_BROWSERS_PATH`
- **THEN** the spawned agent's environment uses argus's forced value, not the inherited one

### Requirement: Configurable shared cache directory redirection

The system SHALL support a project-configurable mapping (`cache_dirs`) from a
target environment-variable name to a subdirectory created under
`~/.argus/cache/`, merged from a global config-level mapping and a
per-project mapping (a per-project entry overrides a shared key and adds any
key the global mapping doesn't define). For every entry in a task's resolved
mapping, the system SHALL create the resolved subdirectory if it does not
already exist and SHALL export `TARGET=<resolved-dir>` on the spawned
agent's environment. This mapping SHALL hold directory paths only, never a
secret value. An entry whose target is empty or contains `=`, or whose
subdirectory is absolute or escapes the cache root via a `..` path segment,
SHALL be skipped (logged, not fatal) rather than exported or used to create a
directory outside the cache root.

#### Scenario: Global cache_dirs entry exported and its directory created

- **WHEN** a command is built for a task whose config defines a global
  `cache_dirs` entry mapping a target env var to a subdirectory name
- **THEN** the spawned agent's environment sets that target to a path under
  `~/.argus/cache/<subdirectory>`, and that directory exists on disk

#### Scenario: Per-project entry overrides a shared key

- **WHEN** a task's project defines a `cache_dirs` entry for the same target
  env var as the global `cache_dirs` mapping, with a different subdirectory
- **THEN** the spawned agent's environment uses the project's subdirectory,
  not the global one

#### Scenario: Per-project entry adds a project-only key

- **WHEN** a task's project defines a `cache_dirs` entry for a target env var
  the global mapping does not define
- **THEN** the spawned agent's environment includes that target, resolved
  under `~/.argus/cache/`

#### Scenario: No cache_dirs configured changes nothing

- **WHEN** neither the global config nor the task's project defines any
  `cache_dirs` entry
- **THEN** no additional cache-directory environment variables are exported,
  beyond the always-forced `GOCACHE`/`PLAYWRIGHT_BROWSERS_PATH`

#### Scenario: Invalid entry is skipped, not fatal

- **WHEN** a resolved `cache_dirs` entry has an empty target, a target
  containing `=`, or a subdirectory that is absolute or contains a `..`
  path segment
- **THEN** command construction still succeeds, that entry is not exported,
  and no directory is created for it outside `~/.argus/cache/`

