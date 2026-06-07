# Events Bus

## Purpose

The events bus is the daemon-wide notification channel that lets external consumers (plugins, the web/PWA client) observe task, session, and messaging activity without polling. Emission sites across the daemon fire typed events through a single global entry point; the daemon persists each event to a bounded ring and fans it out to live Server-Sent-Events (SSE) subscribers. Consumers reconnect with a monotonic cursor to replay missed history, and are told to resnapshot when their cursor has aged out of the ring.

## Requirements

### Requirement: Global emission entry point

The system SHALL expose a single `Emit(type, taskID, payload)` entry point that constructs an event and forwards it to the installed sink. When no sink is installed, emission SHALL be a silent no-op that never panics, so every call site can fire unconditionally.

#### Scenario: Emit with no sink installed

- **WHEN** `Emit` is called while no sink is registered
- **THEN** the call returns without error, delivers nothing, and does not panic

#### Scenario: Emit delivers a stamped event to the installed sink

- **WHEN** `Emit` is called with a type, task ID, and payload while a sink is installed
- **THEN** the sink receives exactly one event whose type and task ID match the inputs and whose timestamp is non-zero

### Requirement: Payload encoding is best-effort

The system SHALL JSON-encode the supplied payload onto the event. A nil payload SHALL produce an event with no payload bytes, and a payload that fails to marshal SHALL still produce an event but with no payload bytes — the encoding failure SHALL NOT propagate to the caller and SHALL NOT prevent the event from firing.

#### Scenario: Nil payload omits payload bytes

- **WHEN** `Emit` is called with a nil payload
- **THEN** the delivered event has no payload bytes

#### Scenario: Unmarshalable payload still fires the event

- **WHEN** `Emit` is called with a payload that cannot be JSON-encoded
- **THEN** exactly one event is still delivered, with no payload bytes, and no error is surfaced to the caller

#### Scenario: Marshalable payload round-trips

- **WHEN** `Emit` is called with a JSON-encodable payload
- **THEN** the delivered event carries the payload as JSON that decodes back to the original values

### Requirement: Sink installation is global and reversible

The system SHALL maintain exactly one globally installed sink. Installing a sink SHALL return the previously installed sink so callers can save and restore it, and installing nil SHALL clear the sink so subsequent emissions become no-ops.

#### Scenario: Installing nil clears the sink

- **WHEN** a sink is installed and then nil is installed
- **THEN** subsequent emissions deliver nothing to the previously installed sink

#### Scenario: Install returns the prior sink

- **WHEN** a sink B is installed while sink A was already installed
- **THEN** the install call returns sink A

### Requirement: Persist-then-broadcast on the daemon sink

The daemon sink SHALL persist each emitted event to the events ring before broadcasting it to live subscribers, so that broadcast order matches commit order and broadcast events carry their assigned ID. If persistence fails, the sink SHALL log the failure and SHALL NOT broadcast and SHALL NOT panic.

#### Scenario: Successful emit persists and broadcasts with an ID

- **WHEN** the daemon sink emits an event and persistence succeeds
- **THEN** the event is stored in the ring and the same event — bearing a positive assigned ID — is delivered to live subscribers

#### Scenario: Persistence failure suppresses broadcast

- **WHEN** the daemon sink emits an event but the underlying store is unavailable
- **THEN** no event is broadcast to subscribers and the daemon does not panic

### Requirement: SSE stream endpoint

The system SHALL serve `GET /api/events/stream` as an SSE channel that responds with `Content-Type: text/event-stream`, encodes each event as an SSE block carrying the event type in the `event:` field and the JSON event in the `data:` field, and keeps the connection open. When the underlying writer does not support streaming, the endpoint SHALL respond with an error rather than attempting to stream.

#### Scenario: Stream returns SSE content type and typed events

- **WHEN** a client connects to `/api/events/stream`
- **THEN** the response status is 200 with `Content-Type: text/event-stream` and each delivered block names the event type in its `event:` field and a decodable JSON event in its `data:` field

### Requirement: Cursor replay with exclusive `since`

The stream SHALL accept a `since` query cursor and replay all retained events strictly newer than that cursor before delivering live events. An absent, empty, negative, or unparseable `since` SHALL be treated as zero (replay all retained events).

#### Scenario: Replays history newer than the cursor

- **WHEN** a client connects with `since` set to an existing event's ID
- **THEN** the stream replays only events with an ID strictly greater than that cursor

#### Scenario: Replays all events from zero

- **WHEN** a client connects with `since=0`
- **THEN** the stream replays every retained event in ascending ID order, each bearing a positive ID

#### Scenario: Invalid cursor falls back to zero

- **WHEN** a client connects with a non-numeric `since` value
- **THEN** the stream behaves as if `since=0` and replays all retained events

### Requirement: Resync on aged-out cursor

When a client connects with a positive `since` cursor that is older than the oldest event still retained in the ring, the stream SHALL emit a synthetic resync event as the very first event before any replay, signalling that history has rotated out and the client should resnapshot daemon state. The resync event SHALL NOT be persisted in the ring.

#### Scenario: Cursor older than the ring triggers resync first

- **WHEN** a client connects with a `since` cursor pointing at an event that has been evicted from the ring
- **THEN** the first event delivered on the stream is a resync event

### Requirement: Lossless, dupe-free live delivery after replay

The stream SHALL deliver live events after replay without duplicating or reordering events relative to the replayed history. An event whose ID falls within the replayed range SHALL NOT be delivered again as a live event.

#### Scenario: Live event delivered after replay

- **WHEN** a client connected with a cursor at the latest event, and a new event is emitted after the subscription attaches
- **THEN** the new live event is delivered on the stream

#### Scenario: No duplicate across the replay/live boundary

- **WHEN** an event is committed during the window between subscription and replay-boundary snapshot
- **THEN** that event is delivered exactly once and not duplicated across the replay and live phases

### Requirement: Bounded ring with slow-subscriber drop

Each subscriber SHALL have a bounded backlog buffer, and the broadcaster SHALL drop events for any subscriber whose buffer is full rather than blocking the daemon. A subscriber that has dropped events recovers by reconnecting and replaying from its cursor (or via resync if the cursor has aged out).

#### Scenario: Slow subscriber does not stall the daemon

- **WHEN** a subscriber's backlog buffer is full and a new event is broadcast
- **THEN** the event is dropped for that subscriber and the broadcast to other subscribers and the daemon proceeds without blocking

### Requirement: Subscriber lifecycle cleanup on disconnect

The stream SHALL register each connection as a subscriber on connect and SHALL deregister it when the client disconnects or the request context is cancelled, so that subscriber resources do not leak after a client goes away.

#### Scenario: Disconnect deregisters the subscriber

- **WHEN** a connected SSE client cancels its request and closes the connection
- **THEN** the subscriber is removed and the active subscriber count returns to zero
