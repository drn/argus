## Why

BUG-017: pressing `Ctrl+D` on a coordinator / orchestrator HEADER in the native Hera rail removed only that orchestrator's hera rows and PRESERVED every underlying argus task — worktrees stayed on disk, sessions kept running, and nested sub-coordinators were left behind entirely. So "delete the coordinator" left orphaned worktrees and stranded sub-teams.

The intended Hera delete model (Aaron's rule): **"Deleting an agent or coord in hera just archives the inbox. The database doesn't get any deletes."** Combined with the requirement that delete must reclaim the real agent + worktree, delete means: **archive every DB record, reclaim only the worktree/branch/session — zero hard deletes.**

## What Changes

- `Ctrl+D` on a coordinator / orchestrator HEADER now cascades the full subtree (rooted at the selected orchestrator: itself + every nested sub-coordinator + their agents), the same teardown a bridging worker row runs.
- **Delete ARCHIVES, never hard-deletes, every DB record:**
  - hera role rows → `RetireRole` (ends the live binding `user_deleted` + `ArchiveHeraRole`) — the archive-not-delete primitive the `R` key uses.
  - orchestrator rows → `Ops.ArchiveOrchestrator` (`ArchiveHeraOrchestrator`), NOT `DeleteHeraOrchestrator`.
  - the argus task row → `db.SetArchived`, NOT `db.Delete`.
  - the role's inbox/messages → archived implicitly (Option A): because the role is archived rather than deleted, its `hera_messages` rows are retained attached to an archived role. No `archived_at` column or message-archive method is added.
- **Reclaim only the real resources:** stop the session + remove the worktree + LOCAL and REMOTE branch (`agent.RemoveWorktreeAndBranch`), for a task whose live bindings are all within the subtree.
- A task bound live OUTSIDE the subtree is PRESERVED — left fully alone (not archived, worktree kept) — multi-binding safety.
- The confirm modal is count-bearing, worded "archives" for DB rows and "reclaims" for worktrees/branches. `Ctrl+D` binding unchanged.
- Difference from the `a` key: `a` archives but KEEPS the worktree/session; `Ctrl+D` archives AND reclaims them.

## Capabilities

### Modified Capabilities

- `hera-view`: orchestrator/coord-header delete changes from "cascade-delete hera rows, preserve all argus tasks" to "cascade-ARCHIVE the full subtree (roles + orchestrators + tasks + inbox), reclaim worktrees, show a count, zero hard deletes" — reversing the old preserve-everything behavior while keeping multi-binding preservation.

## Impact

- **Modified code:** `internal/tui/heraactions.go` (`heraOpenDelete` header branch → `heraCascadeDeleteFrom`; `heraDeleteRole` + `heraDoCascadeDelete` archive instead of delete; new `heraReclaimAndArchiveTask`), `internal/tui/hera/ops.go` (new `ArchiveOrchestrator`; `DeleteOrchestrator` retained for the prune paths only).
- **No model/rail change:** `Model.BridgeSubtree(rootID)` already walks a TOP-LEVEL root (proven by existing `TestModel_BridgeSubtree` / `TestModel_BridgeSubtreeIncludesCoordSpawnedChild`).
- **No schema change** (Option A: no `archived_at` on `hera_messages`). **No new keybinding.**
- Out of scope / accepted: `db.SetArchived` on a task drops that task's queued LEGACY `task_messages` (a different table from `hera_messages`) — established retire/archive behavior; the "no deletes" rule is scoped to hera role/orch/inbox rows.
- **Specs are LOCAL DOCS only** (`openspec/project.md`). Do NOT wire `openspec validate` into Go CI or `make`; the quality gate stays `make pre-pr`. Run `openspec validate --strict` locally only.
