## ADDED Requirements

### Requirement: Dev stacks of finished tasks are stopped

The daemon SHALL periodically scan running dev-stack processes, map each to its worktree's task row by exact worktree path, and stop every dev-stack process of a task that is `complete` or archived and has no live session. The sweep SHALL be driven by the process table and task rows, not by session exits, so it covers a task whose session ended before the sweeper observed it. A stack SHALL be stopped only after the conditions have held continuously for a grace period of about one minute. Worktrees with no matching task row SHALL be left alone. A failed scan SHALL be logged and the tick skipped. The same holds, with no change to any sweeper state (first-tick status, exemptions, known sessions, grace counters; counters neither advance nor reset), when task rows cannot be read in full (a row that fails to scan is an error, not a omitted row) or the running-session list cannot be obtained (an RPC failure SHALL NOT be read as "no sessions"). Stops run one worktree at a time; task rows, live sessions and the setting SHALL be re-read immediately before each stop and before each escalation (the direct `SIGTERM` round and `SIGKILL`), and a stop SHALL be abandoned if the worktree is no longer eligible. The `SIGTERM`-to-`SIGKILL` grace on this path SHALL be 30 seconds (removal keeps its short grace), and daemon shutdown SHALL cancel a stop in progress without further signals.

#### Scenario: Stack of a task whose session already exited

- **WHEN** a task's session exited cleanly, the task became `complete`, and its worktree still has a running `mysqld`
- **THEN** after the grace period with no live session the sweeper stops that stack

#### Scenario: Archived task

- **WHEN** a task is archived, has no live session, and its worktree has running dev-stack processes
- **THEN** the sweeper stops them

#### Scenario: Unfinished task untouched

- **WHEN** a task is `in_progress`, `in_review` or `pending`
- **THEN** the sweeper does not signal its worktree's dev-stack processes

#### Scenario: Prefix-named sibling untouched

- **WHEN** a finished task `Sherlock/3b` has no live session and `Sherlock/3b-more` is unfinished with a running stack
- **THEN** only `Sherlock/3b`'s processes are signaled

#### Scenario: Scan failure

- **WHEN** the process scan cannot run
- **THEN** the sweeper logs it and signals nothing that tick

#### Scenario: Task rows unreadable or partial

- **WHEN** reading task rows fails or returns a row that cannot be scanned
- **THEN** the tick is skipped and nothing is stopped

#### Scenario: Running-session list unavailable

- **WHEN** the daemon cannot list running sessions (RPC failure)
- **THEN** the tick is skipped, and a failed first tick does not turn the startup backlog into exemptions

#### Scenario: Task resumed while an earlier stack is being stopped

- **WHEN** two worktrees are eligible at the start of a tick and the second task is resumed while the first stack is stopping
- **THEN** the second stack is not stopped

#### Scenario: Slow shutdown is allowed on the sweeper path

- **WHEN** the sweeper stops a stack whose database needs more than 5 seconds to shut down
- **THEN** it is not `SIGKILL`ed until 30 seconds after `SIGTERM`

### Requirement: Sweep is limited to managed, unclaimed worktrees

The sweeper SHALL consider only worktrees whose cleaned path is inside the argus data directory's `worktrees/` directory; any other path (such as a project's main checkout) SHALL be skipped and logged. When several task rows share one cleaned worktree path, the stack SHALL be stopped only if every such task is finished, has no live session or pending restart, and is not exempt.

#### Scenario: Main checkout is never a candidate

- **WHEN** a finished task's row holds a project's main checkout path and a `mysqld` runs against it
- **THEN** the sweeper does not signal it

#### Scenario: Shared path with an unfinished claimant

- **WHEN** two task rows share a worktree path, one `complete` and one `in_progress`
- **THEN** the stack is not stopped

#### Scenario: Shared path with a live finished claimant

- **WHEN** two task rows share a worktree path, both finished, and one has a live session
- **THEN** the stack is not stopped

#### Scenario: Hand-started stack in a finished worktree

- **WHEN** a stack runs in a managed worktree of a finished task with no session, however it was started
- **THEN** it is stopped after the grace period, unless `devstack.stop_finished` is false

### Requirement: Deliberately resumed finished tasks are exempt

A finished task observed with a live session on any tick other than the sweeper's first after daemon start SHALL be exempt from the dev-stack sweep until the task becomes unfinished. The first tick after daemon start SHALL grant no exemption.

#### Scenario: Resumed task keeps its stack

- **WHEN** a `complete` task is resumed, its session runs on a later tick, and later exits
- **THEN** the sweeper never stops that task's dev stack while it stays finished

#### Scenario: Exemption cleared

- **WHEN** an exempt task becomes `in_progress` and later `complete` again with no live session
- **THEN** it is eligible for the sweep again

#### Scenario: Startup backlog

- **WHEN** the daemon starts and a finished task with no session has a running stack
- **THEN** it is swept without an exemption

