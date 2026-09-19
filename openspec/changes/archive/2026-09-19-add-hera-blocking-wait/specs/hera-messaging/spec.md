## MODIFIED Requirements

### Requirement: Reading or marking-read cancels pending doorbells

The system SHALL, on `hera_inbox`, return unread messages oldest-first AND cancel pending doorbell deliveries for them (reading implies the recipient saw the doorbell), then mark them read. `hera_inbox` SHALL accept an optional integer `timeout_seconds`: omitted or zero SHALL query immediately, while values from 1 through 120 SHALL block server-side until at least one unread message exists, the timeout elapses, or daemon shutdown cancels the wait. The wait SHALL perform a fast-path unread query before polling, SHALL use Hera's `read_at IS NULL` unread convention, and SHALL apply the same delivery-cancellation and read-acknowledgement behavior to messages returned after waiting. Negative values and values above 120 SHALL be rejected at the tool layer. `hera_mark_read` SHALL mark the supplied ids read for the caller's role and cancel their pending deliveries, returning the count flipped. Cancellation is per-message (delivery id `hera:<id>`), not task-scoped, mirroring `task_message_ack`. The claim-mode `hera_join` path SHALL count unread WITHOUT cancelling deliveries.

Derived from: `internal/hera/service.go` (`Inbox` cancels), `internal/hera/service.go` (`MarkRead`), `internal/hera/service.go` (`cancelDeliveries` per-message), `internal/mcp/hera.go` (`toolHeraInbox`), `internal/db/hera_messages.go` (`WaitForHeraInbox`), `internal/mcp/hera.go` (claim mode does not cancel).

#### Scenario: Inbox read cancels its doorbells

- **WHEN** the caller reads its inbox via hera_inbox
- **THEN** the returned messages' pending doorbell deliveries are cancelled and the messages are marked read

#### Scenario: Blocking inbox returns newly arrived mail

- **WHEN** the caller invokes `hera_inbox` with `timeout_seconds` from 1 through 120 and unread mail arrives before the deadline
- **THEN** the call returns that mail oldest-first, cancels its pending doorbells, and marks it read

#### Scenario: Blocking inbox fast path returns existing mail

- **WHEN** unread mail already exists before a blocking `hera_inbox` begins
- **THEN** the call returns immediately without waiting for the first polling interval

#### Scenario: Blocking inbox times out empty

- **WHEN** no unread mail arrives before `timeout_seconds` elapses
- **THEN** the call returns an empty-inbox result without an error

#### Scenario: Blocking inbox validates timeout bounds

- **WHEN** `timeout_seconds` is negative or greater than 120
- **THEN** the tool rejects the call before querying the inbox

#### Scenario: Daemon shutdown cancels blocking inbox

- **WHEN** daemon shutdown begins while `hera_inbox` is waiting
- **THEN** the wait returns promptly without delaying shutdown

#### Scenario: Mark-read flips and cancels by id

- **WHEN** hera_mark_read is called with specific ids
- **THEN** only those rows addressed to the caller are flipped read and their per-message doorbells cancelled, and the flipped count is returned

#### Scenario: Claim-mode join does not cancel

- **WHEN** a task claims its existing role via hera_join (no role_name)
- **THEN** the unread count is reported but no pending deliveries are cancelled
