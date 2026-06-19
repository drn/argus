# Tasks: hera-eol-redesign

Replace the five-key EOL surface with the two-state hide/nuke model and three keys (`a` hide, `Ctrl+D` nuke, `C` clear-archive). Drop `R` and rail-wide `Ctrl+R`. One coherent PR. Spec-first, TDD.

## 1. Tests (red first)

- [x] 1.1 `internal/db/hera_test.go`: `NukeHeraRole`/`NukeHeraOrchestrator` stamp `nuked_at` + `archived_at`, clear `pinned_at`, are idempotent, free the active-name index; `ListHeraRoles`/`ListHeraOrchestrators` exclude nuked rows while `HeraRole(id)`/`HeraOrchestrator(id)` still return them.
- [x] 1.2 `internal/tui/hera/model_test.go`: BuildModel filters nuked orchestrators AND nuked roles out of every section (DB-level + defensive model-level skip).
- [x] 1.3 `internal/tui/hera/ops_test.go`: `Ops.NukeRole`/`NukeOrchestrator` stamp nuked (no hard delete, row retained, multi-binding isolation); `Ops.ArchiveToggle` (the hide/unhide toggle) still tested.
- [x] 1.4 `internal/tui/heraactions_test.go`: `heraHide` is a no-op-with-feedback on a top-level coordinator; nukes (Ctrl+D role/coordinator + C) reclaim worktree, archive task, mark nuked, never hard-delete, preserve multi-bound tasks, and confirm with a count; `C` "nothing to clear" on an empty archive; remote-mode (`heraOps==nil`) keys inert.
- [x] 1.5 `internal/tui/hera/page_test.go` / keyset: `a`/`C`/`Ctrl+D` route via the selection; `R` and rail-wide `Ctrl+R` are no longer bound (no callback fires); all suppressed while filtering.
- [x] 1.6 `internal/tui/modal/help_test.go`: the "Hera View (rail)" section lists `a` (hide), `C` (clear archive), `Ctrl+D` (nuke) and does NOT list `R` or a rail-wide `Ctrl+R`.

## 2. Schema + DAO

- [x] 2.1 `internal/db/schema.go`: add `nuked_at TEXT` to the `hera_orchestrators` + `hera_roles` CREATE DDL and idempotent `ALTER TABLE … ADD COLUMN nuked_at` for existing DBs; index on `nuked_at`.
- [x] 2.2 `internal/db/hera.go`: `NukeHeraRole`/`NukeHeraOrchestrator`; thread `nuked_at` through scans + the rail-feeding list queries (exclude nuked); keep id lookups returning nuked rows.

## 3. Model projection

- [x] 3.1 `internal/tui/hera/model.go`: BuildModel skips rows with `nuked_at` set (never appended to any section); keep archived (hidden) rows. No projection field needed (nuked rows never reach the model).

## 4. Ops

- [x] 4.1 `internal/tui/hera/ops.go`: `MutateStore` gains `NukeHeraRole`/`NukeHeraOrchestrator` and DROPS the hard-delete verbs (`DeleteHeraRole`/`DeleteHeraOrchestrator`) — the EOL surface can't hard-delete. Add `Ops.NukeRole`/`NukeOrchestrator`; the hide toggle reuses the existing `Ops.ArchiveToggle`. Removed the now-dead `Ops.RetireRole`/`DeleteRole`/`ArchiveOrchestrator`/`DeleteOrchestrator`.

## 5. Page key routing + callbacks

- [x] 5.1 `internal/tui/hera/page.go`: drop `OnRetire`/`OnPruneDone` (+ their `R`/`Ctrl+R` cases); keep `OnArchiveToggle` (now hide), `OnDelete` (now nuke), `OnPruneDescendants` re-pointed to clear-archive (`C`).

## 6. App wiring + handlers

- [x] 6.1 `internal/tui/heraactions.go`: rework `heraArchiveToggle`→`heraHide` (worker/sub-coord only, no confirm; top-level coord feedback no-op; Q2 seam for task-archive); rename delete→nuke (`heraOpenDelete`/`heraNukeRole`/`heraCascadeNukeFrom`/`heraDoCascadeNuke` using `nuked_at`); `heraPruneDescendants`→`heraClearArchive` (nuke the coord's hidden descendants); DELETE `heraRetireWorker`/`heraDoRetire` and `heraPruneDone`/`heraDoPruneDone` (+ `heraOrchHasLiveBinding` if unused).
- [x] 6.2 `internal/tui/app.go`: drop the `OnRetire`/`OnPruneDone` wiring; keep the rest.

## 7. Docs + gates

- [x] 7.1 `internal/tui/modal/help.go` + `help_test.go`: drop `R` + rail-wide `Ctrl+R`; relabel `a` (hide), `C` (clear archive), `Ctrl+D` (nuke).
- [x] 7.2 README Reference keybinding table: same.
- [x] 7.3 `context/knowledge/gotchas/hera-view.md`: document the two-state hide/nuke model, the per-coord nested archive, the `nuked_at`-invisible-to-rail rule, and the zero-hard-deletes invariant.
- [x] 7.4 Reconcile the in-flight `hera-rail-eol-keys` + `hera-coord-cascade-delete` changes (drop R/Ctrl+R requirements; recast cascade to nuke) OR note supersession.
- [x] 7.5 `make pre-pr` clean; `openspec validate hera-eol-redesign --strict`.

## Resolved by the user (via coordinator)

- [x] Q2 = HIDE is RAIL-ONLY. `heraHide` archives the hera role row only; it does NOT `db.SetArchived` the argus task (matched the default — no code change, doc/comment updated).
- [x] Q3 = hiding a bridging sub-coord COLLAPSES its whole subtree into the parent's Archive expando (structure retained), not dimmed-in-place. `rail.go appendOrchWorkers` routes ALL archived workers into the expando; `structuralReach` prevents the collapsed-state orphaning. Tested at ≥2 levels in both fold states (`TestRail_HiddenSubCoordCollapsesSubtreeIntoExpando`, `TestRail_ArchivedBridgingWorkerHoistsSubtreeToExpando`).
