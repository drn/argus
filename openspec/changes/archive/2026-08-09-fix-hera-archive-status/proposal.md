## Why

`heraReclaimAndArchiveTask` — the single code path behind every Hera nuke/reclaim action (Ctrl+D on a role, Ctrl+D cascading a coordinator's subtree, `C` clearing a hidden archive) — archives a task's row but never advances its `status`. Because the normal worker finish path already rolls a finished worker's task to `in_review` before a human ever nukes it, an archived task is almost always left stranded at `status=in_review` forever, permanently invisible to `PruneCompleted` (Ctrl+R), which only ever looks at `status=complete` rows. A live audit found 737 tasks across every project stuck in exactly this state with zero path to cleanup.

## What Changes

- `heraReclaimAndArchiveTask` additionally advances a task's status from `in_review` to `complete` at the moment it archives it — but ONLY when the task is currently `in_review`. A task that is still `pending` or `in_progress` at archive time (e.g. an operator force-nuking a live, still-working role) is archived exactly as today, with its status left untouched.
- This applies uniformly to coordinator, worker, and freelance roles — the gate is on the task's own `Status` column (a value common to every role kind), not on hera role status or role kind.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the "Conservative delete semantics for multi-binding safety (area 7)" requirement — which documents `heraReclaimAndArchiveTask`'s full archive/reclaim contract — gains the status-advancement behavior described above.

## Impact

- `internal/tui/heraactions.go`: `heraReclaimAndArchiveTask` gains one conditional `db.SetStatus` call alongside its existing `db.SetArchived` call. Its three callers (`heraNukeRole`, `heraDoCascadeNuke`, `heraNukeArchivedRole`) are unchanged — they all funnel through the one fixed function.
- No schema change, no new DB method, no new MCP tool surface. Reuses the existing generic `store.Store.SetStatus` primitive already used elsewhere (e.g. `RollHeraWorkerToReview`).
- Downstream: previously-stranded tasks that reach this code path going forward become visible to `PruneCompleted` and the task list's completed-section filtering. The 737 already-stranded historical tasks are NOT touched by this change (explicit Non-Goal — a separate follow-on historical sweep depends on this landing first).
