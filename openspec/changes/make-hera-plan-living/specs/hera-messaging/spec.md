## MODIFIED Requirements

### Requirement: hera_send recipient resolution and defaults

The system SHALL, on `hera_send`, require a non-empty body and tldr. It SHALL resolve the recipient from an explicit `to` role name within the sender's orchestrator, or default it: a worker/freelance sender with no `to` defaults to the orchestrator's active coordinator; a coordinator sender MUST supply an explicit `to`. An unknown `to` role SHALL fail naming the orchestrator. On success it returns the new message id, the recipient name, and the delivery mode.

The system SHALL accept an optional `status` argument on `hera_send` whose value is a hera role-status (`idle`/`working`/`blocked`/`done`/`failed`). When the resolved SENDER role is a `worker` or `freelance`, `status` is REQUIRED: a worker/freelance `hera_send` with no `status` SHALL fail naming the valid values. A `coordinator` sender's `status` is optional and, if omitted, leaves the coordinator's status unchanged. When `status` is supplied the system SHALL apply it to the SENDER's role SYNCHRONOUSLY within the send handler — using the same upsert-and-roll path as `hera_status` (including the worker `done` → in_review / `failed` → in_review roll) — BEFORE the call returns, so the status change never depends on the best-effort doorbell delivery path. A status-apply failure is soft-fail and SHALL NOT block the message send.

Derived from: `internal/mcp/hera.go:462` (`toolHeraSend`), `internal/mcp/hera.go:491` (recipient resolution + coordinator-must-supply-to).

#### Scenario: Worker defaults to the coordinator

- **WHEN** a worker calls hera_send with no `to`
- **THEN** the message is addressed to the orchestrator's active coordinator

#### Scenario: Coordinator must address explicitly

- **WHEN** a coordinator calls hera_send with no `to`
- **THEN** the call fails requiring an explicit recipient

#### Scenario: Worker send requires a status

- **WHEN** a worker or freelance role calls hera_send with no `status`
- **THEN** the call fails naming the valid status values and no message is sent

#### Scenario: Sent status is applied synchronously

- **WHEN** a worker calls hera_send with status=working
- **THEN** the sender's role status is set to working before the call returns, independent of whether the doorbell is delivered

#### Scenario: Sent done status rolls the worker task

- **WHEN** a worker calls hera_send with status=done and its task is in_progress
- **THEN** the task is rolled to in_review (and stamped ready_to_close) synchronously, exactly as hera_status(done) would

#### Scenario: Coordinator send does not require status

- **WHEN** a coordinator calls hera_send with an explicit `to` and no `status`
- **THEN** the message is sent and the coordinator's status is left unchanged
