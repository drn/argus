## ADDED Requirements

### Requirement: Daemon publishes its bound API port
When the REST API binds, the daemon SHALL atomically write the actual bound port to `~/.argus/api-port` (mode 0600, temp file plus rename), so processes that outlive a daemon restart can rediscover it.

#### Scenario: Port moved by bind retry
- **WHEN** the configured port is busy and the API binds a later port
- **THEN** `~/.argus/api-port` contains the port actually bound

#### Scenario: Restart
- **WHEN** the daemon restarts and binds a different port
- **THEN** the file is replaced with the new port without a window where it is empty

### Requirement: Daemon withdraws its port file on clean shutdown
On clean shutdown the daemon SHALL remove `~/.argus/api-port` only while it still names that daemon's own port, so a newer daemon's file is never removed.

#### Scenario: Clean shutdown
- **WHEN** the daemon shuts down and the port file names its port
- **THEN** the file is removed

#### Scenario: Takeover
- **WHEN** a newer daemon has already rewritten the port file with a different port
- **THEN** the exiting daemon leaves it in place
