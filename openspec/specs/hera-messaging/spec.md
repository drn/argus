# Hera Messaging

## Purpose

Hera Messaging is the role-addressed message bus beneath the Hera View (comparison area 9: messaging / doorbell surfacing). It persists messages between hera roles durably in SQLite and delivers them to a recipient's live agent pane via the SAME reliable, idle-gated notifier the `task_messages` path uses — no second delivery engine is introduced. This mirrors the plugin's `hera-delivery-receipt` capability.

This is a faithful capture of current native behavior. Each requirement cites `file:line`; native-vs-plugin differences carry a `NOTE:`.

## Requirements

### Requirement: Role-addressed durable message store with caps

The system SHALL persist hera messages addressed role→role, carrying a body, a one-line `tldr` subject, an optional `in_reply_to`, send timestamp, delivery stamp, and read state. It SHALL enforce three caps: body ≤ 64 KiB, recipient unread ≤ 500, and sender rolling 60-second rate ≤ 50 sends/min. It SHALL reject a self-send, a missing or too-long tldr, and a recipient role that is missing or archived. Storage is always durable regardless of delivery outcome.

Derived from: `internal/db/hera_messages.go:42` (`HeraMaxUnreadPerRole`=500, `HeraMaxSendsPerMinute`=50, window=1min), `internal/db/hera_messages.go:15` (body 64 KiB cap), `internal/db/hera_messages.go:80` (`SendHeraMessage` validation), `internal/mcp/hera.go:520` (error mapping in `toolHeraSend`).

#### Scenario: Body over 64 KiB is rejected

- **WHEN** a send carries a body larger than 64 KiB
- **THEN** the send fails with a body-too-large error and nothing is persisted

#### Scenario: Full inbox is rejected

- **WHEN** the recipient already holds 500 unread messages
- **THEN** the send fails with an inbox-full error

#### Scenario: Rate limit is enforced

- **WHEN** the sender has emitted 50 messages in the last minute
- **THEN** the next send fails with a rate-limited error

#### Scenario: Self-send and bad tldr are rejected

- **WHEN** a send targets the sender's own role, omits the tldr, or supplies a tldr over 120 characters
- **THEN** the send fails with the corresponding validation error

### Requirement: hera_send recipient resolution and defaults

The system SHALL, on `hera_send`, require a non-empty body and tldr. It SHALL resolve the recipient from an explicit `to` role name within the sender's orchestrator, or default it: a worker/freelance sender with no `to` defaults to the orchestrator's active coordinator; a coordinator sender MUST supply an explicit `to`. An unknown `to` role SHALL fail naming the orchestrator. On success it returns the new message id, the recipient name, and the delivery mode.

Derived from: `internal/mcp/hera.go:462` (`toolHeraSend`), `internal/mcp/hera.go:491` (recipient resolution + coordinator-must-supply-to).

#### Scenario: Worker defaults to the coordinator

- **WHEN** a worker calls hera_send with no `to`
- **THEN** the message is addressed to the orchestrator's active coordinator

#### Scenario: Coordinator must address explicitly

- **WHEN** a coordinator calls hera_send with no `to`
- **THEN** the call fails requiring an explicit recipient

### Requirement: Reliable doorbell delivery via the shared notifier

The system SHALL deliver a message to the recipient's live argus task by enqueueing a doorbell line through the existing `notify.Notifier` (`ReliableNotify`) — the single idle-gated, exactly-once pane-delivery implementation, the same one `task_messages` uses. The doorbell line carries the from-role name, message id, and tldr. Delivery uses a per-message delivery id prefixed `hera:` so it never collides with `task_messages` delivery ids. Delivery is best-effort and never rolls back the stored message.

Derived from: `internal/hera/service.go:67` (`Send`), `internal/hera/service.go:105` (doorbell line), `internal/hera/service.go:186` (`heraDeliveryID` "hera:" prefix), `internal/hera/service.go:31` (`Notifier` = `notify.Notifier`).

#### Scenario: Live recipient gets a doorbell

- **WHEN** the recipient role holds a live binding
- **THEN** ReliableNotify enqueues a `[hera from <role>] msg #<id> — <tldr>` doorbell to that task and the message is stamped idle_submit

#### Scenario: Delivery failure does not roll back storage

- **WHEN** the notifier fails or is nil
- **THEN** the message remains durably stored and the send still succeeds

### Requirement: Delivery-mode stamping for offline recipients

The system SHALL stamp a message's delivery mode at enqueue time: `idle_submit` when delivered to a live binding, `queued_no_binding` when the recipient has no live binding (durable, delivery deferred), and pending/skipped when no notifier is wired. A missing live binding is a soft-fail — the message is stored and no error is returned.

Derived from: `internal/hera/service.go:73` (nil-notifier skip), `internal/hera/service.go:79` (no-binding → `queued_no_binding`), `internal/hera/service.go:113` (`idle_submit` stamp).

#### Scenario: Offline recipient queues without error

- **WHEN** the recipient role has no live binding
- **THEN** the message is stamped queued_no_binding and the send returns successfully with no delivery

### Requirement: Reading or marking-read cancels pending doorbells

The system SHALL, on `hera_inbox`, return unread messages oldest-first AND cancel pending doorbell deliveries for them (reading implies the recipient saw the doorbell), then mark them read. `hera_mark_read` SHALL mark the supplied ids read for the caller's role and cancel their pending deliveries, returning the count flipped. Cancellation is per-message (delivery id `hera:<id>`), not task-scoped, mirroring `task_message_ack`. The claim-mode `hera_join` path SHALL count unread WITHOUT cancelling deliveries.

Derived from: `internal/hera/service.go:125` (`Inbox` cancels), `internal/hera/service.go:140` (`MarkRead`), `internal/hera/service.go:169` (`cancelDeliveries` per-message), `internal/mcp/hera.go:552` (`toolHeraInbox`), `internal/mcp/hera.go:382` (claim mode does not cancel).

#### Scenario: Inbox read cancels its doorbells

- **WHEN** the caller reads its inbox via hera_inbox
- **THEN** the returned messages' pending doorbell deliveries are cancelled and the messages are marked read

#### Scenario: Mark-read flips and cancels by id

- **WHEN** hera_mark_read is called with specific ids
- **THEN** only those rows addressed to the caller are flipped read and their per-message doorbells cancelled, and the flipped count is returned

#### Scenario: Claim-mode join does not cancel

- **WHEN** a task claims its existing role via hera_join (no role_name)
- **THEN** the unread count is reported but no pending deliveries are cancelled

### Requirement: Doorbell threat-model boundary

The system MAY include user-controlled strings (from-role name and tldr) in the hera doorbell line, acceptable under the cooperative single-user local threat model. The system SHALL NOT add user-controllable strings to the stricter `task_messages` nudge line.

Derived from: `internal/hera/service.go:62` (security NOTE in `Send` doc).

#### Scenario: Hera doorbell carries user strings

- **WHEN** a hera doorbell is composed
- **THEN** it may embed the sender role name and tldr, unlike the task_messages nudge line which must not
