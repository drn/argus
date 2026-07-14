## Why

**A recycled coordinator's pane showed stray garbled text at the top, above
the freshly-recycled session's first rendered line.** Aaron used
`recycle_coord` live on an unrelated project; the recycle worked functionally
(same task/worktree/branch/binding, fresh session) but the Hera pane leaked a
partial-repaint artifact from the outgoing session.

Root cause: `internal/tui/hera/panes.go`'s `bindPane` and `reconcileOne` call
`tp.SetSession(sess)` alone when a pane's session changes. `SetSession` only
resets the emulator/scroll/paint-cache fields; every OTHER "new content
incoming" transition in the codebase (`onTaskSelect`, `enterPendingAgentView`)
pairs it with `tp.ResetVT()`, which additionally clears the replay
emulator/cache, replay data, and scroll anchor. `reconcileOne` is the ONLY
site a recycle's same-task session swap flows through (the task ID never
changes, so `bindPane`'s task-changed guard never fires) — and it was the one
transition skipping `ResetVT`, so state left over from the outgoing session
could survive into the incoming one's render.

## What Changes

- **`bindPane` and `reconcileOne` call `ResetVT()` before `SetSession()`** on
  every genuine session swap, matching the `SetTaskID→ResetVT→SetSession`
  order the main agent view already uses.
- **Hera's human-forced recycle (`B` key) stages `pendingRerenderRestart`**
  the same way a rerender kick does, so `handleSessionExitUI`'s dedicated
  immediate-resume branch handles a recycle exit instead of falling through
  to the generic crash/stop path; that branch's reattachment is paired with
  `ResetVT()` too.
- **Defense-in-depth: the "No active session" / "Session not running" /
  "Waiting for output..." transitional messages now fill their full bounding
  rect before drawing text**, so a partially-repainted pane can never leak a
  prior frame's cells regardless of which code path triggers the transition.

## Capabilities

### Modified Capabilities

- `hera-view`: the dead-session-replacement reconcile (BUG-013's fix) now
  also resets the pane's VT/replay state on every genuine session swap, so no
  cell or replay state from the outgoing session survives into the fresh
  one's render.

## Impact

- **Modified code:** `internal/tui/hera/panes.go` (`bindPane`,
  `reconcileOne`), `internal/tui/heraactions.go` (`heraDoForceRecycle`),
  `internal/tui/app.go` (`handleSessionExitUI`), `internal/tui/terminal/terminalpane.go`
  (transitional-message full-rect fill).
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  rendering/reconcile-correctness bugfix.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make /
  Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
