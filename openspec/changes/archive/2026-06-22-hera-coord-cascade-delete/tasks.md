## 1. Cascade on coordinator/header delete

- [x] 1.1 Generalize `heraCascadeDeleteSubtree(childID)` → `heraCascadeDeleteFrom(rootID)`; it accepts any orch id (`BridgeSubtree` already walks a top-level root).
- [x] 1.2 Route `heraOpenDelete`'s `sel.Orch != nil` (coordinator/header) branch to `heraCascadeDeleteFrom(sel.Orch.ID)`.
- [x] 1.3 Count-bearing confirm, generalized title; copy worded "archives" (DB rows) + "reclaims" (worktrees/branches).

## 2. Archive-not-delete semantics (Option A — no schema change)

- [x] 2.1 Add `Ops.ArchiveOrchestrator` (`ArchiveHeraOrchestrator`); keep `DeleteOrchestrator` for the prune paths only.
- [x] 2.2 Per-role terminal op = `RetireRole(r, false)` (ends binding + archives role; no hard delete).
- [x] 2.3 New `heraReclaimAndArchiveTask`: stop session + `RemoveWorktreeAndBranch` + `db.SetArchived` (NOT `db.Delete`).
- [x] 2.4 `heraDeleteRole` + `heraDoCascadeDelete` archive instead of delete; multi-bound tasks left fully alone.
- [x] 2.5 Inbox archived implicitly via role archive (no `archived_at` on `hera_messages`, no message-archive method).

## 3. Tests

- [x] 3.1 Single-orch header delete: count-bearing confirm; orchestrator + roles + tasks ARCHIVED (not deleted), bindings ended, worktree reclaimed.
- [x] 3.2 Nested ≥2-level: top-level coord delete archives coord + agents + sub-coord; internal-bridge worktree counted; outside-bound task preserved (not archived); outside orch/role/binding untouched.
- [x] 3.3 Update existing smoke tests (`TestSmoke_HeraDeleteOrchestrator*`, `TestSmoke_HeraDeleteRoleMultiBindingIsolation`, `TestSmoke_HeraCascadeDelete*`) to assert archive-not-delete.

## 4. Docs

- [x] 4.1 Gotcha in `context/knowledge/gotchas/hera-view.md` reworded to archive-not-delete (zero hard deletes; inbox retained; multi-binding preserved).
- [x] 4.2 README Reference `Ctrl+D` row reworded (archives rows, reclaims worktree/branch/session; vs `a`).
