## MODIFIED Requirements

### Requirement: Schedule lifecycle over REST

The API MUST support creating, listing, updating, and deleting schedules. New schedules default to enabled and MUST have their next-run time pre-populated so the UI can preview it before the first tick. Updates MUST support partial field changes (e.g. toggling enabled without resending the prompt), including the optional per-schedule model override. Updating or deleting a non-existent schedule MUST return 404 Not Found.

#### Scenario: Create returns the persisted schedule
- **WHEN** a valid create request is submitted
- **THEN** the schedule is persisted with a generated ID, defaults to enabled, has a populated next-run time, and is returned with 201 Created

#### Scenario: Partial update toggles a single field
- **WHEN** an update request sets only `enabled` to false
- **THEN** the schedule's enabled flag is updated and all other fields are preserved, returned with 200 OK

#### Scenario: Partial update sets the model override
- **WHEN** an update request sets only `model` to a non-empty identifier
- **THEN** the schedule's model is updated, all other fields are preserved, and the value round-trips on a subsequent read

#### Scenario: Delete removes the schedule
- **WHEN** a delete request targets an existing schedule
- **THEN** the schedule is removed and returns 204 No Content, and a subsequent list no longer contains it

#### Scenario: Operating on a missing schedule
- **WHEN** an update or delete targets an ID that does not exist
- **THEN** the response is 404 Not Found

### Requirement: Firing creates a task and records bookkeeping

When a schedule fires, the scheduler MUST create a task using the schedule's name, prompt, project, backend override, and model override. The fired task name MUST be made unique per fire by appending the fire timestamp so concurrent worktrees cannot collide. On success the schedule MUST record the last-run time, the created task ID, and clear any prior error. The backend and model overrides MUST reach task creation at fire time so the launched agent runs on the overriding backend and model. An empty model override MUST fall back to the backend's configured default model (the prior behavior).

#### Scenario: Fire creates a task with the schedule's fields
- **WHEN** a due schedule fires
- **THEN** a task is created carrying the schedule's project and prompt, and the schedule records last-run time and the new task ID

#### Scenario: Backend override is passed at fire time
- **WHEN** a schedule with a non-empty backend override fires (via tick or run-now)
- **THEN** the created task is launched with that backend

#### Scenario: Model override is passed at fire time
- **WHEN** a schedule with a non-empty model override fires (via tick or run-now)
- **THEN** the created task carries that model and its agent is launched with that model rather than the backend's default

#### Scenario: Empty model override uses the backend default
- **WHEN** a schedule with an empty model override fires
- **THEN** the created task carries no model and the agent runs on the backend's configured default model
