## 1. Daemon primitive

- [x] 1.1 Extract `restartSessionWithSeedPrompt` out of `HeraRecycleRunner.Restart` (`internal/daemon/recycle.go`), taking `*db.DB`, `agent.SessionRunner`, `cfgFn`, `*model.Task`, and the seed prompt directly.
- [x] 1.2 Add `taskRecycleRunner` (`internal/daemon/task_recycle.go`): `Recycle(taskID, handoffNote)` builds the seed prompt and schedules `awaitIdleAndRestart` in the background; `awaitIdleAndRestart` polls idleness (reusing `agent.ContentIdleTracker`, mirroring `HeraRecycleRunner.IsIdle`) up to a bounded timeout, then calls `restart` (stop stray jobs, hand off to `restartSessionWithSeedPrompt`).
- [x] 1.3 `buildTaskRecycleSeedPrompt(originalPrompt, handoffNote)`: handoff note first, original prompt marked as historical background (mirrors `hera.BuildRecycleSeedPrompt`'s ordering rationale).
- [x] 1.4 Unit tests: seed-prompt ordering; restart clears `SessionID`/sets `Prompt`/starts a fresh session (live-session and no-session cases); unknown-task errors; idle-wait gives up on timeout without restarting; no-session restarts immediately; `Recycle` end-to-end with a synchronous `spawn` override.

## 2. MCP surface

- [x] 2.1 `TaskRecycleInput` / `TaskRecycler` types and `SetTaskRecycler` setter (`internal/mcp/server.go`), mirroring the `HeraReviveInput`/`HeraReviver` pattern.
- [x] 2.2 `task_recycle` tool definition + `taskRecycleEnabled()` gate (recycler wired AND task management wired) + `toolTaskRecycle` handler (resolves via `id`/`cwd`, validates/caps `handoff_note`, calls the recycler).
- [x] 2.3 Wire `mcpSrv.SetTaskRecycler(...)` in `internal/daemon/daemon.go` alongside the existing `SetTaskManager` call.
- [x] 2.4 Tests: tool listed only when both `SetTaskManager` and `SetTaskRecycler` are wired; success via `id` and via `cwd`; missing/oversized `handoff_note`; recycler error; not-configured; unknown task.

## 3. Skill

- [x] 3.1 `internal/skills/builtin/task-recycle/SKILL.md`: guides the agent to write a real handoff (done/in-progress/decisions/next-step) and call `mcp__argus__task_recycle` with `cwd` + `handoff_note`.

## 4. Docs

- [x] 4.1 `make pre-pr`-equivalent gate (build, vet, fmt-check, lint-pr, race+coverage) green.
- [x] 4.2 Archive this change into `openspec/specs/` before merge.
