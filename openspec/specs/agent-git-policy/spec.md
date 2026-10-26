# Agent Git policy

## Purpose

Keep agent commit signing explicit and isolated from host configuration, and ensure agent Git test fixtures never depend on personal signing credentials.

## Requirements

### Requirement: Explicit agent commit-signing policy

Argus SHALL support `git.agent_commit_signing` with values `inherit` and `unsigned`. The default SHALL be `inherit`; unsupported values MUST fail configuration validation with an actionable message. The policy SHALL apply through the common launcher to agent sessions regardless of initiating frontend or backend.

#### Scenario: Existing installations preserve signing

- **WHEN** the setting is absent or is `inherit`
- **THEN** Argus introduces no signing override and Git inherits the existing configuration

#### Scenario: Unattended signing policy enabled

- **WHEN** the setting is `unsigned` and Argus launches an agent session
- **THEN** ordinary commits in that process tree default to `commit.gpgsign=false` without invoking a configured signer

#### Scenario: Invalid policy

- **WHEN** configuration contains a value other than `inherit` or `unsigned`
- **THEN** validation rejects it and names the allowed values

### Requirement: Child-only Git configuration isolation

The signing override MUST be injected only into the spawned agent process environment, preserving existing command-scope Git entries and applying after backend environment mappings. Argus MUST NOT modify global Git configuration, SSH configuration, keychain data, or shared repository configuration to apply the policy. Explicit Git command-line options SHALL retain Git's normal precedence. The policy SHALL be documented as process-scoped rather than branch-enforced.

#### Scenario: Existing command-scope configuration

- **WHEN** an unsigned session inherits command-scope Git settings
- **THEN** unrelated entries remain effective and the configured signing policy deterministically disables default commit signing

#### Scenario: Host isolation

- **WHEN** an unsigned agent session is launched
- **THEN** a Git command launched outside that agent process tree continues to observe its prior signing configuration and all host configuration files remain unchanged

#### Scenario: Explicit signing requested

- **WHEN** a caller passes an explicit signing option to Git within an unsigned session
- **THEN** Git processes that option according to its normal precedence

### Requirement: Policy lifecycle and observability

Fresh launches, resume, and recycle SHALL apply the configured signing policy consistently. Existing running sessions SHALL retain their current environment until restarted. Logs MUST identify the selected policy and task without credential values.

#### Scenario: Session resumed after policy change

- **WHEN** a task is resumed or recycled after the configured signing policy changes
- **THEN** the replacement agent receives the new policy and a credential-free policy log entry is emitted

### Requirement: Hermetic agent Git fixtures

Affected agent tests that initialize repositories and commit fixtures MUST isolate system/global Git configuration before the fixture's first Git operation. They MUST NOT contact the real signing agent, live daemon, or real Argus data directory. Signing-policy tests SHALL use synthetic configuration and a controlled failing signer to prove the unsigned policy avoids signing independently of the developer's machine.

#### Scenario: Developer has interactive global signing

- **WHEN** fixture tests run with synthetic global signing configured to fail
- **THEN** fixture creation succeeds through explicit test isolation and policy-specific tests independently prove inherit versus unsigned behavior
