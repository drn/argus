# Daemon Client

## MODIFIED Requirements

### Requirement: Client shutdown stops streaming and retries

Closing the client SHALL signal all stream-connection goroutines to stop retrying and SHALL close every tracked session, so no stale stream goroutine continues to dial the socket after shutdown (e.g. across a daemon restart). When the close is part of a daemon bounce (restarting the daemon process, not just tearing down the local connection), the caller SHALL first request a graceful remote shutdown via the `Shutdown()` RPC so the connected daemon process actually exits, rather than only closing the local connection and relying on the replacement daemon's own startup-time singleton-lock takeover to force the old process out. A `Shutdown()` RPC failure (e.g. the connection is already dead) SHALL NOT block the bounce — the caller proceeds to close the local connection and start a replacement as before.

#### Scenario: Close signals stream goroutines

- **WHEN** the client is closed
- **THEN** the shutdown signal SHALL become observable and in-flight stream connection loops SHALL stop and return

#### Scenario: Stream loop exits when client already closed

- **WHEN** a stream connection loop runs after the client has been closed
- **THEN** it SHALL exit immediately without attempting to dial the daemon

#### Scenario: Daemon bounce requests a real remote shutdown

- **WHEN** the TUI bounces the daemon (directly, or as part of a supervisor restart)
- **THEN** it SHALL call the connected client's `Shutdown()` RPC before closing the local connection and starting a replacement daemon

#### Scenario: Remote shutdown failure does not block the bounce

- **WHEN** the daemon bounce's `Shutdown()` RPC call fails (e.g. the connection is already shut down)
- **THEN** the bounce SHALL proceed to close the local connection and attempt to start a replacement daemon, unchanged from today
