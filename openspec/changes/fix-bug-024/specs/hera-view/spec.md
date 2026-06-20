# Hera View

## MODIFIED Requirements

### Requirement: Archive, pin, rename, delete, and status ops act on the selection (area 7)

The system SHALL apply each mutation to the SELECTED `(role, orchestrator)` from the rail cursor, never a bare task ID. Archive and pin toggles read the current row state from the store to choose direction. Pinning clears archived state (pin and archive are mutually exclusive). Rename surfaces a name-conflict error for the caller to display. Status advance/revert step the hera role status ladder (idle → working → blocked → done), clamped at the ends, and reaching `done` on a WORKER role also rolls the bound task to in_review (soft-fail). Stepping a WORKER role to any NON-`done` status (revert off `done`, or any other step) clears the bound task's `meta:hera.ready_to_close` mark via `ClearHeraReadyToClose` (the inverse of the done-roll's stamp), so the rail glyph — which checks `ready_to_close` FIRST in its precedence — reflects the new hera status instead of staying pinned to the review `✓`. The clear is soft-fail (the status update always lands) and touches meta only; the task's argus WORKFLOW status is owned by the session lifecycle and is never changed by a status step. Mutations are thin adapters over existing store methods; the spawn path is the shared `agent.SpawnHeraWorker` primitive, run off the main thread.

Derived from: `internal/tui/hera/ops.go:85` (`ArchiveToggle`), `internal/tui/hera/ops.go:118` (`PinToggle`), `internal/tui/hera/ops.go:150` (`Rename`), `internal/tui/hera/ops.go:175` (`StepStatus`), `internal/tui/hera/ops.go:66` (status ladder), `internal/db/hera.go` (`ClearHeraReadyToClose`), `context/knowledge/gotchas/hera-view.md` (M6c).

#### Scenario: Worker reaching done rolls its task to in_review

- **WHEN** `s` advances a worker role to `done`
- **THEN** the hera status is set to done AND `RollHeraWorkerToReview` rolls the bound task to in_review and stamps `ready_to_close` (the roll is soft-fail so the status update always lands)

#### Scenario: Stepping a worker out of done clears the review mark

- **WHEN** a worker role is `done` with its bound task carrying `meta:hera.ready_to_close=true` (rail glyph = review `✓`) and the user presses `S` (revert)
- **THEN** the hera status moves `done → blocked`, `ready_to_close` is cleared, and the rail glyph visibly changes to the blocked glyph (the review `✓` no longer masks the status)

#### Scenario: The clear never changes the task's workflow status

- **WHEN** a worker stepped to in_review (by an earlier done-roll) is reverted off `done`
- **THEN** only the `ready_to_close` meta flag is cleared; the bound task's argus workflow status is left unchanged (it stays in_review — owned by the session lifecycle, not the hera ladder)

#### Scenario: Status step on a coordinator-less header is a no-op

- **WHEN** `s`/`S` is pressed with the cursor on an orchestrator header that has no coordinator role (and the cursor is not on a role)
- **THEN** nothing happens (`Selection.StatusRole()` resolves to nil)
