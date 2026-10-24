## ADDED Requirements

### Requirement: Daemon publishes its bound API port
When the REST API binds, the daemon SHALL atomically write the actual bound port to `~/.argus/api-port` (mode 0600, temp file plus rename), so processes that outlive a daemon restart can rediscover it.

#### Scenario: Port moved by bind retry
- **WHEN** the configured port is busy and the API binds a later port
- **THEN** `~/.argus/api-port` contains the port actually bound

#### Scenario: Restart
- **WHEN** the daemon restarts and binds a different port
- **THEN** the file is replaced with the new port without a window where it is empty
