## Why

Coordinators already have a full context-reset mechanism (`recycle_coord`): kill and restart a session in place, same task/worktree/branch/binding, reseeded from a handoff note. Workers and freelance roles have no equivalent — a worker on a long implementation task that grows heavy on context can only be abandoned (delete + respawn), losing its worktree/branch continuity. This change gives a human a manual, rail-invoked way to bounce an idle worker or freelance role's session — reusing the existing recycle machinery rather than building something new — for the narrow, deliberate moment when the work is done, the role is idle, and it's about to start something new or is waiting on something external (CI, a review, etc.).

## What Changes

- `hera_status`'s `handoff_note`/`request_recycle` parameters, currently rejected for worker/freelance callers, are accepted from any hera-bound role kind.
- `RecycleWatcher.tickTask`, currently filtering to `role.Kind == coordinator` only, picks up a pending self-service recycle for a worker or freelance role's live binding too — driving it through the existing `RecycleCoord`/`RecycleSelfService` path once the session is idle.
- `BuildRecycleSeedPrompt`'s opening line and doc comments stop assuming the recycled role is a coordinator.
- The rail `B` key, currently a no-op on anything but a coordinator selection, now also fires on a worker or freelance selection: after a confirm modal, it sends a system-input instruction to the role's live session asking it to call `hera_status(handoff_note=<summary>, request_recycle=true)` — the role's own tool call then drives the rest through the now-widened self-service pipeline.
- No automation added anywhere in this path: no Stop-hook nudge, no budget tracking, no fallback/timeout if the role never responds. Coordinator `B` behavior (immediate `RecycleHumanForced`) is unchanged.

## Capabilities

### New Capabilities

(none — this widens three existing capabilities)

### Modified Capabilities

- `hera-coordination`: `hera_status` accepts `handoff_note`/`request_recycle` from worker/freelance callers, not just coordinators.
- `coordinator-context-management`: `RecycleWatcher`'s self-service path and `BuildRecycleSeedPrompt`'s seed text apply to worker/freelance roles, not just coordinators.
- `hera-view`: the rail `B` key fires a distinct, non-immediate ("bounce") action on a worker/freelance selection, alongside its unchanged coordinator behavior.

## Impact

- `internal/mcp/hera.go` (`toolHeraStatus` validation + tool description)
- `internal/hera/recycle_watcher.go` (`RecycleWatcher.tickTask` role-kind filter)
- `internal/hera/recycle.go` (`BuildRecycleSeedPrompt` wording)
- `internal/tui/hera/page.go` (`B` key dispatch) and `internal/tui/heraactions.go` (new bounce action, alongside the existing `heraOpenForceRecycle`/`heraDoForceRecycle`)
- No schema/data migration, no new dependencies, no REST/API surface change.
