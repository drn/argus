## ADDED Requirements

### Requirement: Sandbox proposals are readable by any token and decidable only by the master token

The API SHALL serve `GET /api/sandbox/proposals` (optional `status` filter) and `GET /api/sandbox/proposals/{id}` to any authenticated token. `POST /api/sandbox/proposals/{id}/approve` (body: `scope` project|global, optional edited `paths`) and `POST /api/sandbox/proposals/{id}/reject` SHALL require the master token and return 403 otherwise. Deciding an already-decided proposal SHALL return 409.

#### Scenario: Device token cannot approve

- **WHEN** a device-token request POSTs approve
- **THEN** the response is 403 and the proposal remains pending

#### Scenario: argus_api_get can read status

- **WHEN** an agent reads `/api/sandbox/proposals` through `argus_api_get`
- **THEN** it receives the proposal list including each status

#### Scenario: Double approve

- **WHEN** the master token approves an already-approved proposal
- **THEN** the response is 409 and config is unchanged
