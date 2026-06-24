# Task Messaging

## ADDED Requirements

### Requirement: Message delivery via reliable notify

The system SHALL deliver the PTY notification for an outbound task message through the reliable notify service rather than a bare PTY write. The delivery SHALL use the message ID as the delivery_id. The send operation (durable message insert) SHALL remain decoupled from the PTY delivery: the message is committed before the delivery is registered, and a delivery failure or missing session SHALL not fail the send.

#### Scenario: Message deliver registers a reliable delivery

- **WHEN** a task message is sent and the recipient task has a live session
- **THEN** the system registers a reliable delivery for the message ID and returns `delivered=nudged` or `delivered=queued` based on immediate vs pending state

#### Scenario: No session – delivery queued in notifier

- **WHEN** a message is sent but the recipient task has no live session
- **THEN** the message is committed, the delivery is registered as pending, and `delivered=queued` is reported (the notifier will submit when a session appears)

#### Scenario: Delivery does not gate the message commit

- **WHEN** the reliable notify service cannot submit (session busy, focused, etc.)
- **THEN** the durable message row is already committed before the notify call, so the send is not rolled back

### Requirement: Ack cancels the pending delivery

The system SHALL cancel the in-flight reliable delivery for a message when that message is acknowledged (read_at set). Cancellation is best-effort: if the delivery has already submitted, the cancel is a no-op.

#### Scenario: Ack cancels pending delivery

- **WHEN** a task acknowledges a message whose reliable delivery is still pending
- **THEN** the delivery is cancelled (no future PTY write for that message ID)

#### Scenario: Ack of already-submitted delivery is a no-op

- **WHEN** a task acknowledges a message whose delivery already submitted
- **THEN** the cancel call returns silently without error

#### Scenario: Ack of message without a delivery is a no-op

- **WHEN** a task acknowledges a message that never had a reliable delivery registered (e.g. sent when no notifier was wired)
- **THEN** the ack succeeds normally and no error is surfaced
