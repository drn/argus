# account-move Specification

## Purpose
TBD - created by archiving change add-account-move. Update Purpose after archive.
## Requirements
### Requirement: A Claude task can be moved to another account

The system SHALL provide an account-move operation for a task on a Claude backend. The target SHALL be a configured account, `default`, or `auto`. For `auto` the system SHALL choose, among the accounts that support the backend (`default` first, then configured names in sorted order) excluding the task's current account, the one with the lowest current Claude usage, where an account's usage is the fuller of its currently valid 5-hour and weekly readings and an account with no valid reading counts as 0%, ties going to candidate order.

The move SHALL be rejected without side effects when:

- the task does not exist, or its backend is not Claude;
- the target is unknown, equals the current account, or does not support the backend;
- the target is `auto` and there is no other candidate;
- the task's session is live and not idle, and the caller did not request force;
- another move or restart of the task is already in progress (force does not override this).

The operation SHALL be carried out entirely by Argus and SHALL NOT need any agent turn or model usage on the task's current account. It SHALL NOT create a new task: the same task row, worktree, branch and hera bindings are kept, and only the agent process is replaced.

The system SHALL:

1. Ensure the target's Claude config dir exists.
2. Copy the task's newest transcript (`<session-id>.jsonl`, plus its `<session-id>/` sidecar directory when present) from the source account's `projects/<encoded worktree>/` into the target's, overwriting an older copy and leaving the source copy in place.
3. Store the target as the task's account, clearing the routed flag. Only the account and routed flag SHALL be written.
4. Stop a live session and resume it under the target account in place.

When no transcript exists, the move SHALL still complete, the session SHALL start fresh, and the result SHALL report that the conversation was not resumed. When the restart fails while the source session is still running, the previous account and routed flag SHALL be restored and the move reported as failed. Every move SHALL be logged under `[account]` with the task, source and target.

#### Scenario: Stuck agent moved and resumed
- **WHEN** an idle task on `default`, whose session hit its 5-hour cap, is moved to `personal`
- **THEN** its transcript is copied into `personal`'s projects dir, the task's account becomes `personal`, and the session resumes the same conversation under `personal`

#### Scenario: Move to auto
- **WHEN** a task on `personal` is moved with target `auto`, `default` is at 40%, and `work` is at 85%
- **THEN** the task moves to `default`

#### Scenario: Same task, same hera role
- **WHEN** a hera worker on `default` is moved to `personal`
- **THEN** no new task is created, the task keeps its ID, worktree, branch and worker binding, and its coordinator's messages still reach it

#### Scenario: Busy session refused without force
- **WHEN** a task's session is actively producing output and force is not requested
- **THEN** the move is rejected and nothing changes

#### Scenario: Busy session moved with force
- **WHEN** a task's session is actively producing output and force is requested
- **THEN** the session is stopped and the move proceeds

#### Scenario: Failed restart rolls back
- **WHEN** a move's in-place restart is refused while the source session keeps running
- **THEN** the task's stored account and routed flag are put back and the move reports failure

#### Scenario: Non-Claude task refused
- **WHEN** a codex task is moved
- **THEN** the move is rejected naming the backend

#### Scenario: Moving back keeps history
- **WHEN** a task is moved from `default` to `personal` and later back to `default`
- **THEN** it resumes the newest conversation, including turns taken on `personal`

