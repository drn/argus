## Why

**A TUI-triggered daemon/supervisor bounce does not reliably bring the daemon
back up.** `App.restartDaemon` (`internal/tui/app.go`) is documented as "try
graceful shutdown via RPC" but only calls `Client.Close()`, which tears down
the TUI's own local connection (`internal/daemon/client/client.go`) — it never
calls the `Shutdown()` RPC that actually asks the remote daemon process to
exit. Recovery is left entirely to the newly-forked daemon's own
`killExistingDaemon` PID-file kill at startup, racing `autoStartFork`'s fixed
3-second readiness poll. Under real load (many concurrent hera sessions
needing reattachment) this has been observed, twice, to time out: the old
daemon process is never told to leave, the poll gives up, and the TUI reports
a failed restart with no automatic retry — leaving both the daemon and (via
`restartSupervisor`, which chains into `restartDaemon`) the session-supervisor
down until someone manually forces an OS-level restart outside the TUI
entirely (e.g. `launchctl bootout`/`bootstrap`, which delivers a real SIGTERM
the buggy path never sent).

## What Changes

- **`App.restartDaemon` SHALL request a real remote shutdown before forking a
  replacement.** It calls the daemon client's `Shutdown()` RPC (falling back
  to the existing local `Close()` when the RPC itself fails, e.g. the
  connection is already dead) instead of only `Close()`, so the old daemon
  process is actually asked to exit rather than relying solely on the new
  process's PID-file kill to force it out.
- No change to `autoStartFork`'s readiness-poll timeout or to
  `restartSupervisor`'s call sequence — this change is scoped to making the
  existing "graceful shutdown via RPC" step do what its own comment already
  claims.

## Capabilities

### Modified Capabilities

- `daemon-client`: a daemon bounce initiated by the client SHALL request a
  graceful remote shutdown via RPC before the client's local connection is
  torn down, rather than only closing the local connection.

## Impact

- **Modified code:**
  - `internal/tui/app.go` — `restartDaemon` calls `Shutdown()` before/instead
    of a bare `Close()`.
- **No new key, no new dependency, no schema change, no wire-protocol
  change.** `Shutdown()` and `Close()` both already exist on `*dclient.Client`;
  this only changes which one `restartDaemon` calls and in what order.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make /
  Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
