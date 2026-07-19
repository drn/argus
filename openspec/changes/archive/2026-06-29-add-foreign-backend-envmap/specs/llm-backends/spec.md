# llm-backends (delta)

## ADDED Requirements

### Requirement: Backend credential environment mapping definition

A backend definition SHALL be able to carry an optional credential environment
mapping: a set of entries mapping a target environment-variable name to a
source descriptor. The mapping SHALL hold only descriptors and SHALL NOT store
a secret value. The mapping SHALL be persisted with the backend definition and
read back with it. The default `codex` backend SHALL be seeded with the mapping
`OPENAI_API_KEY -> HERA_OPENAI` so a Codex (OpenAI) agent can receive its key
under the expected variable name. No `gemini` backend SHALL be added by this
change.

#### Scenario: Codex default carries the OpenAI mapping

- **WHEN** the default backend set is seeded into a fresh database
- **THEN** the `codex` backend carries a credential mapping
  `OPENAI_API_KEY -> HERA_OPENAI` and no secret value is stored

#### Scenario: Existing database picks up the codex mapping

- **WHEN** a database that predates this change is opened and the existing
  `codex` row has no credential mapping
- **THEN** the `codex` row is updated to carry `OPENAI_API_KEY -> HERA_OPENAI`
  without overwriting a mapping a user has already customized

#### Scenario: Mapping round-trips without a value

- **WHEN** a backend with a credential mapping is written and read back
- **THEN** the mapping is preserved as target-to-source descriptors with no
  secret value
