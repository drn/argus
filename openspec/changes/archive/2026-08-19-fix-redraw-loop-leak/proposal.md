## Why

**The agent view's 200ms redraw loop (`App.startAgentRedrawLoop`) can leak an
unbounded number of goroutines per task, degrading the WHOLE TUI's input
responsiveness over a long session — not just the task the loop is for.**

Both call sites (`onTaskSelect`, `startSession`) spawn a fresh redraw-loop
goroutine every time the user enters a running task's agent view, with no
guard against one already running for that task. The loop's only exit check
is `stillViewing` (`a.mode == modeAgent && a.agentState.TaskID == taskID`),
evaluated once per 200ms wake-up. If the user leaves that task's agent view
and returns to it again before the stale loop's next wake-up — trivially easy
via fast keyboard navigation (task switcher, Hera rail, bouncing between
tasks to check progress) — the stale loop wakes up, finds `stillViewing` true
again, and keeps running. The fresh entry spawns a second loop alongside it.
Neither loop is aware of the other.

Each surviving loop fires an RPC (`SyncPTYSize`) and, on new output, a
*synchronous* `QueueUpdateDraw` every 200ms. `QueueUpdateDraw` blocks until
tview's single event loop — the same loop that dispatches keystrokes — runs
the queued closure. Enough accumulated zombie loops (one more per fast
revisit, compounding over a long session with a lot of task-switching
iteration) saturate that event loop, reproducing as general input lag across
the whole TUI, not scoped to any one pane. Restarting the TUI kills every
goroutine and resets the count to zero — matching the reported "restart makes
it fast again" behavior.

## What Changes

- **`startAgentRedrawLoop` gains a per-task generation counter
  (`App.redrawLoopGen`).** Every call bumps the task's generation; each loop
  captures its own generation at spawn time and exits the instant a newer
  loop supersedes it, regardless of what `stillViewing` reads that tick — so
  at most one redraw loop is ever live per task, no matter how fast the user
  leaves and returns to it.
- **`redrawLoopGen` entries are cleared on task delete**, mirroring the
  existing `pendingRerenderRestart`/`committedCols` cleanup, so the map
  doesn't accumulate one entry per task forever in a long-lived TUI process.
- No behavior change to the loop's cadence, RPC calls, or draw-skip-when-idle
  logic — only its lifecycle/uniqueness guarantee changes.

## Capabilities

### Modified Capabilities

- `agent-view`: at most one redraw loop is now live per task at any time —
  previously a fast leave-and-return to the same task's agent view could
  leave a stale loop running indefinitely alongside a fresh one.

## Impact

- **Modified code:** `internal/tui/app.go` — `redrawLoopGen` field + init,
  `startAgentRedrawLoop`, new `redrawLoopShouldExit` predicate, cleanup in
  `deleteTask`.
- **No new key, no new dependency, no schema change, no daemon RPC surface
  change.** Pure in-process goroutine lifecycle fix.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make /
  Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
