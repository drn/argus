## 1. Fix restartDaemon's graceful shutdown

- [x] 1.1 `App.restartDaemon` (`internal/tui/app.go`) calls the daemon
  client's `Shutdown()` RPC before closing the local connection, so the old
  daemon process is actually asked to exit. A `Shutdown()` error is logged and
  swallowed (mirrors the existing best-effort tone of the surrounding code) —
  the bounce proceeds to `Close()` + `WaitForShutdown` + `AutoStart` exactly
  as before.

## 2. Tests

- [x] 2.1 A test asserting `restartDaemon`'s shutdown path calls `Shutdown()`
  on the daemon client before/alongside `Close()`.
- [x] 2.2 A test asserting a `Shutdown()` RPC failure does not prevent the
  bounce from proceeding to `Close()` + `AutoStart`.

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/daemon-rpc.md`
  noting that a daemon/supervisor bounce now requests a real remote shutdown
  before forking a replacement, and why (a fixed-timeout race left the old
  daemon alive with no automatic retry on failure).
