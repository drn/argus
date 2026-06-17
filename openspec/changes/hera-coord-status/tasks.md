# Tasks

## 1. BUG-014 — cycle the coordinator ✓ via `s`/`S` (TDD)

- [x] 1.1 Add `Selection.StatusRole()` (`internal/tui/hera/model.go`): selected role, else the orchestrator's folded coordinator role, else nil — with a failing unit test first (`TestSelection_StatusRole`).
- [x] 1.2 Resolve the target in `Ops.StepStatus` via `StatusRole()` (`internal/tui/hera/ops.go`); keep the `done` → in_review roll guarded on `Kind == worker` so a coordinator never rolls a task.
- [x] 1.3 Loosen `App.heraStatusStep` (`internal/tui/heraactions.go`) to proceed when `StatusRole() != nil` (was `Role != nil`).
- [x] 1.4 Tests: stepping a coordinator HEADER selection cycles its hera status and the rail ✓ clears on revert (`TestOps_StepStatus_CoordinatorHeader`); a coordinator `done` does not roll its task (`TestOps_StepStatus_CoordinatorDoneDoesNotRollTask`); App-level header step (`TestHeraActions_StatusStepCoordinatorHeader`); coordinator-less header stays a no-op.

## 2. BUG-015 — task-aware Details coordinator status (TDD)

- [x] 2.1 Extract the existing role-status honesty logic to `coordRoleStatusLabel` and add `coordTaskStatusLabel` (in_review / complete / failed) + `taskResultFailed` (`internal/tui/hera/details.go`).
- [x] 2.2 Combine them in `coordStatusLabel` as `<role> · task <state>` only when the task adds a terminal signal; role-only otherwise.
- [x] 2.3 Tests: a live coordinator whose task is complete surfaces completion; in_review and failed surface; ongoing/unbound add no suffix (`TestCoordStatusLabel`, `TestCoordStatusLabel_TaskAware`). `ContentHeight` is unaffected (the status line stays one row).

## 3. Validate

- [x] 3.1 `make pre-pr` passes (build, vet, fmt-check, lint-pr, vuln, coverage gate).
- [x] 3.2 Add the role-vs-task coordinator-status gotcha to `context/knowledge/gotchas/hera-view.md`.
