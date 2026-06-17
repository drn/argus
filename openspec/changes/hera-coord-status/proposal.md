## Why

Two coordinator-status defects in the native Hera view:

- **BUG-014 — the coordinator ✓ cannot be cycled with `s`/`S`.** A coordinator that finished work shows `✓` in the rail (the coordinator role's hera status is `done`). The operator cannot step it back down: `s`/`S` on a coordinator lands on the orchestrator HEADER (the coordinator role is folded into the header, so the selection has `Role == nil`). Both `App.heraStatusStep` and `hera.Ops.StepStatus` early-return on a `Role == nil` selection, so the status ladder never runs on a coordinator and the ✓ is stuck.

- **BUG-015 — the Details coordinator status line is not task-aware.** `coordStatusLabel` (`internal/tui/hera/details.go`) derives the coordinator status purely from the hera ROLE status (live / stopped / working / done), never from the bound argus TASK status. So when a coordinator's task is `complete`, the Details line can still read `coordinator: live`, disagreeing with the operator's expectation that the line reflect completion.

## What Changes

- **BUG-014:** Resolve the status-step target through a new `Selection.StatusRole()` — the selected role directly, or the orchestrator's folded coordinator role for a header selection. `Ops.StepStatus` and `App.heraStatusStep` use it, so `s`/`S` step a coordinator's hera status (`S` moves `done` → `blocked` → `working` → `idle`, clearing the rail ✓; `s` advances back). The status persists across restart (it is a plain `UpsertHeraRoleStatus`). Worker behavior is unchanged — the `done` → in_review task roll stays guarded on `Kind == worker`, so stepping a coordinator to `done` never rolls a task. A header with no coordinator role remains a silent no-op. No new key — `s`/`S` are reused, so the help overlay (already "advance / revert status", unscoped) is unchanged.

- **BUG-015:** Make `coordStatusLabel` task-aware. It combines the role-status label (the existing BUG-003 honesty logic, extracted to `coordRoleStatusLabel`) with a task-status label (`coordTaskStatusLabel`) that surfaces notable TERMINAL task states — `in review`, `complete`, or `failed` (from `TaskResult` `{"failed":true}`, mirroring the orchestration tree's failed-node detector). When the task adds a signal the role status doesn't already convey, BOTH are shown as `<role> · task <state>`; an ongoing (`in_progress`/`pending`) or unbound task adds no suffix. The coord-details metadata block (Created / Last activity / Repos) is untouched — this is additive to the status line only.

## Capabilities

### Modified Capabilities

- `hera-view`: `s`/`S` step a coordinator's hera status from a header selection (cycling the rail ✓); the Details coordinator status line is task-aware (surfaces in_review/complete/failed alongside the role status).

## Impact

- **Modified code:** `internal/tui/hera/model.go` (`Selection.StatusRole`), `internal/tui/hera/ops.go` (`StepStatus` target resolution), `internal/tui/heraactions.go` (`heraStatusStep` guard), `internal/tui/hera/details.go` (task-aware `coordStatusLabel` + `coordRoleStatusLabel`/`coordTaskStatusLabel`/`taskResultFailed`).
- **Tests:** `internal/tui/hera/ops_test.go`, `internal/tui/hera/model_test.go`, `internal/tui/hera/details_test.go`, `internal/tui/heraactions_test.go`.
- **Dependencies / data:** none added — every input is already loaded by `BuildModel`.
- **Out of scope:** worker status stepping (unchanged), the coord-details metadata block, the rail spinner/icon vocabulary, the embedded Orchestration Tree.
