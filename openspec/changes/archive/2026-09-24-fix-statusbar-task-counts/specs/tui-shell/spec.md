# TUI Shell

## MODIFIED Requirements

### Requirement: Status bar summarizes every task

When no transient notice is displayed, the status bar SHALL render counts for
`active`, `pending`, `review`, and `done` from the full task snapshot. Each
persisted task, including an archived task, SHALL contribute to exactly one
count according to its stored status: `in_progress` to `active`, `pending` to
`pending`, `in_review` to `review`, and `complete` to `done`. An `in_progress`
task SHALL count as active independently of the running-session snapshot.

#### Scenario: Mixed statuses and archived tasks

- **WHEN** the task snapshot contains tasks in all four statuses, including an archived task
- **THEN** the status bar shows all four counts and their sum equals the number of tasks in the snapshot

#### Scenario: Session liveness does not change the status count

- **WHEN** an `in_progress` task is absent from the running-session snapshot
- **THEN** it contributes to the active count until its stored status changes

#### Scenario: Transient notice expires

- **WHEN** a transient notice expires
- **THEN** the status bar returns to the four-status task summary

#### Scenario: Count summary competes with key hints

- **WHEN** the terminal width cannot fit the full count summary and every key hint
- **THEN** the status bar keeps the complete count summary, omits whole intermediate hints, and retains the final help or quit hint when it fits
