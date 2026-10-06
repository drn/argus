## ADDED Requirements

### Requirement: Settings update validates global extra-write paths

`PUT /api/settings` SHALL respond 400 and persist nothing when any `sandbox.extra_write` entry is `/`, is neither absolute nor `~/`-prefixed, contains a comma, or contains a control character.

#### Scenario: Rejecting a root grant

- **WHEN** a master-token client sends `{"sandbox":{"extra_write":["/"]}}`
- **THEN** the response is 400 and the stored list is unchanged
