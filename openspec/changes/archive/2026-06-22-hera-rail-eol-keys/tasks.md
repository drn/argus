# Tasks: hera-rail-eol-keys

Add the rail key family: full new-task modal for `w`, new-root-coordinator `n`, retire `R`, prune-descendants `C`, rail-wide prune `Ctrl+R`. One coherent PR.

## 1. Tests (red first)

- [x] 1.1 `internal/db/hera_test.go`: `UniqueHeraOrchestratorName` returns the base when free, de-collides with a numeric suffix against ACTIVE orchestrators, defaults empty base.
- [x] 1.2 `internal/agent/hera_spawn_test.go`: `SpawnHeraCoordinator` creates a fresh orchestrator + coordinator role + binding to a new task; stamps `meta:hera.role=coordinator`; carries branch/backend/model; unwinds the orchestrator on a start failure (no orphan orchestrator).
- [x] 1.3 `internal/tui/hera/eol_test.go`: `RoleView.IsFinished` (archived / status done / ready_to_close → true; live in_progress → false); `Model.SubtreeArchivedWorkers` returns archived worker roles across the subtree; `Model.FinishedRoles` gathers finished roles rail-wide; `Model.FullyFinishedOrchestratorIDs` returns orchestrators whose every role is finished.
- [x] 1.4 `internal/tui/hera/ops_test.go`: `Ops.RetireRole` sets status `done`, rolls a worker task to review when sole-bound, ends the role's live binding, archives the role row; multi-bound path does not roll the task.
- [x] 1.5 `internal/tui/heraactions_test.go`: `heraSpawnWorker`/`heraNewCoordinator` open the new-task modal with the defaulted project and return to the hera tab; `heraRetireWorker` on a coord/header gives feedback (no-op); `heraPruneDescendants`/`heraPruneDone` open a confirm with a summary and "nothing to prune" feedback when empty; remote-mode (`heraOps==nil`) keys are inert.
- [x] 1.6 `internal/tui/hera/page_test.go` / keyset: `n` fires `OnNewCoordinator` even on an empty selection; `R`/`C` route via the selection; `Ctrl+R` fires `OnPruneDone`; all are suppressed while filtering.
- [x] 1.7 `internal/tui/newtaskform_test.go`: `SetTitle` renders the custom title.
- [x] 1.8 `internal/tui/modal/help_test.go`: the "Hera View (rail)" section lists `n`, `R`, `C`, `Ctrl+R` and the updated `w` action.

## 2. DAO + agent primitives

- [x] 2.1 `internal/db/hera.go`: `UniqueHeraOrchestratorName(base string) (string, error)`.
- [x] 2.2 `internal/agent/hera_spawn.go`: `SpawnHeraCoordinator` (+ input/result types + `HeraCoordinatorOrientation`), with orchestrator-unwind on failure; extend `HeraWorkerSpawnInput` usage to carry branch/backend/model (already supported by the struct).

## 3. Pure selection helpers (new file)

- [x] 3.1 `internal/tui/hera/eol.go`: `RoleView.IsFinished`, `Model.SubtreeArchivedWorkers`, `Model.FinishedRoles`, `Model.FullyFinishedOrchestratorIDs`.

## 4. Ops

- [x] 4.1 `internal/tui/hera/ops.go`: extend `MutateStore` with `HeraLiveBindingByRole`/`EndHeraBinding`; add `Ops.RetireRole(r *RoleView, rollTask bool) error`.

## 5. Page key routing + callbacks

- [x] 5.1 `internal/tui/hera/page.go`: add `OnNewCoordinator`/`OnRetire`/`OnPruneDescendants`/`OnPruneDone`; handle `n` (direct, selection-independent), `R`/`C` (via `fire`), `Ctrl+R` (direct) in `handleRailMutation`, all suppressed while filtering.

## 6. App wiring + handlers

- [x] 6.1 `internal/tui/newtaskform.go`: settable title; `internal/tui/app.go`: `buildNewTaskForm` extraction, `newTaskOnDone` + `newTaskReturnPage` so the same modal serves the hera tab; route the override in `handleNewTaskKey`; clear in `closeNewTaskForm`.
- [x] 6.2 `internal/tui/heraactions.go`: `heraSpawnWorker` (full modal), `heraNewCoordinator`/`heraDoNewCoordinator`, `heraRetireWorker`/`heraDoRetire`, `heraPruneDescendants`/`heraDoPruneDescendants`, `heraPruneDone`/`heraDoPruneDone`, `heraReclaimRole` + the multi-binding guard.
- [x] 6.3 `internal/tui/app.go`: wire the four new `HeraPage` callbacks in the local-mode block.

## 7. Docs + gates

- [x] 7.1 `internal/tui/modal/help.go` + `help_test.go`: add `n`/`R`/`C`/`Ctrl+R`, update `w`.
- [x] 7.2 README Reference keybinding table: add the new rail keys.
- [x] 7.3 `context/knowledge/gotchas/hera-view.md`: document the non-obvious gotchas (rail-scoped `Ctrl+R` vs agent-view; `n` bypasses `fire`; retire keeps worktree / prune reclaims; multi-binding guard).
- [x] 7.4 `make pre-pr` clean; `openspec validate hera-rail-eol-keys --strict`.
