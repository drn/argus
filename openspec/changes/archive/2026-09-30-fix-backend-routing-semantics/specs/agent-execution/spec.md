## MODIFIED Requirements

### Requirement: Backend resolution precedence

The system SHALL resolve a task's backend using the following precedence, evaluated in order, stopping at the first non-empty result: (1) the task's own explicit backend, (2) the owning project's configured backend, (3) the tiered backend-routing resolver's result when a tier list is configured, (4) the single global default backend. Steps 1 and 2 SHALL NOT consult the tiered resolver at all — an explicit task or project backend is never overridden by usage-based routing.

This precedence SHALL be evaluated exactly once, at task creation (`agent.CreateAndStart`), and the result SHALL be persisted as the task's own explicit backend (`task.Backend`) — never re-evaluated on a later session start, resume, or restart of that same task. Task creation and session start are not separate moments in practice (`CreateAndStart` creates the worktree, persists the row, and starts the session synchronously in one call), and freezing the choice at that single point is required for resume correctness: a backend's resume mechanics (session-ID capture style, CLI resume flags) are specific to that backend and not transferable to a different one chosen later. `CreateAndStart` and the general backend-resolution entry point used at session-start/display time (e.g. for an already-created task) SHALL share the identical precedence implementation, so the two can never independently drift out of sync with each other.

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

#### Scenario: The resolved backend is stamped at creation and never re-evaluated

- **WHEN** a task is created with no explicit backend, a project-level backend override or tier-list entry resolves it to a concrete name, and the task is later resumed after that project's or tier list's configuration has changed
- **THEN** the task resumes with the SAME backend it was created with, not a freshly re-resolved one

#### Scenario: Task creation and session-start resolution cannot drift apart

- **WHEN** a project-level backend override or a configured tier list would change what an empty-backend task resolves to
- **THEN** `agent.CreateAndStart`'s task-creation-time stamp reflects that same change, because both call sites share one precedence implementation
