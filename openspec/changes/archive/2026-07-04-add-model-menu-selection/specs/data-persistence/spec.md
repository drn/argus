## MODIFIED Requirements

### Requirement: Archetype and profile-binding columns

The schema SHALL persist a task's archetype, a task's per-spawn model/effort overrides, a task's per-spawn profile override, a project's bound profile name, and a hera role's planned archetype/effort: a `tasks.archetype` column (the authoritative model-resolution key), a `tasks.effort` column (the per-spawn effort override, resolved the same way as the existing `tasks.model` override), a `tasks.profile` column (the per-spawn profile override — non-empty means the operator overrode the project's profile for this one spawn), a `projects.profile` column (the project→profile-name binding), a `hera_roles.archetype` column (a planned node's intended archetype, mirrored for the live role), and a `hera_roles.effort` column (a planned node's intended effort override, mirrored for the live role the same way). Each SHALL default to empty for existing rows and require no data migration. The database SHALL NOT store profile bodies.

#### Scenario: Task archetype round-trips

- **WHEN** a task with archetype `ci_loop` is written and re-read
- **THEN** the read-back task carries `ci_loop`

#### Scenario: Task per-spawn profile override round-trips

- **WHEN** a task is created with a per-spawn profile override `custom`
- **THEN** the read-back task carries `custom` as its `profile` field

#### Scenario: Task per-spawn effort override round-trips

- **WHEN** a task is created with a per-spawn effort override `xhigh`
- **THEN** the read-back task carries `xhigh` as its `effort` field

#### Scenario: Project profile name round-trips

- **WHEN** a project with profile `lean` is written and re-read
- **THEN** the read-back project carries `lean`

#### Scenario: Existing rows default to empty

- **WHEN** the new columns are added to a database with existing rows
- **THEN** those rows read empty archetype / empty effort / empty profile without error

#### Scenario: Planned role archetype propagates at materialization

- **WHEN** a planned hera role with archetype `review` is materialized into a task
- **THEN** the materialized task carries `review` as its archetype

#### Scenario: Planned role effort propagates at materialization

- **WHEN** a planned hera role with effort override `high` is materialized into a task
- **THEN** the materialized task carries `high` as its effort override
