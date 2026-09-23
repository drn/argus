## Why

A long-running argus task session accumulates context the same way a Hera coordinator does, but there is no way to reset it without losing the task's place: `/compact` shrinks the transcript but keeps every prior turn in scope, and starting over means abandoning the worktree/branch/in-progress changes and re-explaining everything to a brand-new task. `recycle_coord` already solves exactly this for Hera coordinators (kill the session, start a fresh one on the identical task/worktree/branch, seeded with a handoff note), but it is reachable only through `hera_status`, which requires a live Hera role binding — a plain (non-Hera) task has no way to trigger it.

## What Changes

- A new `task_recycle` MCP tool: any argus task (Hera-bound or not) can call it with a `handoff_note` to reset its own context window while continuing the same work. The daemon kills the current session and starts a fresh, empty-context one on the identical task/worktree/branch, seeded with the handoff note plus the task's original prompt as background — the plain-task sibling of `recycle_coord`, with no hera role/binding to resolve.
- The restart is deferred until the calling session goes idle (a bounded one-shot poll, not hera's persisted-flag-plus-watcher design, since this is a single manually-triggered action with no need to survive a daemon restart) — the same self-kill race `recycle_coord`'s self-service trigger already guards against.
- The daemon's existing kill/restart-with-seed-prompt mechanics (`HeraRecycleRunner.Restart`'s tail: clear `SessionID`, set `Prompt`, hand off to `SessionRunner.Recycle`/`Start`) are extracted into a shared `restartSessionWithSeedPrompt` helper so both the hera and plain-task paths use the identical primitive.
- A new builtin skill, `task-recycle`, so an agent inside any argus task can manually trigger this (`/task-recycle` or on request) instead of running a built-in `/compact` that keeps the whole transcript in scope.

## Capabilities

### New Capabilities

- `task-recycle`: the `task_recycle` MCP tool, the idle-deferred kill/restart primitive for a plain task, and the seed-prompt assembly (handoff note + original prompt as background).

### Modified Capabilities

- `mcp-server`: new `task_recycle` tool, gated on both task management and the new recycler being wired.

## Impact

- **New code:** `internal/daemon/task_recycle.go` (`taskRecycleRunner`, `buildTaskRecycleSeedPrompt`); `internal/skills/builtin/task-recycle/SKILL.md`.
- **Modified code:** `internal/mcp/server.go` (`task_recycle` tool + handler, `TaskRecycler` seam); `internal/daemon/recycle.go` (extracts `restartSessionWithSeedPrompt`, reused by `HeraRecycleRunner.Restart`); `internal/daemon/daemon.go` (wires `SetTaskRecycler`).
- **Modified data:** none — no new `task_meta` keys or schema; the handoff note flows directly through the tool call into the seed prompt, never persisted.
- **No breaking changes.** Purely additive; existing tasks and Hera roles are unaffected.
