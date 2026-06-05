# Task Scheduling

## Purpose

Lets an operator register prompts that spawn argus tasks automatically — either on a recurring cron cadence or once at a specific future time. A daemon-resident scheduler fires due schedules by creating tasks through the standard headless task-creation flow, and a master-only REST surface lets operators list, create, update, delete, and manually run schedules.

## Requirements

### Requirement: Cadence validation

A schedule MUST specify exactly one cadence: either a cron expression (`schedule`) or a one-shot fire time (`run_once_at`), never both and never neither. A schedule MUST also have a non-empty name, project, and prompt. Cron expressions accept standard 5-field syntax, descriptors (`@hourly`, `@daily`, etc.), and intervals (`@every 30m`). A one-shot fire time MUST be RFC3339-formatted and in the future. A request specifying both cadences in one call MUST be rejected.

#### Scenario: Missing required field is rejected
- **WHEN** a create request omits name, project, prompt, or any cadence
- **THEN** the request is rejected with a 400 Bad Request and no schedule is persisted

#### Scenario: Malformed cron expression is rejected
- **WHEN** a create request supplies a cron expression that does not parse
- **THEN** the request is rejected with a 400 Bad Request

#### Scenario: Past or malformed run_once_at is rejected
- **WHEN** a create request supplies a `run_once_at` that is in the past or not RFC3339
- **THEN** the request is rejected with a 400 Bad Request

#### Scenario: Both cadences in one request is rejected
- **WHEN** a request supplies both a non-empty cron expression and a non-empty `run_once_at`
- **THEN** the request is rejected with a 400 Bad Request and an error indicating exactly one cadence must be chosen

### Requirement: Schedule lifecycle over REST

The API MUST support creating, listing, updating, and deleting schedules. New schedules default to enabled and MUST have their next-run time pre-populated so the UI can preview it before the first tick. Updates MUST support partial field changes (e.g. toggling enabled without resending the prompt). Updating or deleting a non-existent schedule MUST return 404 Not Found.

#### Scenario: Create returns the persisted schedule
- **WHEN** a valid create request is submitted
- **THEN** the schedule is persisted with a generated ID, defaults to enabled, has a populated next-run time, and is returned with 201 Created

#### Scenario: Partial update toggles a single field
- **WHEN** an update request sets only `enabled` to false
- **THEN** the schedule's enabled flag is updated and all other fields are preserved, returned with 200 OK

#### Scenario: Delete removes the schedule
- **WHEN** a delete request targets an existing schedule
- **THEN** the schedule is removed and returns 204 No Content, and a subsequent list no longer contains it

#### Scenario: Operating on a missing schedule
- **WHEN** an update or delete targets an ID that does not exist
- **THEN** the response is 404 Not Found

### Requirement: Cadence switching clears the prior anchor

When an update changes the cadence, the previously-set cadence anchor MUST be cleared automatically so a row is never simultaneously recurring and one-shot. Switching to a one-shot clears the cron expression; switching to a cron expression clears the one-shot time. Whenever the cadence changes, the next-run time MUST be recomputed.

#### Scenario: Recurring switched to one-shot
- **WHEN** a recurring schedule is updated with a future `run_once_at`
- **THEN** the cron expression is cleared and the schedule becomes a one-shot

#### Scenario: One-shot switched to recurring
- **WHEN** a one-shot schedule is updated with a cron expression
- **THEN** the one-shot fire time is cleared and the schedule becomes recurring

#### Scenario: Editing a never-run schedule anchors next-run on now
- **WHEN** the cron expression of a schedule that has never fired is updated
- **THEN** the recomputed next-run time is a real future time anchored on the present, not a year-0001 date that would fire on the next tick

### Requirement: Master-only access

All schedule endpoints (list, create, update, delete, run-now) MUST require master authorization. Per-device tokens MUST be denied because schedule prompts can carry sensitive operational context.

#### Scenario: Device token is forbidden
- **WHEN** any schedule endpoint is called with a per-device token rather than the master token
- **THEN** the response is 403 Forbidden

### Requirement: First fire is deferred, not immediate

A recurring schedule MUST NOT fire on the first scheduler tick after creation. The first tick only computes and persists the next-run time; the schedule fires only on a later tick once its next-run time has passed. Disabled schedules MUST still have their next-run time populated for UI preview, but MUST NOT fire.

#### Scenario: No fire on the initial tick
- **WHEN** the scheduler ticks for the first time after a recurring schedule is created
- **THEN** no task is created and the schedule's next-run time is populated

