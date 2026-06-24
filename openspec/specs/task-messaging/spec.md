# Task Messaging

## Purpose

Task Messaging lets one task send durable messages to another task and lets a task read and acknowledge its inbox. It exposes this over the REST API (mirroring the equivalent MCP tool surface) so the mobile PWA inbox view and admin clients can list, send, and mark messages read. Messages persist independently of agent process lifecycle and carry caps to protect recipients from overload.
## Requirements
### Requirement: Listing a task inbox

The system SHALL return the inbox for a path-bound task, including the messages and the task's current unread count. Listing SHALL default to unread-only and accept filters for read state, sender, a `since` timestamp, and a result limit. A request for a task that does not exist SHALL fail with a not-found error.

#### Scenario: Empty inbox

- **WHEN** a client lists the inbox for an existing task that has no messages
- **THEN** the response succeeds with an empty message list and an unread count of zero

#### Scenario: Inbox reflects a received message

- **WHEN** a message has been sent to a task and the client lists that task's inbox
- **THEN** the response includes that message and reports an unread count of one

#### Scenario: Unread-only toggle synonyms

- **WHEN** a client passes `unread_only` as `false`, `0`, or `no`
- **THEN** the request succeeds and already-acknowledged messages are included

#### Scenario: Sender, since, and limit filters

- **WHEN** a client lists an inbox with a sender filter, a valid `since` timestamp, and a limit
- **THEN** the request succeeds and returns only matching messages

#### Scenario: Unknown task

- **WHEN** a client lists the inbox for a task ID that does not exist
- **THEN** the request fails with a not-found error

### Requirement: Validating inbox filter parameters

The system SHALL reject malformed inbox filter parameters with a bad-request error rather than silently ignoring them. The `since` parameter SHALL be accepted in RFC3339 form (with or without sub-second precision) and rejected otherwise; the `limit` parameter SHALL be a non-negative integer.

#### Scenario: Invalid since timestamp

- **WHEN** a client supplies a `since` value that is not a valid RFC3339 timestamp
- **THEN** the request fails with a bad-request error identifying the invalid `since`

#### Scenario: Invalid limit

- **WHEN** a client supplies a `limit` value that is not a non-negative integer
- **THEN** the request fails with a bad-request error identifying the invalid limit

### Requirement: Sending a message between tasks

The system SHALL stage a durable message from the path-bound sender task to a named recipient task. The request SHALL require both a recipient and a non-empty body, default the message kind to a note when omitted, and accept only the recognized kinds (note, question, answer). On success the system SHALL return the new message identifier and its creation timestamp. The sender task and the recipient task SHALL both exist.

#### Scenario: Successful send

- **WHEN** an authorized client sends a message with a recipient and body to an existing recipient task
- **THEN** the message is created and the new message identifier and creation timestamp are returned

#### Scenario: Missing recipient or body

- **WHEN** a send request omits the recipient or omits the body
- **THEN** the request fails with a bad-request error

#### Scenario: Sender task does not exist

- **WHEN** the path-bound sender task ID does not resolve
- **THEN** the request fails with a not-found error

#### Scenario: Recipient task does not exist

- **WHEN** the named recipient task does not exist
- **THEN** the request fails with a not-found error identifying the missing recipient

#### Scenario: Unknown message kind

- **WHEN** a send request supplies a message kind that is not note, question, or answer
- **THEN** the request fails with a bad-request error naming the invalid kind

#### Scenario: Malformed request body

- **WHEN** a send request body is not valid JSON
- **THEN** the request fails with a bad-request error

### Requirement: Sending is master-only over REST

The system SHALL restrict the REST send endpoint to master-tier authentication. Device-tier tokens SHALL be rejected, because the mobile inbox view is read-only and outbound sends mutate shared state across tasks.

#### Scenario: Device token rejected

- **WHEN** a client authenticated with a device token attempts to send a message
- **THEN** the request fails with a forbidden error and no message is created

### Requirement: Message caps

The system SHALL reject messages that violate protective caps and surface each cap rejection as a bad-request error with the reason preserved. The caps SHALL cover an oversized body, a self-send (sender equals recipient), a full recipient inbox, and exceeding the send rate limit.

#### Scenario: Self-send rejected

- **WHEN** a client sends a message whose recipient is the same as the sender task
- **THEN** the request fails with a bad-request error indicating a self-send

#### Scenario: Oversized body rejected

- **WHEN** a client sends a message whose body exceeds the maximum body size
- **THEN** the request fails with a client error rather than a server error

### Requirement: Acknowledging inbox messages

The system SHALL mark the supplied message identifiers read for the path-bound task and return the count of messages actually flipped. The path-bound task SHALL exist, at least one identifier SHALL be supplied, and the number of identifiers per call SHALL not exceed the maximum. Identifiers not belonging to the path-bound task SHALL be silently ignored and excluded from the returned count.

#### Scenario: Acknowledge an unread message

- **WHEN** a client acknowledges the identifier of an unread message addressed to the task
- **THEN** the response reports one message acknowledged and the task's unread count drops to zero

#### Scenario: Acknowledge for unknown task

- **WHEN** a client acknowledges messages for a task ID that does not exist
- **THEN** the request fails with a not-found error

#### Scenario: Empty identifier list rejected

- **WHEN** an acknowledge request supplies no identifiers
- **THEN** the request fails with a bad-request error

#### Scenario: Too many identifiers rejected

- **WHEN** an acknowledge request supplies more identifiers than the per-call maximum
- **THEN** the request fails with a bad-request error stating the maximum

### Requirement: Message cleanup on task lifecycle

The system SHALL remove a task's messages when the task leaves active use, so a recipient never remains pinned at its unread cap by messages bound to a departed task. Archiving a task SHALL clear its unread messages, and deleting a task SHALL remove all messages referencing it on both the sending and receiving side.

#### Scenario: Archive clears unread messages

- **WHEN** a task with unread messages is archived
- **THEN** that task's unread count becomes zero

#### Scenario: Delete cascades both message legs

- **WHEN** a task that has both sent and received messages is deleted
- **THEN** no surviving message references the deleted task on either side

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

