## MODIFIED Requirements

### Requirement: hera_status updates role status and rolls a finished worker

The system SHALL, on `hera_status`, validate the status as one of idle/working/blocked/done/failed, upsert it on the caller's role, and mirror it to the `task_meta` "hera" namespace best-effort. When a WORKER role reports `done` the system SHALL roll its bound task to in_review and stamp `ready_to_close` via `RollHeraWorkerToReview` — the primary BUG-050 trigger for the idle-but-done case the exit hook misses. That roll is worker-kind only, no-ops unless the task is currently in_progress (so it never clobbers a human-set in_review/complete and never auto-completes), leaves the live session running, is idempotent, and is soft-fail (a failure never blocks the status update).

`hera_status` SHALL additionally accept two optional parameters, valid only for a `coordinator`-kind caller: `handoff_note` (a short free-text string) and `request_recycle` (a boolean). When `handoff_note` is supplied, the system SHALL overwrite `task_meta` (namespace `hera`, key `handoff_note`) with it in the same call. When `request_recycle` is `true`, the system SHALL record a pending-recycle intent for the caller's task, which the `recycle_coord` primitive (see `coordinator-context-management`) consumes to defer the actual restart until the session is idle. Supplying either parameter for a non-coordinator caller SHALL be rejected with an error naming the parameter.

Derived from: `internal/mcp/hera.go:643` (`toolHeraStatus`), `internal/mcp/hera.go:691` (BUG-050 worker roll), `internal/tui/hera/ops.go:193` (the same roll mirrored on the rail `s` key).

#### Scenario: Invalid status is rejected

- **WHEN** hera_status is called with a status other than idle/working/blocked/done/failed
- **THEN** the tool errors naming the valid values

#### Scenario: Worker done rolls to in_review

- **WHEN** a worker role reports status=done and its task is in_progress
- **THEN** the task is rolled to in_review and stamped ready_to_close, while the session keeps running

#### Scenario: Done roll never clobbers a non-progress task

- **WHEN** a worker reports done but its task is already in_review or complete
- **THEN** RollHeraWorkerToReview no-ops and the status update still succeeds

#### Scenario: Coordinator can record a handoff note

- **WHEN** a coordinator calls hera_status with a non-empty handoff_note
- **THEN** task_meta (hera, handoff_note) is overwritten with that text in the same call

#### Scenario: Coordinator can request recycle

- **WHEN** a coordinator calls hera_status with request_recycle=true
- **THEN** a pending-recycle intent is recorded for the caller's task

#### Scenario: Non-coordinator cannot use the new parameters

- **WHEN** a worker or freelance role calls hera_status with handoff_note or request_recycle set
- **THEN** the tool errors naming the offending parameter, and no task_meta write or recycle intent occurs
