## ADDED Requirements

### Requirement: Bridge event ingest endpoint
The REST API SHALL expose `POST /api/tasks/{id}/bridge/events`, authenticated like every `/api/*` route, accepting `{"events":[...]}` and recording them as the task's bridge state. It SHALL return 404 for an unknown task and 400 for a malformed body, and SHALL NOT alter any session or task state.

#### Scenario: Valid batch
- **WHEN** an authenticated client posts a valid batch for an existing task
- **THEN** the response is 200 with the count of accepted events and the task's bridge state reflects them

#### Scenario: Unknown task
- **WHEN** the task id does not exist
- **THEN** the response is 404

#### Scenario: Unauthenticated
- **WHEN** the request carries no valid token
- **THEN** the response is 401

### Requirement: Bridge state and shadow statistics endpoints
The REST API SHALL expose `GET /api/tasks/{id}/bridge` returning the task's latest bridge state (404 when none) and `GET /api/bridge/shadow` returning, per signal (`idle`, `needs_input`), the counters `agree`, `disagree`, `settling` and `no_bridge`, the settle window, and the currently open disagreements.

#### Scenario: Stats
- **WHEN** a client requests `GET /api/bridge/shadow`
- **THEN** the response lists both signals with their four counters
