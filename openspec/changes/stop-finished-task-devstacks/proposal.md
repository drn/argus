## Why

A task that finishes leaves its worktree on disk, and anything an agent started inside it keeps running. On 2026-11-04 the host sat at load ~11, largely from `mysqld` restart loops in worktrees whose tasks were already `complete`; on Oct 30, 12 of 14 running MySQL instances belonged to `complete` tasks. `stopDevStackFor` has exactly one caller (`RemoveWorktree`, `internal/agent/cleanup.go:175`), and `stop-finished-task-sessions` stops the agent *session* of a finished task but never its dev stack. `complete` tasks keep their worktree, so their stacks run indefinitely.

## What Changes

- A new daemon sweeper scans for dev-stack processes, maps each to its worktree's task row, and stops the stack of any task that is `complete` or archived and has no live session. It works from the process table, so it also covers tasks whose session exited before the session reaper ever saw them (clean exit -> `transitionTaskOnExit` marks `complete`; `hera_accept`; prune).
- A stack whose finished task has had a live session since the sweeper started (a deliberate resume) is left alone until the task becomes unfinished again.
- `stopDevStackFor` is reordered to stop supervisors (`process-compose`, `devbox run --` wrappers) before their children, so `restart: "always"` cannot race the kill. Every matched process is still signaled directly.
- Process matching is tightened to the executable (argv[0], or the token after `devbox run --`), not a substring of the whole command line, because the auto-stop widens the blast radius of a false match.
- `argus doctor` additionally flags dev stacks whose task is finished (today it flags only stacks whose worktree is gone).
- A config setting (`on` by default) gates the sweeper.

## Capabilities

### New Capabilities

### Modified Capabilities
- `worktree-management`: new requirement for stopping the dev stack of a finished task; the existing dev-stack teardown requirement gains supervisor-first ordering and executable-based matching.
- `binary-coherence`: the dev-stack diagnostic also reports stacks belonging to finished tasks.

## Impact

- `internal/agent/devstack.go` (ordering, matching, exported stop helper), new `internal/daemon/devstackreap.go`, `daemon.go` (start the loop), `cmd/argus/doctor.go` + `internal/doctor/devstack.go`, config schema, tests.
- Daemon-only. No supervisor spawn/stream surface, RPC, or ProtocolVersion change (see design D5).
- No REST, SSE or wire change, so no web or macOS change.