### Requirement: Finished-stack sweep is configurable

The sweep SHALL be controlled by the boolean `devstack.stop_finished` in `config.toml`, default true, read on every tick. When false the sweeper SHALL signal nothing; removal-time teardown is unaffected. If `config.toml` cannot be read or parsed, the setting is unknown (the loader silently falls back to defaults, i.e. on) and the sweeper SHALL stop nothing and log why.

#### Scenario: Disabled

- **WHEN** `devstack.stop_finished = false`
- **THEN** no dev-stack process is signaled by the sweeper

#### Scenario: Unparseable config.toml

- **WHEN** `config.toml` contains `stop_finished = false` and a syntax error elsewhere
- **THEN** the sweeper stops nothing

## MODIFIED Requirements

### Requirement: Dev-stack teardown before removal

`RemoveWorktree` SHALL stop any devbox-managed dev-stack process (`process-compose`, `mysqld`, `redis-server`, `postgres`, or `caddy`, including a `devbox run --` wrapper around one) whose command line embeds the worktree's exact path, before removing the worktree's git registration or files. This step SHALL apply uniformly to every worktree-removal call site, since all of them funnel through `RemoveWorktree`.

The teardown SHALL first send `SIGTERM` to supervisor processes (`process-compose`, `mysqld_safe`, `devbox run` wrappers) and wait a short bounded grace period, so a supervisor configured to restart services cannot respawn them. It SHALL then rescan, send `SIGTERM` to every remaining matched process directly, wait a bounded grace period, and send `SIGKILL` to any matched process still running, supervisors first. It SHALL NOT assume that signaling a supervisor alone stops its services. A process SHALL match only when its executable (argv[0], or the command a `devbox run [flags] [--] [env] [VAR=val ...] <binary>` wrapper launches) is a known dev-stack binary and the worktree path appears as a whole path segment in its command line; a process that merely mentions a binary name or path in other arguments SHALL NOT match. `sh -c` wrappers are not matched themselves; the binary they launch matches directly.

Because scan output is line-based while a process's arguments can contain raw newlines (an agent's multi-line prompt), every scanned entry is only a candidate: immediately before each signal the process's own live command line SHALL be re-read and re-classified, and if the re-read fails, the process is gone, or it is no longer a dev-stack process of the same worktree, no signal SHALL be sent and the refusal SHALL be logged. A scan whose output cannot be read in full (including a line beyond a hard cap) SHALL be a failed scan, and lines longer than 64 KiB SHALL be handled. Every signal, refusal and failure SHALL be logged to the daemon's log, not only to the TUI log.

This step SHALL be best-effort: a failure to scan for or signal a process SHALL be logged and SHALL NOT block or fail worktree removal.

#### Scenario: Dev-stack process for this worktree is stopped

- **WHEN** a worktree being removed has a running dev-stack process whose command line references that worktree's exact path
- **THEN** `RemoveWorktree` sends `SIGTERM` to it before removal, and `SIGKILL`s it if still running after the grace period

#### Scenario: Supervisor is stopped before its services

- **WHEN** a `process-compose` supervisor and its `mysqld` child both run for the worktree
- **THEN** the supervisor receives `SIGTERM` first, and any service still running afterwards is signaled directly

#### Scenario: Supervisor-only signal is not assumed sufficient

- **WHEN** a `process-compose` supervisor and a `redis-server` or `caddy` child run for the worktree and the child survives the supervisor's shutdown
- **THEN** the child is signaled directly

#### Scenario: Unsupervised wrappers and launchd-parented processes

- **WHEN** a worktree has `devbox run -- mysqld --datadir=<worktree>/tmp/mysql-data` or a `mysqld` whose parent is launchd with that datadir
- **THEN** both are matched and stopped

#### Scenario: Multi-line prompt cannot produce a phantom target

- **WHEN** an agent's multi-line prompt quotes a line such as `48213 /nix/.../mysqld --datadir=<worktree>/d`, so scan output lists pid 48213, whose own live command line is something else
- **THEN** pid 48213 is not signaled and the refusal is logged

#### Scenario: Long command line does not hide later processes

- **WHEN** a matching process has a command line longer than 64 KiB and other dev-stack processes follow it in the scan output
- **THEN** the later processes are still found

#### Scenario: Argument mention does not match

- **WHEN** another process (for example an agent) has the worktree path and the word `postgres` only inside prompt arguments
- **THEN** it is not signaled

#### Scenario: Sibling worktree with a prefix name is not affected

- **WHEN** worktree `Sherlock/3b` is being removed and a dev-stack process runs for a still-live `Sherlock/3b-more`
- **THEN** that process is not signaled

#### Scenario: No dev-stack process running is a no-op

- **WHEN** the worktree has no matching process
- **THEN** removal proceeds immediately

#### Scenario: Scan failure never blocks removal

- **WHEN** the process scan cannot run
- **THEN** the failure is logged and removal proceeds as if nothing was found
