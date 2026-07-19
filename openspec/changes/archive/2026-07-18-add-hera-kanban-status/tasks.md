**Design doc:** `openspec/changes/add-hera-kanban-status/design.md`

Note on structure: stages are a strict linear chain (schema → model/ops → rail rendering → hotkey wiring → REST → docs → gate). Each depends on the immediately preceding stage.

## 1. DB layer

- [x] 1.1 Write failing tests: `db.HeraKanbanStatus` constants; `HeraOrchestrator.KanbanStatus` defaults to `active` for a freshly created orchestrator; `SetHeraOrchestratorKanbanStatus` persists and round-trips through `HeraOrchestrator(id)` / `ListHeraOrchestrators`; unknown id returns `ErrHeraNotFound`
- [x] 1.2 Add `kanban_status TEXT NOT NULL DEFAULT 'active'` to the `hera_orchestrators` `CREATE TABLE IF NOT EXISTS` block and an idempotent `ALTER TABLE hera_orchestrators ADD COLUMN kanban_status TEXT NOT NULL DEFAULT 'active'` migration (`internal/db/schema.go`), matching the `nuked_at`/`base_branch` precedent — no CHECK constraint (see design.md D2)
- [x] 1.3 Add `db.HeraKanbanStatus` type + `HeraKanbanActive`/`HeraKanbanBacklog`/`HeraKanbanBlocked`/`HeraKanbanDone` constants, `HeraOrchestrator.KanbanStatus` field, update `scanHeraOrchestrator` and every `SELECT ... FROM hera_orchestrators` query to include the column, and `SetHeraOrchestratorKanbanStatus(id int64, status HeraKanbanStatus) error` (`internal/db/hera.go`)
- [x] 1.4 Make Stage 1.1 tests green; `make test-pkg PKG=./internal/db/`

## 2. Model + Ops layer

**Depends on:** Stage 1

- [x] 2.1 Write failing tests: `OrchView.KanbanStatus` populated by `BuildModel`; `Selection.TopLevelOrch` true only for a root orchestrator header selection (false for a role, a nested/bridged orchestrator, an empty selection); `Ops.KanbanStep` advances/reverts with wraparound at both ends; no-op (returns `errNoTarget` or equivalent) when the selection isn't a top-level coordinator header
- [x] 2.2 Add `OrchView.KanbanStatus db.HeraKanbanStatus` (`internal/tui/hera/model.go`), populated in `BuildModel`
- [x] 2.3 Add `Selection.TopLevelOrch bool`, stamped by `Rail.Selection()` from the same `canonical` map `buildRows` computes (see Stage 3) — resolves to true only when the cursor is on an `rrOrch` row whose orchestrator has no canonical parent
- [x] 2.4 Add `MutateStore.SetHeraOrchestratorKanbanStatus` to the `Ops` interface; implement `Ops.KanbanStep(sel Selection, dir int) error` with its own wrapping `kanbanOrder` ladder (distinct from `heraStatusLadder`/`ladderIndex`, per design.md D5) — no-ops via `errNoTarget` when `sel.Role != nil || sel.Orch == nil || !sel.TopLevelOrch`
- [x] 2.5 Make Stage 2.1 tests green; `make test-pkg PKG=./internal/tui/hera/`

## 3. Rail grouping and dividers

**Depends on:** Stage 2

- [x] 3.1 Write failing tests (`rail_test.go`): active-status top-level orchestrators render headerless exactly as today; a non-empty Backlog/Blocked/Done group renders its labeled header + leading divider; an empty group renders neither; rail order is `Pinned → active → Backlog → Blocked → Done → Freelance → Archive`; a nested/bridged orchestrator's kanban status never affects its placement; the existing Pinned→active divider scenario is unchanged
- [x] 3.2 Partition the existing "Active" render pass in `Rail.buildRows()` into the four ordered kanban sub-groups (root pass + true-orphan safety sweep per group, filtered by `!consumed[id] && KanbanStatus == status`), inserting the labeled header + divider per design.md D4 (`internal/tui/hera/rail.go`)
- [x] 3.3 Make Stage 3.1 tests green; run the FULL existing `rail_test.go`/`model_test.go`/`pin_nonroot_test.go` suites to confirm zero regression in Pinned/Freelance/Archive rendering

## 4. m/M hotkey wiring

**Depends on:** Stage 3

- [x] 4.1 Write failing tests: `keymap/parse_test.go`/`keymap_test.go` resolve `m`/`M` to `ActHeraKanbanAdv`/`ActHeraKanbanRev` in `CtxHeraRail`; `hera/keyset_test.go` and `hera/details_railmut_test.go` table entries for `m`/`M` (mirroring the existing `'J'` entries) covering: fires on a top-level coordinator, no-op on a role/nested-coordinator/empty selection, suppressed in filter-input mode
- [x] 4.2 Add `ActHeraKanbanAdv`/`ActHeraKanbanRev` to `internal/tui/keymap/actions.go`: `defaultSpecs[CtxHeraRail]` (`"m"`/`"M"`), `actionLabels`, `contextOrder` (placed after `ActHeraPin`, before `ActHeraStatAdv`)
- [x] 4.3 Wire `handleRailMutation` in `internal/tui/hera/page.go` to resolve `m`/`M` and call `Ops.KanbanStep`; update the `isRailMutationKey` doc comment's rune-set list to include `m`/`M`
- [x] 4.4 Make Stage 4.1 tests green; `make test-pkg PKG=./internal/tui/keymap/` and `make test-pkg PKG=./internal/tui/hera/`

## 5. REST read-plumbing

**Depends on:** Stage 4

- [x] 5.1 Write failing tests (`internal/api/hera_test.go`): `heraOrchJSON.kanban_status` defaults to `"active"` and reflects an explicit value
- [x] 5.2 Add `KanbanStatus string \`json:"kanban_status"\`` to `heraOrchJSON` and populate it from `o.KanbanStatus` in `handleHera` (`internal/api/hera.go`)
- [x] 5.3 Make Stage 5.1 tests green; `make test-pkg PKG=./internal/api/`

## 6. Docs

**Depends on:** Stage 5

- [x] 6.1 README.md Reference keybinding table: add the `m`/`M` row
- [x] 6.2 `context/knowledge/gotchas/hera-view.md`: bullet documenting the kanban axis (DB column persistence, rail grouping/order/divider logic, `m`/`M`, explicit non-collision with `s`/`S`)
- [x] 6.3 `context/knowledge/gotchas/keybindings.md`: bullet for the new `m`/`M` `CtxHeraRail` binding
- [x] 6.4 `context/knowledge/index.md`: bump the `hera-view.md` and `keybindings.md` bullet counts

## 7. Gate and ship

**Depends on:** Stage 6

- [x] 7.1 `make pre-pr` full gate (build, vet, fmt-check, lint-pr, vuln, test-cover-gate) — fix everything surfaced, don't stop at first failure. Green except `vuln`, which fails only on pre-existing stdlib CVEs (GO-2026-5856/5039/5037) unrelated to this change — CI runs `vuln` as `continue-on-error` (advisory), matching prior repo precedent.
- [x] 7.2 `openspec archive add-hera-kanban-status` (or the manual merge-and-move fallback) in the same PR, before merge
- [x] 7.3 Open the PR via `mcp__argus__iris_gh_pr_create`
