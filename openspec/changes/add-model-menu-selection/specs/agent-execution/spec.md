## MODIFIED Requirements

### Requirement: Profile environment exported alongside the task ID

When a profile resolves for a spawned agent, the system SHALL export `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, `ARGUS_MODEL`, and `ARGUS_EFFORT` into the agent command's environment, in addition to the existing task-ID export, so in-repo skills can read the active profile, archetype, and resolved tier. These four are exported together or not at all. When no profile resolves, all four SHALL be omitted.

#### Scenario: Profile env present when a profile resolves

- **WHEN** a command is built for a task whose archetype resolves a valid bound profile
- **THEN** the command environment exports `ARGUS_PROFILE`, `ARGUS_ARCHETYPE`, `ARGUS_MODEL`, and
  `ARGUS_EFFORT`

#### Scenario: Profile env absent without a profile

- **WHEN** a command is built for a task that carries no archetype or whose profile does not resolve
- **THEN** the command environment contains none of the profile variables
