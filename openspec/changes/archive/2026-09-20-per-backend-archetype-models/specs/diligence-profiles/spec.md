## MODIFIED Requirements

### Requirement: Profile structure and archetypes

A profile SHALL describe, per archetype, optional backend-keyed model choices, `effort`, and `window`, plus a `[rigor]` table and an opaque `[panel]` table. A model choice SHALL be declared under the archetype's `models` table, keyed by backend name. The system SHALL recognize exactly thirteen canonical archetypes.

#### Scenario: Backend-keyed archetype models parse

- **WHEN** a profile declares `[archetype.code_slice.models]` with `claude = "sonnet"` and `codex = "gpt-5-codex"`
- **THEN** the loaded profile exposes both backend-specific choices for `code_slice`

### Requirement: Profile validation

The system SHALL validate every backend-keyed archetype model against the selectable models for that named backend. A configured backend's `models` list SHALL take precedence; otherwise, its known CLI aliases SHALL be used. The system SHALL report an unknown backend key or a model unsupported by its named backend as a conformance error, alongside all other profile conformance errors.

#### Scenario: Model rejected for its named backend

- **WHEN** a profile declares `codex = "opus"` for an archetype
- **THEN** validation reports that `opus` is unsupported for the `codex` backend

#### Scenario: Model accepted for its named backend

- **WHEN** a profile declares `codex = "gpt-5-codex"` for an archetype
- **THEN** validation accepts that entry when Codex exposes that model

### Requirement: Profile-aware model resolution

The system SHALL resolve a task's effective model with the precedence: a valid per-task model override; otherwise, the resolved profile's model for the task's selected backend and archetype; otherwise the selected backend's configured default; otherwise no injected model flag. An invalid explicit override or missing/invalid backend-specific profile entry SHALL fall through to the selected backend's configured default rather than reaching the backend CLI.

#### Scenario: Backend-specific profile model applied

- **WHEN** a Codex task has no valid override, carries `code_slice`, and its valid profile declares `codex = "gpt-5-codex"`
- **THEN** the effective model is `gpt-5-codex`

#### Scenario: Unsupported explicit override falls through

- **WHEN** a Codex task sets `Model = "opus"`
- **THEN** the system does not inject `opus` and instead uses the Codex backend default or no model flag
