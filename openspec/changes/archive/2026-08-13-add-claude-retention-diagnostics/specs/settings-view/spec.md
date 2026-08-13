## ADDED Requirements

### Requirement: Claude session retention status row in System category

The System category SHALL include a row surfacing the same OK / LOW / UNKNOWN tri-state for Claude Code's `cleanupPeriodDays` retention window (see `binary-coherence`) that `argus doctor` reports, computed via the same underlying query. The row's detail pane SHALL show the current effective value (or that it is unset and defaulting to 30) and, when LOW, the JSON snippet to raise it in `~/.claude/settings.json`. Not shown in `--remote` mode, since the setting lives on the local machine's `~/.claude/settings.json`, not the remote daemon's.

#### Scenario: Row reflects OK status

- **WHEN** `cleanupPeriodDays` is explicitly set above 30
- **THEN** the System category's retention row shows OK

#### Scenario: Row reflects LOW status

- **WHEN** `cleanupPeriodDays` is unset or set to 30 or below
- **THEN** the System category's retention row shows LOW and its detail pane includes the fix snippet

#### Scenario: Row reflects UNKNOWN status

- **WHEN** `~/.claude/settings.json` cannot be read or parsed
- **THEN** the System category's retention row shows UNKNOWN

#### Scenario: Row hidden in remote mode

- **WHEN** the Settings view is connected to a remote daemon (`--remote`)
- **THEN** the Claude session retention row is not shown
