## Context

Verified findings (path:line against base `d44efd3c`):

- `stopDevStackFor` callers: only `internal/agent/cleanup.go:175` (inside `RemoveWorktree`).
- `internal/daemon/finishedreap.go:59-108`: `tick` iterates `runner.RunningAndIdle()` running sessions only, then `runner.Stop`. It never touches dev stacks.
- A session that already exited is invisible to it: `transitionTaskOnExit` (`internal/daemon/daemon.go:516`, clean exit -> `SetStatus(StatusComplete)` at `:540`) marks the task `complete` after the session is gone. `hera_accept` and the gater complete tasks the same way.
- `internal/agent/devstack.go:178-200`: `stopDevStackFor` SIGTERMs every match in one loop in pgrep (PID) order, sleeps 5s, rescans, SIGKILLs survivors. There is no supervisor-first step.
- `ScanDevStackProcesses` (`devstack.go:95`) is `pgrep -f -l 'process-compose|mysqld|redis-server|postgres|caddy'`, parsed by `parsePgrepOutput`; it never looks at PPID.
- `cmd/argus/doctor.go:341-364` + `internal/doctor/devstack.go` flag only processes whose worktree path no longer exists.
- `db.Tasks()` (`internal/db/tasks.go:74`) returns all rows including archived, each with `Worktree`.

## Goals / Non-Goals

**Goals:**
- Stop dev stacks of `complete`/archived tasks with no live session, regardless of how/when the session ended.
- Never touch a stack of a deliberately resumed task.
- Make `argus doctor` surface finished-task stacks.

