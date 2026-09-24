## MODIFIED Requirements

### Requirement: Backend resolution precedence

The system SHALL resolve a task's backend using the following precedence, evaluated in order, stopping at the first non-empty result: (1) the task's own explicit backend, (2) the owning project's configured backend, (3) the tiered backend-routing resolver's result when a tier list is configured, (4) the single global default backend. Steps 1 and 2 SHALL NOT consult the tiered resolver at all — an explicit task or project backend is never overridden by usage-based routing.

#### Scenario: Explicit task backend always wins

- **WHEN** a task carries its own explicit backend
- **THEN** that backend is used and neither the project backend, the tiered resolver, nor the global default are consulted

#### Scenario: Explicit project backend wins over tiered routing

- **WHEN** a task has no explicit backend but its project has a configured backend
- **THEN** the project's backend is used and the tiered resolver is not consulted

#### Scenario: Tiered routing fills in when neither explicit backend is set

- **WHEN** neither the task nor its project has an explicit backend, and a tier list is configured
- **THEN** the tiered resolver's result is used

#### Scenario: Global default is used when no tier list is configured

- **WHEN** neither the task nor its project has an explicit backend, and no tier list is configured (or the tiered resolver returns empty)
- **THEN** the single global default backend is used, unchanged from prior behavior
