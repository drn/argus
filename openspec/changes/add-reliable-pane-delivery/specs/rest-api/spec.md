# REST API

## MODIFIED Requirements

### Requirement: Reliable pane-delivery endpoint

The system SHALL expose a `POST /api/tasks/{id}/notify` endpoint that registers a text delivery for the named task via the reliable notify service. The request body SHALL require `text` (non-empty string), `submit` (must be `true`), and `delivery_id` (non-empty identifier, max 128 bytes, alphanumeric and `-_` only). An optional `deadline_ms` field controls the delivery deadline in milliseconds (default 300,000; minimum 1,000; maximum 3,600,000). The endpoint SHALL be callable by any authenticated token (master, device, or plugin-scoped). On success it SHALL return the delivery_id and its current state (`"submitted"` or `"pending"`). Re-posting a previously submitted delivery_id SHALL be idempotent (200 with state `"submitted"`).

#### Scenario: Delivery registered and pending

- **WHEN** a client posts a valid notify request for a task whose session is not yet idle
- **THEN** the endpoint returns 202 with `{"delivery_id": "...", "state": "pending"}`

#### Scenario: Delivery submitted inline (session already idle and unfocused)

- **WHEN** a client posts a valid notify request for a task that is idle and unfocused at request time
- **THEN** the endpoint returns 202 with `{"delivery_id": "...", "state": "submitted"}`

#### Scenario: Re-post of submitted delivery_id is idempotent

- **WHEN** a client posts a notify request with a delivery_id that was already submitted
- **THEN** the endpoint returns 200 with `{"delivery_id": "...", "state": "submitted"}` without re-injecting

#### Scenario: Missing or invalid fields rejected

- **WHEN** a client posts a notify request with missing text, missing delivery_id, or `submit` not `true`
- **THEN** the endpoint returns 400 with an error identifying the missing or invalid field

#### Scenario: Delivery_id format rejected

- **WHEN** a client posts a notify request with a delivery_id containing characters outside alphanumeric and `-_`
- **THEN** the endpoint returns 400

#### Scenario: Task not found

- **WHEN** a client posts a notify request for a task ID that does not exist
- **THEN** the endpoint returns 404

#### Scenario: Any authenticated token accepted

- **WHEN** a request authenticated with a device token or a plugin-scoped token posts to this endpoint
- **THEN** the request is accepted (not rejected as master-only)

### Requirement: Cancel pane-delivery endpoint

The system SHALL expose a `DELETE /api/tasks/{id}/notify/{delivery_id}` endpoint that cancels a pending delivery registered via the notify endpoint. If the delivery is pending, it SHALL be removed and the response SHALL indicate `cancelled: true`. If the delivery is not found (already submitted or never registered), the response SHALL indicate `cancelled: false` and return 200 (idempotent).

#### Scenario: Pending delivery cancelled

- **WHEN** a client calls DELETE for a delivery_id that is currently pending
- **THEN** the response is `{"delivery_id": "...", "cancelled": true}` and no further PTY write occurs for that delivery

#### Scenario: Already-submitted delivery cancel is a no-op

- **WHEN** a client calls DELETE for a delivery_id that was already submitted
- **THEN** the response is `{"delivery_id": "...", "cancelled": false}` with 200 (no error)

#### Scenario: Unknown delivery_id is a no-op

- **WHEN** a client calls DELETE for a delivery_id that was never registered
- **THEN** the response is `{"delivery_id": "...", "cancelled": false}` with 200 (no error)

#### Scenario: Any authenticated token accepted

- **WHEN** a request authenticated with a device token or plugin-scoped token calls DELETE on this endpoint
- **THEN** the request is accepted (not rejected as master-only)
