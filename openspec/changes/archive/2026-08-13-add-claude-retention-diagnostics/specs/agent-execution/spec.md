## ADDED Requirements

### Requirement: Query Claude Code's effective cleanup-period retention

`internal/agent` SHALL expose a query that reads `~/.claude/settings.json` and returns the effective `cleanupPeriodDays` value (nil when the key is absent, meaning Claude Code's own 30-day default applies) alongside any file-read or parse error, so both `argus doctor` and the Settings TUI classify the same underlying state via one implementation.

#### Scenario: Configured value returned

- **WHEN** `~/.claude/settings.json` sets `cleanupPeriodDays` to a value
- **THEN** the query returns that value with no error

#### Scenario: Absent key returns nil

- **WHEN** `~/.claude/settings.json` exists but has no `cleanupPeriodDays` key
- **THEN** the query returns a nil value with no error

#### Scenario: Unreadable or malformed file returns an error

- **WHEN** `~/.claude/settings.json` cannot be read or fails to parse as JSON
- **THEN** the query returns a non-nil error

### Requirement: Retention-swept resume failure is classified distinctly

`internal/agent` SHALL expose a pure classifier that recognizes Claude Code's exact resume-failure signature — output containing `No conversation found with session ID:` — as distinct from a generic process crash, operating on the same last-output bytes already captured by the "Process exit notification and last output" requirement.

#### Scenario: Signature match is classified as a retention failure

- **WHEN** a resumed session's last output contains `No conversation found with session ID:`
- **THEN** the classifier reports it as a retention-swept resume failure

#### Scenario: Unrelated crash output does not match

- **WHEN** a session's last output is a generic error or stack trace not containing that signature
- **THEN** the classifier does not report a retention-swept resume failure
