## Why

A task that is actively `in_progress` at the moment of a Hera nuke/reclaim (`Ctrl+D`, cascade, or `C` clear-archive) never reaches `status=complete`. `heraReclaimAndArchiveTask` snapshots the task, backgrounds its session stop (`heraGoSafe`, the BUG-062 fix for bulk-cascade UI freeze), and archives it immediately — leaving `Status` untouched because it was `in_progress`, not `in_review`, at that instant. The backgrounded stop's eventual exit notification races `heraReclaimAndArchiveTask`'s own one-shot completion check and always loses: `handleSessionExitUI` runs later, sees a non-clean exit (the stop's own signal), and — correctly, for an ordinary stop — lands the task at `in_review`. But by then the task is already archived, its hera binding already ended, and its worktree/branch already reclaimed: nothing is left to ever revisit it, so it is permanently stranded at `in_review` instead of `complete`. Confirmed live on 2026-08-10 via the daemon log and `~/.argus/data.sql`.

## What Changes

- `heraReclaimAndArchiveTask` marks a task that is `in_progress` at reclaim time as "pending forced completion" BEFORE backgrounding its session stop, instead of relying on a one-shot synchronous status check that only ever catches an already-`in_review` task.
- `handleSessionExitUI` consults that marker when resolving a `StatusInProgress` task's terminal state: if set, the task lands at `complete` regardless of the exit's clean/non-clean verdict, instead of falling through to the ordinary crash/stop/fast-fail → `in_review` rule. The marker is consumed (cleared) the moment it's read, so it can never leak into a later, unrelated exit of the same task ID. It never overrides the existing PR #707 `RollHeraWorkerToReview` invariant (a task holding a live worker-kind hera binding never self-completes) — that check still runs first and wins if it fires.
- The ordinary (non-reclaim) stop/crash/fast-fail → `in_review` rule, and the daemon kick-restart (`pendingRestart`) skip, are both unchanged for every exit that isn't reclaim-marked.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the "Conservative delete semantics for multi-binding safety (area 7)" requirement gains a guarantee that a task `in_progress` at reclaim time still reaches `complete` once its reclaim-triggered session stop settles, closing the gap left by the prior `fix-hera-archive-status` change (which only advanced an already-`in_review` task synchronously).

## Impact

- `internal/tui/app.go`: new `pendingHeraReclaim` map (guarded by the existing `a.mu`) on `App`; `handleSessionExitUI` gains a branch consulting/consuming it.
- `internal/tui/heraactions.go`: `heraReclaimAndArchiveTask` marks the task before backgrounding its stop, and clears the mark on any path that guarantees no exit notification is coming (no live session to stop, or the stop call itself errors).
- No schema change, no new DB method, no new MCP tool surface, no change to task deletion/pruning logic.
