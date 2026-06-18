# Hera View

## MODIFIED Requirements

### Requirement: Archive, pin, rename, delete, and status ops act on the selection (area 7)

The system SHALL apply each mutation to the SELECTED `(role, orchestrator)` from the rail cursor, never a bare task ID. Archive and pin toggles read the current row state from the store to choose direction. Pinning clears archived state (pin and archive are mutually exclusive). Rename surfaces a name-conflict error for the caller to display. Status advance/revert step the hera role status ladder (idle → working → blocked → done), clamped at the ends, and reaching `done` on a WORKER role also rolls the bound task to in_review (soft-fail). The status keys (`s`/`S`) target the role resolved by `Selection.StatusRole()`: the selected role when one is selected, ELSE the orchestrator's folded coordinator role (since the coordinator renders as the orchestrator HEADER, not a child row, a header selection has `Role == nil`), ELSE nil. So `s`/`S` cycle a coordinator's hera status from a header selection — `S` moves `done → blocked` (the rail `✓` clears), `s` advances it back — while a header over a coordinator-less orchestrator (or an empty rail) is a silent no-op. Reaching `done` rolls the bound task ONLY for a WORKER-kind role, so stepping a coordinator to `done` never rolls a task. Mutations are thin adapters over existing store methods; the spawn path is the shared `agent.SpawnHeraWorker` primitive, run off the main thread.

Derived from: `internal/tui/hera/ops.go:85` (`ArchiveToggle`), `internal/tui/hera/ops.go:118` (`PinToggle`), `internal/tui/hera/ops.go:150` (`Rename`), `internal/tui/hera/ops.go:168` (`StepStatus`), `internal/tui/hera/ops.go:66` (status ladder), `internal/tui/hera/model.go` (`Selection.StatusRole`), `internal/tui/heraactions.go` (`heraStatusStep`), `context/knowledge/gotchas/hera-view.md` (M6c).

#### Scenario: Archive toggle reads current state

- **WHEN** the selected role is currently archived and the user presses `a`
- **THEN** the op unarchives it (direction is read from the store row, not the possibly-stale model flag)

#### Scenario: Worker reaching done rolls its task to in_review

- **WHEN** `s` advances a worker role to `done`
- **THEN** the hera status is set to done AND `RollHeraWorkerToReview` rolls the bound task to in_review (the roll is soft-fail so the status update always lands)

#### Scenario: Status step cycles the coordinator from a header selection

- **WHEN** `S` then `s` is pressed with the cursor on an orchestrator header whose folded coordinator role status is `done`
- **THEN** the coordinator's hera status moves `done → blocked` (the rail `✓` clears) and then `blocked → done` again, persisted via `UpsertHeraRoleStatus`

#### Scenario: Stepping a coordinator to done does not roll a task

- **WHEN** `s` advances a coordinator (header) role to `done`
- **THEN** the hera status is set to done but `RollHeraWorkerToReview` is NOT called (the roll is worker-only), so no task is rolled to in_review

#### Scenario: Status step on a coordinator-less header is a no-op

- **WHEN** `s`/`S` is pressed with the cursor on an orchestrator header that has no coordinator role (and the cursor is not on a role)
- **THEN** nothing happens (`Selection.StatusRole()` resolves to nil)

### Requirement: Agent/details region is mode-switched by the selected role (area 6)

The system SHALL render the right region as a live AGENT terminal when a worker/freelance/leaf role is selected, and as a read-only Details summary (worker roster) when a coordinator role is selected. A coordinator selection renders the agent pane unbound (no terminal).

The Details summary's `coordinator:` status line SHALL be task-aware: it combines the coordinator's hera ROLE status (preserving the BUG-003 stale-`working` honesty — a `working` role with no real activity reads `live` when its binding is alive and `stopped` when gone) with any TERMINAL bound-task signal. Terminal task states are in_review, complete, and failed (the last derived from the bound task's `TaskResult` `{"failed":true}`, mirroring `dagview.parseFailed`). When the task adds a terminal signal the line reads `"<role> · task <state>"` (e.g. `live · task complete`); an ongoing (pending / in_progress) or unbound task adds no suffix. A malformed `TaskResult` JSON blob is tolerated (no failed suffix). The line stays a single row, so `DetailsView.ContentHeight()` and the coordinator metadata block (Created / Last activity / Agent / Worktree / Repos) are unaffected.

Derived from: `internal/tui/hera/panes.go:59` (`applySelection` detailsMode), `internal/tui/hera/model.go:120` (`IsCoordinator`), `internal/tui/hera/details.go:23` (`DetailsView`), `internal/tui/hera/details.go` (`coordStatusLabel` / `coordRoleStatusLabel` / `coordTaskStatusLabel`), `internal/tui/hera/page.go:221` (Draw mode branch).

#### Scenario: Worker selection shows a terminal

- **WHEN** a worker role is selected
- **THEN** the right region shows the worker's live agent terminal

#### Scenario: Coordinator selection shows the details region

- **WHEN** a coordinator role is selected
- **THEN** the right region renders the Details roster (no terminal) stacked over the orchestration tree

#### Scenario: Coordinator status line surfaces a terminal task state

- **WHEN** the coordinator's bound task is `complete` (or `in_review`, or carries `TaskResult {"failed":true}`)
- **THEN** the `coordinator:` line appends `· task complete` (or `· task in_review`, or `· task failed`) to the role-status label

#### Scenario: Coordinator status line adds no suffix for an ongoing task

- **WHEN** the coordinator's bound task is `in_progress` (or pending, or unbound)
- **THEN** the `coordinator:` line shows only the role-status label, with no `· task …` suffix
