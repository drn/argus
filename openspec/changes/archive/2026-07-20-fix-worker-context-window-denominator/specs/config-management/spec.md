## ADDED Requirements

### Requirement: Worker context window configuration

The system SHALL provide a `worker_context_window` integer field on `HeraConfig` (`config.toml` key
`hera.worker_context_window`), naming the reference context window size the Hera rail's
worker/freelance context-pressure indicator (see `hera-view`) divides a role's `ContextSize` by to
compute its percentage. This is deliberately a separate field from `coordinator_context_budget` — a
coordinator recycle-nudge policy threshold, not a context window size — since a worker runs with a
much larger real context window. When absent from config, the system SHALL default it to `1000000`.

#### Scenario: Default worker context window applies when unset

- **WHEN** a project's config.toml has no `hera.worker_context_window` key
- **THEN** the effective worker context window is `1000000`

#### Scenario: Explicit worker context window overrides the default

- **WHEN** a project's config.toml sets `hera.worker_context_window = 500000`
- **THEN** the effective worker context window for that project is `500000`
