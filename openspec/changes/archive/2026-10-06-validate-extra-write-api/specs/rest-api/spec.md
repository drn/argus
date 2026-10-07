## ADDED Requirements

### Requirement: Settings update validates global extra-write paths

`PUT /api/settings` SHALL respond 400 and persist nothing when any `sandbox.extra_write` entry is `/`, is neither absolute nor `~/`-prefixed, contains a comma, or contains a control character, or is not a clean path (`.`, `..` or `//` segments, or `~/` alone). The same rule SHALL apply to a project's `sandbox.extra_write` on `POST/PUT /api/projects`.

#### Scenario: Rejecting a root grant

- **WHEN** a master-token client sends `{"sandbox":{"extra_write":["/"]}}`
- **THEN** the response is 400 and the stored list is unchanged
