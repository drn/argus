## Why

Hera's `Ctrl+D` nuke promises to fully clean up a coordinator/worker tree,
but three of its real-world side effects are best-effort with no durability
and quietly don't always happen:

1. Worktree + branch reclaim runs in a fire-and-forget goroutine — 13 real
   git worktrees are still on disk and registered in `git worktree list`
   weeks after their owning role/orchestrator were nuked, and the existing
   orphan-worktree sweep can never see them (it treats any task row's
   `worktree` column as "still owned," archived or not).
2. Nuke archives the argus task row but never deletes it, and deletion is a
   fully separate, 100%-manual action (`Ctrl+R` / the Cleanup popup) in the
   Tasks tab — a tab this operator works in rarely. Result: ~300+ archived
   task rows silently piling up, read as "orphaned agents," while Hera
   itself looks clean because nuked rows are invisible there by design.
3. A stacked chain of hera workers (each branched off the previous via
   `base_branch`) whose tip gets its PR squash-merged leaves every earlier
   branch in the chain undetectable as "safe to delete" by the existing
   merge-safety tiers — a plain ancestry check against `main` fails after a
   squash merge, and only the tip ever gets its own PR. The operator
   currently runs a manual `/cleanup` skill before every `Ctrl+D` to sweep
   these by hand.

## What Changes

- Nuke's worktree + branch reclaim becomes durable: a new reconciliation
  sweep (daemon startup + periodic) re-derives and finishes any reclaim a
  lost/interrupted goroutine didn't complete, self-healing existing leaks on
  first run.
- **BREAKING** (deliberate, per this repo's no-legacy-migration policy):
  once a nuked task's worktree and branch(es) are confirmed reclaimed and it
  holds no live session, its task row is deleted (`db.Delete` via the
  existing `PruneTasks`) instead of being archived forever. Hera role,
  binding, orchestrator, and inbox rows are still never hard-deleted — only
  the argus task row's terminal fate changes.
- New merge-safety Tier D: infers a stacked branch's safety by walking the
  `base_branch` chain back from a Tier-A/B-confirmed-merged tip and checking
  real git ancestry against that tip's own branch (not `main`) — correct
  under squash merges, sibling to the existing Tier C
  (`coordinator-inferred`).
- Cascade-nuke's existing pre-confirm merge-safety pass also runs the new
  Tier D stack-walk per reclaimed task, and the confirm modal lists any
  extra origin branches it found confirmed-safe so the operator can
  approve/exclude them before anything is deleted from origin — folding the
  operator's manual `/cleanup` step into `Ctrl+D` itself.
- The reconciliation sweep also finishes any approved-but-interrupted extra
  branch deletion, honoring any branch the operator explicitly excluded.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: "Conservative delete semantics for multi-binding safety" —
  nuke's task-row fate changes from archive-forever to archive-then-prune
  once reclaim settles; the cascade confirm modal also surfaces
  Tier-D-discovered stacked branches for approval.
- `worktree-management`: adds durable/retryable worktree+branch reclaim and
  a reconciliation sweep; "Pruning completed tasks" gains a second trigger
  (hera nuke, once settled) alongside the existing manual `Ctrl+R`/Cleanup
  paths.
- `merge-safety`: adds Tier D (stack-inferred safety via `base_branch`
  ancestry), sibling to the existing Tier C (`coordinator-inferred`).

## Impact

- `internal/tui/heraactions.go` (`heraReclaimAndArchiveTask`,
  `heraCascadeNukeFrom`, `heraDoCascadeNuke`) — inline prune-on-reclaim,
  extra-branch discovery/deletion wired into the existing confirm flow.
- `internal/tui/app.go` (`handleSessionExitUI`, `pendingHeraReclaim`) — no
  functional change, but the inline prune must respect this existing
  in-progress-at-nuke-time race.
- `internal/mergesafety` (`classify.go`) — new Tier D constant + classifier.
- `internal/api/cleanup_candidates.go` — Tier D reuses/extends the existing
  Tier C grouping pattern.
- `internal/hera/` — new `reclaim_sweep.go`, sibling to the existing
  `adopt.go` (`ReconcileBindings`).
- `internal/daemon/bounce.go`, `internal/daemon/daemon.go` — wire the new
  sweep into `ReconcileOnStartup` and a new periodic ticker.
- `internal/db/tasks.go` / `internal/db/hera.go` — a small `task_meta`
  entry for operator-excluded branches; reuses existing `PruneTasks`,
  `WorktreePaths`, and hera binding queries — no schema migration expected.
- No changes to the macOS app or web PWA — this is TUI/daemon-only behavior
  (Hera mutations are already TUI-only per this repo's frontend-parity
  rules).
