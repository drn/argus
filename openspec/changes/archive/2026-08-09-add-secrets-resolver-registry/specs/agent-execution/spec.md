## MODIFIED Requirements

### Requirement: Per-backend credential environment mapping

The system SHALL allow a backend definition to carry a credential environment
mapping from a target environment-variable name (set in the spawned agent's
child process) to a source descriptor resolved at spawn time. When building the
agent command, after assembling the inherited environment, forced terminal
variables, and task ID, the system SHALL resolve each mapping's source through
the secrets-resolution registry (see `secrets-resolution`) and, for every
source that resolves, append `TARGET=value` to the child environment. A source
that does not resolve SHALL leave the target variable unset and SHALL be
logged as a non-sensitive warning that names only the variable, never the
resolved value. A bare string or `env://`-prefixed source SHALL resolve
against the daemon's own process environment, unchanged from prior behavior;
a `keychain://`- or `op://`-prefixed source SHALL dispatch to the
corresponding resolver in the registry. Resolution SHALL happen fresh, in
whichever process actually calls the command builder (the daemon in
in-process mode, or the session-supervisor process when supervisor mode is
enabled) — never assumed to have propagated from another process's
environment via fork inheritance. The mapping SHALL hold only the
target-to-source descriptor and SHALL NOT carry a secret value; no resolved
value SHALL be persisted or logged.

#### Scenario: Resolved source injected into child environment

- **WHEN** a backend defines a mapping `OPENAI_API_KEY -> HERA_OPENAI` and the
  secret resolver resolves `HERA_OPENAI` to a value
- **THEN** the built command's environment contains `OPENAI_API_KEY` set to that
  value

#### Scenario: Scheme-prefixed source dispatches through the registry

- **WHEN** a backend defines a mapping whose source is
  `keychain://some-service` or `op://vault/item/field`
- **THEN** the command builder resolves it through the secrets-resolution
  registry's matching resolver rather than reading it as a bare environment
  variable name

#### Scenario: Unresolved source leaves the variable unset and warns without the value

- **WHEN** a backend defines a mapping whose source does not resolve
- **THEN** the target variable is absent from the built command's environment
- **AND** a warning is logged that names the variable but contains no value

#### Scenario: Mapping carries no secret value

- **WHEN** a backend's credential mapping is stored or read back
- **THEN** it contains only target-to-source descriptors and no resolved secret
  value

#### Scenario: Resolution happens in whichever process builds the command

- **WHEN** supervisor mode is enabled and the session-supervisor process
  calls the command builder
- **THEN** the source is resolved directly inside the session-supervisor
  process, not assumed to already be present via inheritance from the
  daemon or any other process that may have forked it

#### Scenario: Resolver is pluggable

- **WHEN** an alternate secret resolver is installed in place of the default
  process-environment resolver
- **THEN** subsequent command builds resolve sources through the alternate
  resolver without any change to the command builder