**Non-Goals:**
- Per-task port allocation or exporting `ARGUS_PORT`/`CONDUCTOR_PORT` (dropped), and any Sketch-repo change.
- Cleaning up the live stacks on the current host (Aaron's decision).
- Stopping stacks of `in_review`, `in_progress`, or `pending` tasks.
- Frontend parity: no REST/SSE/wire-visible change. The sweeper is daemon-internal, the setting is `config.toml`-only (like usage routing), and doctor is CLI-only. TUI, web and macOS are unaffected; nothing to follow up.

## Decisions

### D1. Scan-driven sweeper, not session-driven

Answers Q1. Stopping the stack next to `runner.Stop` in the reaper would miss tasks whose session exited first (the `daemon.go:540` path). Instead a daemon loop (`runDevStackReaper`, 10s tick, the same cadence as the session reaper so the resume signal in D3 is seen promptly; the process scan only runs when some worktree could qualify) calls `ScanDevStackProcesses`, groups processes by worktree, resolves each worktree to a task via `db.Tasks()` (cleaned `Worktree` equality, exact, same prefix-sibling guard as today), and acts on finished tasks. Worktrees matching no task row are skipped (doctor reports those; orphan sweep owns them). Scan error -> log, skip tick (unknown, not none).

### D2. Eligibility and grace

A worktree's stack is stopped when, on six consecutive ticks (~60s): its task is `complete` or archived, it has no live session (`runner.Running()`, which includes a pending kick-restart), and it is not exempt. The grace gives a just-finished agent (or `hera_accept` check-in) time to wrap up and avoids racing a restart. Counters reset if any condition fails.

### D3. Deliberate-resume protection

Answers Q4, mirroring the reaper's `exempt`. The sweeper keeps in-memory state per task. If a session is observed live while the task is finished, and it was not the first tick, the task is exempt: its stack is never stopped by the sweeper until the task becomes unfinished (which clears the exemption). The first tick after daemon start is the backlog sweep and grants no exemption. Accepted edge: a resumed session that starts and exits entirely between two ticks is not noticed; a daemon restart forgets exemptions (same accepted edge as the reaper).

### D4. Stop order and matching (Answer to Q2, Q3)

Q2 finding: today it does NOT stop the supervisor first. It signals in PID order in one pass, so `process-compose` (`restart: "always"`, no limit) can respawn a `mysqld` between its SIGTERM and the supervisor's own shutdown, and a SIGKILLed child can be restarted if the supervisor is killed later in the list. New `stopDevStackProcs`: (1) SIGTERM supervisors (`process-compose`, `devbox` wrappers) and wait up to a short grace; (2) rescan, SIGTERM every remaining process; (3) grace, rescan, SIGKILL survivors supervisors-first. Every process is still signaled directly (the existing `worktree-management` requirement stands). `RemoveWorktree` uses the same helper.

Q3 finding: the path regex already extracts the worktree from `--datadir=<wt>/tmp/mysql-data`, and the scan never uses PPID, so both the `devbox run -- mysqld --datadir=...` wrapper and a launchd-parented `mysqld` are caught, provided the worktree path appears in argv. A `mysqld` launched with only relative paths or `--defaults-file` elsewhere is not caught (no cwd lookup); that gap is accepted and documented. I could not confirm the live Oct 30 shapes: `ps`/`pgrep` are blocked in this worker sandbox, so this is code analysis, to be covered by parser tests using those exact lines.

Matching tightening: `pgrep -f` plus `strings.Contains(cmdline, name)` also matches any process whose arguments merely mention e.g. `postgres` and a worktree path, such as an agent whose prompt/args contain both. With auto-stop that could SIGTERM a live agent. Matching moves to the executable basename of argv[0] (or the first token after `devbox run --`).

### D5. Daemon, not supervisor

Answers Q5. The daemon owns the DB (task rows), the runner view and the loops; the supervisor owns PTYs only and has no task state. The sweeper runs in the daemon, signals host PIDs directly (the daemon is unsandboxed), and needs no RPC. `internal/agent/devstack.go` is not in `SupervisorSpawnPaths` (`internal/daemon/surface.go:403`) and nothing the supervisor executes changes, so no surface digest re-record, ProtocolVersion bump, or stream-surface bump. Implementation must confirm this with the surface test.

### D6. Setting

Recommendation: yes, add an on/off setting, default ON. The sweeper kills processes the user may be relying on, which is the case where a setting is warranted, and the master switch restores today's behavior exactly. Key `devstack.stop_finished` (bool, default true) in `config.toml`, live-read each tick (no restart). It gates only the sweeper; removal-time teardown is unconditional as today.

### D7. Doctor

`gatherFinishedStacks` is a second classification: process maps to a managed worktree (under `<DataDir>/worktrees/`) whose claimant task rows are ALL `complete`/archived (any session state; doctor cannot see daemon memory) and reports it as "finished-task stack" with task name; task rows come from a read-only DB open (`db.ReadTaskWorktreesReadOnly`, never migrates). A worktree claimed by an unfinished task is not flagged. Listed separately from the "worktree gone" list. Advisory-only, never affects the exit code.

### D8. Logging

Follow the reaper: `slog` plus `uxlog.Log("[devstackreap] ...")` for stops, skips (exempt, grace, no task, scan failure) and signal failures. The daemon does not call `uxlog.Init`, so (as with the reaper) uxlog lines are no-ops there and `slog` is the daemon's real trail; record this in gotchas.

### D9. Managed-worktree-only (guard 1)

Findings: `Task.Worktree` is stamped in one production place, `agent.CreateAndStart` (`internal/agent/create.go:294`) from `CreateWorktree` -> `WorktreeDir` = `<DataDir>/worktrees/<project>/<task>` (`worktree.go:119`). `api/raw.go:97` preserves the existing value on PUT; the other `Worktree:` literals are the hera role view, a codexcost lookup shim, and `cmd/argus-test-server`. Read-only check of the live DB: 140 tasks, 140 under `.argus/worktrees/`, 0 empty, 0 elsewhere. So today no row holds a canonical checkout. The code does not prevent one (a hand-edited row, a test-server style row), and `worktreePathPattern` only extracts `.argus|.claude/worktrees/<project>/<task>` paths, so a main-checkout `mysqld` is not even extractable. The sweeper adds an explicit second guard: a worktree qualifies only if its cleaned path is inside `<DataDir>/worktrees/` (strictly `db.DataDir()`, narrower than `IsWorktreeSubdir`, which also accepts legacy `.claude/worktrees`). Anything else is skipped and logged (`not-managed`). The main `thanx/sketch` MySQL can never be a candidate.

### D10. Shared worktree path (guard 2)

If several task rows resolve to the same cleaned worktree path, the stack is stopped only when ALL of them are finished, none has a live session or pending restart, and none is exempt (D3). Any unfinished or live claimant blocks the stop.

### D11. Hand-started stack in a finished task's worktree (guard 3 - for Aaron to rule on)

A person who starts a dev stack by hand in the worktree of a `complete`/archived task, with no agent session, has it stopped after about a minute (six consecutive ticks). The sweeper cannot tell a hand-started stack from a leaked agent-started one: both are just processes under a finished task's worktree. Proposed: accept this. The protections are D3 (a resumed session exempts the task) and the off switch `devstack.stop_finished = false`. Detection alternatives (process start time vs task end time, controlling-terminal check) are fragile and are not proposed. Aaron to confirm accept-as-is.

### D12. Verification of the process shapes

The two Oct 30 shapes (`devbox run -- mysqld --datadir=<wt>/tmp/mysql-data` and a launchd-parented `mysqld`) are verified by parser tests only. Live confirmation against real processes is a post-deploy step (e.g. `argus doctor` listing them).

### D13. Review hardening (PR #1109 review)

- **Verify before signaling (D4 addendum).** pgrep output is line-based but argv can hold raw newlines (an agent's multi-line prompt), so a scanned entry can be a phantom PID. Immediately before every signal (SIGTERM and SIGKILL, sweeper and removal) the PID's own argv is re-read (`ps -ww -p <pid> -o command=`), re-classified, and its worktree compared; failure/gone/mismatch means no signal and a logged refusal. Scan lines over 64 KiB are handled (16 MiB hard cap) and a scanner error is a failed scan.
- **Wrapper shapes (D4 addendum).** `mysqld_safe` is a supervisor (it respawns `mysqld`); `devbox run mysqld` and `devbox run -- env A=1 mysqld` are accepted. `sh -c ...` is deliberately not matched: its child matches directly.
- **Everything the sweeper reads can fail and a failed tick changes no state.** Task rows via `db.TasksChecked` (scan/iteration errors are errors), live sessions via the client's `RunningChecked` (no wire change: same `ListSessions` RPC, the error is just no longer folded to an empty list; the in-process runner cannot fail), and config via `db.ConfigFileErr()` read right after `Config()`. A failed tick returns before touching `ticked`, exemptions, known sessions or counters (counters held).
- **Unparseable config.toml stops nothing.** The loader falls back to defaults (on) on a parse error, so `stop_finished = false` plus a typo would silently re-arm the kill. Decision: unknown setting = no stops, logged once per distinct error.
- **Re-read before each stop and escalation, 30s kill grace.** Stops run sequentially and can each take up to ~32s, so state is re-read right before each stop and via `StopOptions.Proceed` before the direct SIGTERM round and before SIGKILL; `StopOptions.Cancel` (daemon shutdown) aborts any grace wait without further signals. The sweeper keeps the worktree and a resume reuses the datadir, hence 30s instead of removal's 5s.
- **Logging.** Signals, refusals and scan failures go through `slog` (the daemon's real trail) as well as uxlog.

### D14. Accepted edge: the resume gap widens to the length of the sweeper's busy time (re-review)

The D3 gap ("a resumed session that starts and exits entirely between two ticks is not noticed") is no longer 10s. The loop is single-threaded and each stop can take ~32s or more (2s supervisor grace + 30s kill grace), so no tick runs while an earlier worktree is being stopped; a task resumed and exited entirely within that window is never exempted and is stopped later in the same tick. Observed in a probe (task B resumed and exited during A's stop, then stopped). Accepted: it is rare, and it fails toward stopping a stack that can simply be restarted. No code change.

## Risks / Trade-offs

- A user who starts a stack by hand on a finished task without resuming a session loses it (D11). Mitigated by the setting and D3.
- Tighter matching could miss an odd wrapper (e.g. a custom launcher). Accepted: false negatives leak a stack, false positives kill an agent.
- The ~60s grace delays cleanup; acceptable.
