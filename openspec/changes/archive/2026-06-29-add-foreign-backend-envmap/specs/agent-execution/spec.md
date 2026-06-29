# agent-execution (delta)

## ADDED Requirements

### Requirement: Per-backend credential environment mapping

The system SHALL allow a backend definition to carry a credential environment
mapping from a target environment-variable name (set in the spawned agent's
child process) to a source descriptor resolved at spawn time. When building the
agent command, after assembling the inherited environment, forced terminal
variables, and task ID, the system SHALL resolve each mapping's source through
a pluggable secret-resolver seam and, for every source that resolves, append
`TARGET=value` to the child environment. A source that does not resolve SHALL
leave the target variable unset and SHALL be logged as a non-sensitive warning
that names only the variable, never the resolved value. The secret-resolver
seam SHALL default to reading the daemon's own process environment by the
source name, and SHALL be replaceable without modifying the command builder so
a future credential resolver (e.g. an `op`/1Password resolver) can be wired in.
The mapping SHALL hold only the target-to-source descriptor and SHALL NOT carry
a secret value; no resolved value SHALL be persisted or logged.

#### Scenario: Resolved source injected into child environment

- **WHEN** a backend defines a mapping `OPENAI_API_KEY -> HERA_OPENAI` and the
  secret resolver resolves `HERA_OPENAI` to a value
- **THEN** the built command's environment contains `OPENAI_API_KEY` set to that
  value

#### Scenario: Unresolved source leaves the variable unset and warns without the value

- **WHEN** a backend defines a mapping whose source does not resolve
- **THEN** the target variable is absent from the built command's environment
- **AND** a warning is logged that names the variable but contains no value

#### Scenario: Mapping carries no secret value

- **WHEN** a backend's credential mapping is stored or read back
- **THEN** it contains only target-to-source descriptors and no resolved secret
  value

#### Scenario: Resolver is pluggable

- **WHEN** an alternate secret resolver is installed in place of the default
  process-environment resolver
- **THEN** subsequent command builds resolve sources through the alternate
  resolver without any change to the command builder
