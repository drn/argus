# Agent Execution

## ADDED Requirements

### Requirement: Archetype carried at task creation

The system SHALL accept an optional `archetype` at the single fresh-task creation chokepoint
(`agent.CreateAndStart`) and persist it on the created task, so that any spawn path — interactive
new-task creation, hera worker spawn, and freelance creation — can set a task's archetype uniformly.
When no archetype is supplied, the task SHALL carry an empty archetype and profile-based resolution
SHALL NOT apply.

#### Scenario: Archetype persisted on the task

- **WHEN** a task is created through `CreateAndStart` with `archetype = "security_review"`
- **THEN** the created task carries `security_review` as its archetype

#### Scenario: Absent archetype leaves the task unmarked

- **WHEN** a task is created with no archetype
- **THEN** the task's archetype is empty and no profile is consulted for its model resolution

### Requirement: Profile environment exported alongside the task ID

When a profile resolves for a spawned agent, the system SHALL export `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`,
and `ARGUS_MODEL` into the agent command's environment, in addition to the existing task-ID export, so
in-repo skills can read the active profile and archetype. When no profile resolves, these variables
SHALL be omitted.

#### Scenario: Profile env present when a profile resolves

- **WHEN** a command is built for a task whose archetype resolves a valid bound profile
- **THEN** the command environment exports `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, and `ARGUS_MODEL`

#### Scenario: Profile env absent without a profile

- **WHEN** a command is built for a task that carries no archetype or whose profile does not resolve
- **THEN** the command environment contains none of the profile variables
