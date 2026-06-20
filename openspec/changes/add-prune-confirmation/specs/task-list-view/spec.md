# Task List View

## MODIFIED Requirements

### Requirement: Prune completed tasks is confirmation-gated

The system SHALL gate the Tasks-tab `Ctrl+R` "prune completed tasks" action
behind an explicit y/N confirmation before any deletion occurs. On `Ctrl+R`
(Tasks tab, task-list mode), the system SHALL count the completed tasks and:

- WHEN at least one completed task exists, open a confirmation modal
  (`modal.ConfirmModal`, title "Prune completed tasks") whose message names the
  count and states that the tasks' worktrees and branches will be removed and
  the action cannot be undone. The destructive two-phase prune SHALL run ONLY on
  an explicit confirm (`Enter` or `y`); `Esc`, `Ctrl+Q`, or `n` SHALL dismiss
  the modal and remove nothing.
- WHEN no completed task exists, skip the modal entirely and surface a brief
  status note ("No completed tasks to prune") instead of acting silently.

The system SHALL NOT open the confirmation while a prune is already in progress
(the header cleanup notice is set), preserving the prior double-`Ctrl+R`
re-entrancy guard. The `Ctrl+R` keybinding and its "prune completed" label are
unchanged, so the help modal and statusbar are unaffected.

#### Scenario: Ctrl+R opens the confirmation, prunes nothing yet

- **WHEN** the user presses `Ctrl+R` on the Tasks tab with at least one completed task
- **THEN** the confirm-prune modal opens and no task is removed until the user confirms

#### Scenario: Confirming prunes completed tasks

- **WHEN** the confirm-prune modal is open and the user presses `Enter` or `y`
- **THEN** the modal is dismissed and the completed tasks are pruned (DB rows, sessions, worktrees, and branches), leaving non-completed tasks intact

#### Scenario: Cancelling removes nothing

- **WHEN** the confirm-prune modal is open and the user presses `Esc` or `n`
- **THEN** the modal is dismissed and every task — completed and active — remains

#### Scenario: Nothing to prune skips the modal

- **WHEN** the user presses `Ctrl+R` with no completed tasks
- **THEN** no modal opens and a "No completed tasks to prune" status note is shown
