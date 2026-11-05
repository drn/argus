## ADDED Requirements

### Requirement: Tier resolution in the TUI process uses the daemon's usage readings

Usage probe caches are populated only in the daemon. When the TUI is connected to the daemon, every tier resolution performed in the TUI process SHALL read Claude usage per account (including default) and the Codex reading from the daemon, with one read per resolution. Without a daemon connection, or when the daemon read fails, resolution SHALL fall back to the process's local caches and the failure SHALL be logged. Remote (`--remote`) task creation is unaffected because it resolves in the daemon over REST.

#### Scenario: TUI routes on the daemon's readings

- **WHEN** the daemon reports default Claude at 98% and personal Claude at 70% (both threshold 90) and the TUI process's local caches are empty
- **THEN** a TUI-initiated creation resolves to the personal account

#### Scenario: Daemon read fails

- **WHEN** the daemon usage read returns an error
- **THEN** resolution uses local caches, and a log line records the fallback

### Requirement: Headroom picks the least-used tier when every known tier is over threshold

Under the `headroom` strategy, a tier with a known reading and headroom SHALL rank above a tier with no reading, which SHALL rank above a tier with a known reading at or over its threshold. When no tier has headroom and none is unread, resolution SHALL select the over-threshold tier with the lowest usage percent (ties by list order) and log "all tiers over threshold; least-used", rather than returning empty. Explicit backend and account pins still constrain the candidate set. The `ordered` strategy is unchanged.

#### Scenario: Unknown beats over-threshold

- **WHEN** one tier is known and over threshold and another has no reading
- **THEN** the unread tier is selected

#### Scenario: Known with headroom beats unknown

- **WHEN** one tier is known with headroom and another has no reading
- **THEN** the known tier is selected

#### Scenario: All tiers over threshold

- **WHEN** default Claude is at 98% and personal Claude at 93%, both threshold 90
- **THEN** the personal account is selected, not the default
