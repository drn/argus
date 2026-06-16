# Tasks — freelancers-only filter on the Tasks tab

TDD throughout (Red → Green → Refactor). Use `internal/testutil` assertions. Gate: `make pre-pr`.

## 1. DB: authoritative managed-task query

- [ ] 1.1 Write a failing test in `internal/db/hera_test.go` (or a new `hera_managed_test.go`): seed an orchestrator with coordinator, worker, and freelance roles; create live bindings for each plus one ended worker binding; assert `ManagedTaskIDs()` returns exactly the task IDs with a live coordinator- or worker-kind binding (excludes the ended worker's task and the freelance-only task).
- [ ] 1.2 Implement `func (d *DB) ManagedTaskIDs() (map[string]bool, error)` in `internal/db/hera.go`: `SELECT DISTINCT b.argus_task_id FROM hera_bindings b JOIN hera_roles r ON r.id = b.role_id WHERE b.ended_at IS NULL AND r.kind IN (?, ?)` with `HeraKindCoordinator`/`HeraKindWorker`; mutex-guarded like the sibling methods; return a `map[string]bool`.
- [ ] 1.3 Cover the empty-DB case (no bindings → empty non-nil map) and the rows.Err() path.

## 2. Tasklist: state, setter, toggle, key

- [ ] 2.1 Failing test in `internal/tui/taskview/tasklist_test.go`: with `freelancersOnly=true` and a `managed` set, `VisibleTaskIDs()` excludes managed task IDs and retains freelancers; toggling off restores; composes correctly with an active `/` filter and with `hideHeraWorkers`.
- [ ] 2.2 Add fields to `TaskListView`: `freelancersOnly bool` (default false) and `managed map[string]bool` (init in `NewTaskListView`).
- [ ] 2.3 Add `SetManagedTasks(ids map[string]bool)` (nil → empty map, mirroring `SetHeraWorkers`).
- [ ] 2.4 Add `ToggleFreelancersOnly()` (flip, `buildRows()`, `clampCursor()`, fire `OnFreelancersOnlyToggle` log-only callback) and exported `FreelancersOnly() bool` test seam.
- [ ] 2.5 In `buildRows()`, after the existing `hideHeraWorkers` skip, add `if tl.freelancersOnly && tl.managed[t.ID] { continue }`.
- [ ] 2.6 Bind `f` in the tasklist `InputHandler` rune switch to `ToggleFreelancersOnly()`. Confirm no collision (it is currently unbound; `ctrl+f` fork is a distinct event).
- [ ] 2.7 Add the `OnFreelancersOnlyToggle func(active bool)` callback field (log-only, mirrors `OnHeraWorkersToggle`).

## 3. Title indicator

- [ ] 3.1 Failing render test: when `freelancersOnly` is true, the panel title area renders a `freelancers only` indicator distinct from the `/filter` indicator.
- [ ] 3.2 Extend the title-drawing block in `tasklist.go` to render the indicator when `freelancersOnly` is true, styled consistently with the existing filter decoration. Ensure full-rect coverage (no Sync; rely on Clear+Show + DrawBorderedPanel per the UX-rendering rules).

## 4. App wiring (data feed)

- [ ] 4.1 Add `readManagedTasks() map[string]bool` in `app.go`: type-assert `a.db` to `*db.DB` → `ManagedTaskIDs()` (local, authoritative); else fall back to the union of the worker + coordinator maps from `readHeraRoles()` (remote best-effort). uxlog both the fetch count and the fallback path.
- [ ] 4.2 Call `a.tasklist.SetManagedTasks(a.readManagedTasks())` in `refreshTasksWithIDs`, beside the existing `SetHeraWorkers`/`SetHeraCoordinators` calls.
- [ ] 4.3 Wire `tasklist.OnFreelancersOnlyToggle` to a `forceRedraw`-style log line in `app.go` (debug trail only; do NOT trigger Sync).

## 5. Help overlay + tests (required by CLAUDE.md keybinding rule)

- [ ] 5.1 Add `{"f", "freelancers only"}` to the "Task List" `HelpSection` in `internal/tui/modal/help.go`.
- [ ] 5.2 Assert the `"freelancers only"` action string in `internal/tui/modal/help_test.go` (`TestHelpModal_Draw`).

## 6. SimulationScreen smoke test

- [ ] 6.1 In `internal/tui/smoke_test.go` (or `tasklist`-level smoke), seed managed + freelancer tasks, press `f` on the Tasks tab, assert the visible set narrows to freelancers and the indicator renders; press `f` again to restore. Use the existing `simApp`/`wireApp`/`runApp` harness.

## 7. Docs + gotchas

- [ ] 7.1 Update the README Reference appendix keybinding table with `f` → "freelancers only" (Reference section only; do NOT touch the marketing top half).
- [ ] 7.2 Add a non-obvious gotcha to `context/knowledge/gotchas/tasklist-ui.md`: the freelancers-only predicate is binding-derived (live `ended_at IS NULL`, kind ∈ {coordinator,worker}) and intentionally diverges from the `H` toggle's `task_meta`-sidecar source, because `task_meta` `hera.role` is never cleared on binding end; remote mode falls back to the (possibly stale) `task_meta` union.

## 8. Gate + review

- [ ] 8.1 `make fmt` then `make pre-pr` — green before reporting (stdlib-only `vuln` findings non-blocking).
- [ ] 8.2 `/ralph-review` (impl vs this change's deltas) and `/spec-audit` on the branch; auto-fix confident issues, park questions.
