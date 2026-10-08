## MODIFIED Requirements

### Requirement: Task lifecycle transitions

`task_stop` SHALL send a stop signal (reporting an eventual transition to in_review) and require an `id`. `task_complete` SHALL set status to complete (stamping the end time) and be a no-op when already complete; it does not itself stop a running session; the session is stopped once idle by the agent-execution capability's finished-session check. `task_archive` SHALL set or toggle the archived flag, report a no-op when the requested state already holds, and on archive best-effort clear queued messages for the task; an archived task's session is likewise stopped once idle by that check. `task_rename` SHALL require a non-empty, length-capped name, update only the display name, and report a no-op when the name is unchanged.

#### Scenario: Stop requires id

- **WHEN** `task_stop` is called without an `id`
- **THEN** the response is a tool error reporting id is required

#### Scenario: Complete is a no-op when already complete

- **WHEN** `task_complete` resolves a task already in complete status
- **THEN** the task is unchanged and the result reports it is already complete

#### Scenario: Archive toggles when no explicit state given

- **WHEN** `task_archive` resolves a task and no `archived` value is supplied
- **THEN** the archived flag is flipped to its opposite value

#### Scenario: Rename rejects empty name

- **WHEN** `task_rename` is called with a name that is empty after trimming
- **THEN** the response is a tool error reporting name is required
