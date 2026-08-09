## Why

Per-worktree dev stacks (mysqld/redis/postgres/caddy, started inside a worktree via devbox + process-compose) are not torn down when argus removes that worktree. A live investigation found 8/8 running `process-compose` instances already reparented to launchd (their launching shell/session had exited) and confirmed 12 processes still running against worktree directories that had already been deleted from disk. Worktree removal never stops or even checks for an associated dev stack, so every cleaned-up task that ran dev services leaks them permanently — accumulating resource pressure over time with no way for the user to notice short of manually enumerating processes.

## What Changes

- Before a worktree's files are removed, scan for any running dev-stack process (`process-compose`, `mysqld`, `redis-server`, `postgres`, `caddy`) whose command line embeds that exact worktree path, SIGTERM everything found, wait a short grace period, then SIGKILL anything still alive. Hooked into the single choke point (`agent.RemoveWorktree`) that every worktree-removal path in the codebase already funnels through (task delete, hera cascade-nuke, both prune paths, orphan sweep, `CreateAndStart`'s failure-unwind).
- No dependency on the `devbox` CLI for the actual stop: an empirical test found `devbox services stop -c <dir>` can hang 15+ seconds on a worktree whose devbox environment isn't fully materialized ("Ensuring packages are installed..."), which is unacceptable for a step that must run before every worktree removal. Detection and termination use the OS process table directly (the worktree path is already embedded in every observed process's command line — no new pidfile or tracking mechanism needed).
- SIGTERM is sent directly to every matched process (not just the process-compose supervisor) — a prior manual investigation found the supervisor-to-children shutdown cascade is not reliable (a sibling `redis-server` and `caddy` under the same `process-compose` survived SIGTERM to their parent while `mysqld` did not), so nothing here assumes the cascade works.
- New advisory `argus doctor` check reporting any currently-running dev-stack process whose embedded worktree path no longer exists on disk — read-only, never auto-kills, consistent with the existing Stop-hook/diligence-profile-library doctor checks. Gives a standing way to notice drift (a pre-fix orphan, or a worktree deleted by some path other than argus) without a one-off script.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `worktree-management`: `RemoveWorktree` gains a mandatory teardown step — stop any devbox/process-compose dev stack rooted at the worktree path before the worktree's files are deleted.
- `binary-coherence`: `argus doctor` gains a new advisory check — surfaces dev-stack processes whose embedded worktree path no longer exists, mirroring the existing Stop-hook-registration and diligence-profile-library diagnostics (read-only, never affects the exit code).

## Impact

- `internal/agent/cleanup.go` (`RemoveWorktree`) — new teardown step at the top, before `git worktree remove` / `os.RemoveAll`.
- `internal/agent/devstack.go` (new) — process scan (pgrep-based, injectable exec seam) + SIGTERM/SIGKILL teardown logic, worktree-path extraction from command lines.
- `internal/doctor/devstack.go` (new) — pure classification + rendering for the new doctor check, following the `stophook.go` / `profilelib.go` pattern.
- `cmd/argus/doctor.go` (`runDoctor`) — wires the new gather step into the existing doctor output.
- `context/knowledge/gotchas/worktree.md` — new entry documenting the teardown hook and why the supervisor-cascade can't be trusted.
- No changes to CI, the Makefile, or any DB schema. No breaking changes.