#### Scenario: Fire once the next-run time passes
- **WHEN** a later tick observes that an enabled schedule's next-run time has passed
- **THEN** the scheduler creates exactly one task and recomputes the next-run time to a future instant

#### Scenario: Disabled schedule never fires
- **WHEN** the scheduler ticks for a disabled schedule whose next-run time has passed
- **THEN** no task is created but the next-run time is still populated for display

### Requirement: Firing creates a task and records bookkeeping

When a schedule fires, the scheduler MUST create a task using the schedule's name, prompt, project, and backend override. The fired task name MUST be made unique per fire by appending the fire timestamp so concurrent worktrees cannot collide. On success the schedule MUST record the last-run time, the created task ID, and clear any prior error. The backend override MUST reach task creation at fire time so the launched agent runs on the overriding backend.

#### Scenario: Fire creates a task with the schedule's fields
- **WHEN** a due schedule fires
- **THEN** a task is created carrying the schedule's project and prompt, and the schedule records last-run time and the new task ID

#### Scenario: Backend override is passed at fire time
- **WHEN** a schedule with a non-empty backend override fires (via tick or run-now)
- **THEN** the created task is launched with that backend

### Requirement: Fire failures are surfaced without losing the row

If task creation fails when a schedule fires, the scheduler MUST persist the error for display and MUST NOT drop the schedule. For recurring rows the next-run time advances normally; for one-shot rows the schedule stays enabled so it (or the user) can retry, and its fire time remains visible.

#### Scenario: Recurring fire failure records the error
- **WHEN** task creation fails on a recurring schedule fire
- **THEN** the schedule's last error is populated and no task is reported created

#### Scenario: One-shot fire failure keeps the row enabled
- **WHEN** task creation fails on a due one-shot schedule
- **THEN** the schedule's last error is populated, it remains enabled, and its next-run time stays populated

### Requirement: One-shot fires exactly once then auto-disables

A one-shot schedule MUST fire once when its fire time has passed and it is enabled, then auto-disable and clear its next-run time so it cannot fire again. Until it fires, its next-run time mirrors its fire time. Once it has fired (last-run time set), it MUST never fire again — even if the user re-enables it.

#### Scenario: Fires once and disables
- **WHEN** an enabled one-shot schedule's fire time passes and the scheduler ticks
- **THEN** exactly one task is created, the schedule auto-disables, its next-run time is cleared, and later ticks create no further tasks

#### Scenario: Re-enabling a fired one-shot does not refire
- **WHEN** a one-shot schedule that already fired is manually re-enabled
- **THEN** subsequent ticks create no new task and the next-run time stays cleared

#### Scenario: Disabled one-shot with a past time never fires
- **WHEN** a disabled one-shot schedule whose fire time is in the past is ticked
- **THEN** no task is created

### Requirement: Run-now fires out of cycle without double-firing

The run-now action MUST fire a schedule immediately regardless of its next-run time, updating bookkeeping so the regular tick will not fire the same schedule again right after. A one-shot run via run-now MUST also auto-disable the row. Running a non-existent schedule MUST return 404. Run-now on a recurring schedule with a malformed cron expression MUST persist the parse error and not fire.

#### Scenario: Run-now fires immediately and advances bookkeeping
- **WHEN** an operator triggers run-now on a recurring schedule
- **THEN** exactly one task is created, the last-run time is recorded, and a concurrent or following tick does not produce a duplicate task

#### Scenario: Run-now on a one-shot auto-disables
- **WHEN** an operator triggers run-now on a future-dated one-shot schedule
- **THEN** a task is created and the schedule auto-disables even though its fire time had not yet arrived

#### Scenario: Run-now on a missing schedule
- **WHEN** run-now targets an ID that does not exist
- **THEN** the response is 404 Not Found

#### Scenario: Run-now is unavailable without a running scheduler
- **WHEN** run-now is called but no scheduler is wired into the API
- **THEN** the response is 503 Service Unavailable

### Requirement: Manual fires suppress automatic notifications

The scheduler MAY notify on a fire via a callback. The callback MUST be invoked only for automatic (tick-driven) fires that succeed; it MUST NOT be invoked for manual run-now fires (which are explicit user actions) nor when a fire fails.

#### Scenario: Notification on a successful tick fire
- **WHEN** an automatic tick fire successfully creates a task and a fire callback is registered
- **THEN** the callback is invoked once with the created task

#### Scenario: No notification for run-now
- **WHEN** a schedule is fired via run-now
- **THEN** the fire callback is not invoked

#### Scenario: No notification when a fire fails
- **WHEN** an automatic tick fire fails to create a task
- **THEN** the fire callback is not invoked
