# hera-view

## ADDED Requirements

### Requirement: `s`/`S` step a coordinator's hera status from a header selection

The native Hera rail SHALL allow the `s` (advance) and `S` (revert) keys to step the HERA STATUS of a coordinator. Because the coordinator role is folded into the orchestrator header (it has no row of its own), a header selection (`Role == nil`, `Orch` set) SHALL resolve the orchestrator's coordinator role as the status-step target. Stepping SHALL move the coordinator role's status along the `idle → working → blocked → done` ladder (clamped at the ends), so `S` moves a finished coordinator's `done` back down and the rail's coordinator `✓` glyph clears, and `s` advances it back. The new status SHALL persist (a plain role-status upsert) so it survives a restart.

Worker behavior SHALL be unchanged: the `done` → roll-task-to-in_review side effect SHALL stay guarded on a WORKER-kind role, so stepping a coordinator to `done` never rolls a task. A header selection with no coordinator role SHALL be a silent no-op. No new keybinding is introduced — `s`/`S` are reused, so the help overlay text ("advance / revert status") is unchanged.

Derived from: `internal/tui/hera/model.go` (`Selection.StatusRole`), `internal/tui/hera/ops.go` (`Ops.StepStatus`), `internal/tui/heraactions.go` (`heraStatusStep`).

#### Scenario: Reverting a finished coordinator clears the rail ✓
- **WHEN** a coordinator whose hera role status is `done` (rail shows `✓`) is selected via its orchestrator header and the operator presses `S`
- **THEN** the coordinator role's status steps to `blocked` and the rail glyph is no longer `✓`

#### Scenario: Advancing steps the coordinator back up
- **WHEN** a coordinator role at `blocked` is selected via its header and the operator presses `s`
- **THEN** the coordinator role's status steps to `done` (restoring the rail `✓`)

#### Scenario: A coordinator reaching done does not roll its task
- **WHEN** a coordinator role is stepped to `done`
- **THEN** its bound argus task is NOT rolled to in_review (the roll is worker-only)

#### Scenario: A coordinator-less header is a no-op
- **WHEN** `s`/`S` is pressed on an orchestrator header that has no coordinator role
- **THEN** no status is written and no error is surfaced

### Requirement: Coordinator Details status line is task-aware

The coordinator Details status line SHALL combine the hera ROLE status with the bound argus TASK status so it never disagrees with completion. The role-status half SHALL keep the existing honesty rules (a stale `working` not backed by real activity reads `live` when the binding is alive, `stopped` when it is gone). When the bound task is in a notable TERMINAL state that the role status does not already convey — `in review`, `complete`, or `failed` (the latter read from the task result's `{"failed":true}`, matching the orchestration tree's failed-node detector) — the line SHALL surface BOTH as `<role> · task <state>`. An ongoing task (`in_progress` / `pending`) or an unbound coordinator SHALL add no task suffix (the role status is the live signal there). The coord-details metadata block (Created / Last activity / Agent / Worktree / Repos in scope / Summary) SHALL remain unchanged, and the status line SHALL remain a single rendered row (so `ContentHeight` is unaffected).

Derived from: `internal/tui/hera/details.go` (`coordStatusLabel`, `coordRoleStatusLabel`, `coordTaskStatusLabel`, `taskResultFailed`).

#### Scenario: A finished coordinator surfaces task completion
- **WHEN** a coordinator with a live binding whose bound task is `complete` is selected
- **THEN** the Details status line reads `live · task complete` (rather than just `live`)

#### Scenario: in_review and failed are surfaced
- **WHEN** the bound task is `in_review`, or its result is `{"failed":true}`
- **THEN** the status line appends `· task in review` / `· task failed` to the role-status label

#### Scenario: An ongoing or unbound coordinator shows role status alone
- **WHEN** the bound task is `in_progress`/`pending`, or the coordinator is unbound
- **THEN** the status line shows only the role-status label (no task suffix)
