## ADDED Requirements

### Requirement: Coordinator context budget configuration

The system SHALL provide a `coordinator_context_budget` integer field on `HeraConfig` (`config.toml` key `hera.coordinator_context_budget`), naming the token count at or above which the context-budget Stop hook (see `coordinator-context-management`) begins nudging a coordinator to recycle. When absent from config, the system SHALL default it to `200000`.

#### Scenario: Default budget applies when unset

- **WHEN** a project's config.toml has no `hera.coordinator_context_budget` key
- **THEN** the effective budget is `200000`

#### Scenario: Explicit budget overrides the default

- **WHEN** a project's config.toml sets `hera.coordinator_context_budget = 350000`
- **THEN** the effective budget for that project is `350000`
