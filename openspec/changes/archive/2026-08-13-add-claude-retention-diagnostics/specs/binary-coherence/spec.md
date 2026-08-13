## ADDED Requirements

### Requirement: Claude session retention diagnostic

`argus doctor` SHALL independently report Claude Code's effective session-retention window (`cleanupPeriodDays` in `~/.claude/settings.json`) as one of three states, printed after the secrets-bootstrap section: **OK** (explicitly set above the 30-day default), **LOW** (unset, which defaults to 30, or explicitly set to 30 or below — prints the fix snippet), or **UNKNOWN** (`~/.claude/settings.json` could not be read or parsed, reported distinctly rather than assumed low). This check is purely advisory and SHALL NOT affect `argus doctor`'s exit code, which stays governed solely by the binary-coherence verdict.

#### Scenario: Retention explicitly raised is OK

- **WHEN** `~/.claude/settings.json` sets `cleanupPeriodDays` above 30
- **THEN** `argus doctor` reports OK

#### Scenario: Unset retention is LOW

- **WHEN** `~/.claude/settings.json` has no `cleanupPeriodDays` key
- **THEN** `argus doctor` reports LOW and prints the fix snippet

#### Scenario: Retention at or below the default is LOW

- **WHEN** `~/.claude/settings.json` sets `cleanupPeriodDays` to 30 or a lower value
- **THEN** `argus doctor` reports LOW and prints the fix snippet

#### Scenario: Unreadable settings file is UNKNOWN

- **WHEN** `~/.claude/settings.json` cannot be read or fails to parse
- **THEN** `argus doctor` reports UNKNOWN rather than LOW

#### Scenario: Check does not change the exit-code contract

- **WHEN** the Claude session retention diagnostic reports LOW or UNKNOWN while the binary-coherence verdict is Healthy
- **THEN** `argus doctor` still exits 0
