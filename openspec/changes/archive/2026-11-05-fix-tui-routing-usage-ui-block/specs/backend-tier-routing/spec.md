## MODIFIED Requirements

### Requirement: Tier resolution in the TUI process uses the daemon's usage readings

Usage probe caches are populated only in the daemon. When the TUI is connected to the daemon, every tier resolution performed in the TUI process SHALL use Claude usage per account (including default) and the Codex reading taken from the daemon, and SHALL NOT perform a daemon call on the resolving goroutine: readings come from a cached snapshot that is refreshed in the background, refreshed at most once a minute, so a snapshot may be up to five minutes stale. When no snapshot exists yet, the snapshot is older than five minutes, or the daemon read fails, resolution SHALL fall back to the process's local caches and a refresh failure SHALL be logged. Remote (`--remote`) task creation is unaffected because it resolves in the daemon over REST.

#### Scenario: TUI routes on the daemon's readings

- **WHEN** the daemon reports default Claude at 98% and personal Claude at 70% (both threshold 90) and the TUI process's local caches are empty
- **THEN** a TUI-initiated creation resolves to the personal account

#### Scenario: Daemon read fails

- **WHEN** the background daemon usage refresh returns an error and no fresh snapshot exists
- **THEN** resolution uses local caches, and a log line records the refresh failure

#### Scenario: Slow daemon never blocks resolution

- **WHEN** the daemon usage call blocks indefinitely
- **THEN** resolution returns immediately using the last snapshot, or local caches if none
