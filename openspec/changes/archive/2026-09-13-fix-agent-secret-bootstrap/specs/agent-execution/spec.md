## ADDED Requirements

### Requirement: Forced op-bootstrap credential environment export

The system SHALL force-export the resolved `[secrets.op]` bootstrap credential into every spawned agent session's environment, alongside the existing forced terminal-capability and cache-redirect environment variables. It SHALL resolve `[secrets.op].bootstrap_source` through the same secrets-resolution registry `Resolve` function the `op://` scheme's own self-referential bootstrap uses, and, when it resolves, SHALL set `[secrets.op].bootstrap_target=<resolved value>` in the spawned session's environment. When `[secrets.op]` is unconfigured (`bootstrap_source` is empty) or the bootstrap source fails to resolve, the system SHALL leave the spawned session's environment unaffected — no partial or empty variable is ever set. The resolved value SHALL NOT be logged, printed, or persisted anywhere.

This closes the gap where a spawned session's non-interactive shell never sources the user's own shell profile, so a secret-needing tool invoked from inside the session (e.g. `op read`) previously had no bootstrap credential available and fell back to interactive authentication.

#### Scenario: Bootstrap credential force-exported when configured and resolving

- **WHEN** a session is spawned and `[secrets.op].bootstrap_source` is configured and resolves successfully
- **THEN** the spawned session's environment includes `[secrets.op].bootstrap_target` set to the resolved value

#### Scenario: No-op when op bootstrap is unconfigured

- **WHEN** a session is spawned and `[secrets.op].bootstrap_source` is empty
- **THEN** the spawned session's environment is unaffected — no bootstrap-target variable is set, and no resolver subprocess is invoked

#### Scenario: No-op when op bootstrap fails to resolve

- **WHEN** a session is spawned and `[secrets.op].bootstrap_source` is configured but fails to resolve
- **THEN** the spawned session's environment is unaffected — no bootstrap-target variable is set
