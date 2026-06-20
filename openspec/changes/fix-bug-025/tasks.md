# Tasks — collapse hera task-list visibility into `H`

TDD throughout (Red → Green → Refactor). Use `internal/testutil` assertions. Gate: `make pre-pr`.

## 1. Remove the freelancers-only filter

- [x] 1.1 Delete `internal/tui/taskview/freelancers_only_test.go` and the `TestSmoke_FreelancersOnlyFilter` smoke test.
- [x] 1.2 Remove the `freelancersOnly` field, `FreelancersOnly()` getter, `ToggleFreelancersOnly()`, `OnFreelancersOnlyToggle` callback, the `f` key handler, the "freelancers" title indicator, and the `f` predicate from `tasklist.go`.
- [x] 1.3 Remove the `OnFreelancersOnlyToggle` wiring + uxlog entry from `app.go`.
- [x] 1.4 Remove the `f` "freelancers only" entries from `help.go` and `help_test.go`, and the README `f` keybinding row.

## 2. Extend `H` to the union predicate

- [x] 2.1 RED: extend `hera_workers_test.go` with a truth-table test — live coordinator hidden when `H` on / visible when off; freelancers and plain tasks always visible.
- [x] 2.2 Rename `hideHeraWorkers` → `hideHeraManaged`, `HideHeraWorkers()`/`ToggleHeraWorkers()`/`OnHeraWorkersToggle` → `HideHeraManaged()`/`ToggleHeraManaged()`/`OnHeraManagedToggle`; update all callers (app.go, hera_smoke_test.go).
- [x] 2.3 Fold `managed` into the `H` predicate: `if tl.hideHeraManaged && (tl.isHeraSpawnedWorker(t) || tl.managed[t.ID]) { continue }`. Keep `SetManagedTasks` fed every tick.
- [x] 2.4 Update the `H` help-overlay text and README keybinding row to advertise workers + coordinators.

## 3. Verify

- [x] 3.1 `go test ./internal/tui/taskview/... ./internal/tui/ ./internal/tui/modal/...` green; `go vet ./internal/tui/...` clean.
- [x] 3.2 Grep confirms ZERO dangling `freelancersOnly`/`FreelancersOnly`/`OnFreelancersOnlyToggle`/`HideHeraWorkers`/`ToggleHeraWorkers` references; build compiles.
- [x] 3.3 Update `context/knowledge/gotchas/tasklist-ui.md` entries for the merged predicate.
