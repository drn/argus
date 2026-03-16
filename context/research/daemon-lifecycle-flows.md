# Daemon Lifecycle: Startup, Shutdown, and Restart Flows

Comprehensive trace of every daemon lifecycle path, documenting the exact
goroutine-level ordering and the invariants that prevent zombie processes,
socket theft, and stale files.

## Background: The Three Bugs This Fixes

1. **Zombie daemon processes**: Old daemons never died because `Shutdown()` ran
   on a goroutine (signal/RPC handler) while `Serve()` returned on the main
   goroutine. `main()` exited, killing the Shutdown goroutine mid-cleanup.
   `StopAll()` never completed, leaving the old daemon blocked in `Accept()` on
   a deleted socket inode — alive but unreachable.

2. **Socket disappearing**: When a new daemon started, the old daemon's
   `Shutdown()` unconditionally called `os.Remove(DefaultSocketPath())`. If the
   old daemon was slow to die, it could delete the **new** daemon's socket file.

3. **SIGTERM silently swallowed**: After Shutdown via RPC, the signal handler
   goroutine exited (saw `d.done`), but `signal.Notify` was still active. Go
   caught subsequent SIGTERMs into the buffered `sigCh` channel that nobody
   read. `killExistingDaemon`'s SIGTERM was ignored, forcing a 2-second wait
   followed by SIGKILL every time.

## Design Principles

- **Cleanup runs on the Serve goroutine** (main goroutine in `runDaemon`), not
  the Shutdown goroutine. This ensures `StopAll()` and file removal complete
  before `main()` returns and the process exits.
- **Shutdown() only signals** — it closes `d.done` and the listener, then
  returns. No cleanup logic.
- **`signal.Stop(sigCh)`** restores default SIGTERM handling after shutdown
  starts, so the process can be killed normally by a newer daemon.
- **`removeIfOwnedByPID`** checks the PID file before removing socket/PID
  files. A dying daemon won't delete a newer daemon's socket.
- **`killExistingDaemon`** at the start of `Serve()` kills the PID-file daemon
  before binding the socket.
- **Paths derived from `sockPath`**: `pidPath` is
  `filepath.Dir(sockPath)/daemon.pid`, so tests using temp dirs never touch
  `~/.argus/`.

## Goroutine Map During Normal Operation

```
Main goroutine (runDaemon):
  └─ d.Serve(sockPath)
       └─ for { ln.Accept() → go handleConn() }

Signal handler goroutine:
  └─ select { <-sigCh → Shutdown() | <-d.done → signal.Stop(sigCh) }

Per-connection goroutines:
  └─ handleConn(conn, server)
       ├─ 'R' prefix → server.ServeCodec()  (blocks until conn closes)
       └─ 'S' prefix → handleStream()       (blocks until session exits)

Per-session goroutines (inside Runner):
  └─ readLoop()  (reads PTY fd, tees to ring buffer + writers)
```

## Flow 1: Fresh Start (`argus daemon start`, no existing daemon)

```
runDaemon()
  └─ d.Serve(DefaultSocketPath())
       1. pidPath = ~/.argus/daemon.pid
       2. killExistingDaemon(pidPath)
            → readPIDFile → file doesn't exist → return (no-op)
       3. os.Remove(sockPath) → no-op (doesn't exist)
       4. net.Listen("unix", sockPath) → creates socket file
       5. writePIDFile(pidPath) → writes our PID atomically (tmp + rename)
       6. signal.Notify(sigCh, SIGTERM, SIGINT) → signal handler goroutine starts
       7. log "daemon listening on..."
       8. Accept loop begins
```

**Invariants**: Socket file exists. PID file exists with our PID. No other
daemon is running.

## Flow 2: Start With Existing Daemon Running

```
New daemon's Serve():
  1. killExistingDaemon(pidPath):
       a. readPIDFile → old PID
       b. Signal(0) → alive
       c. Send SIGTERM to old daemon
       d. Old daemon receives SIGTERM:
            ┌─ Signal handler goroutine:
            │    Shutdown() → close(d.done) → ln.Close()
            │    signal.Stop(sigCh) → default SIGTERM restored
            │
            └─ Serve goroutine (old daemon's main):
                 Accept() fails → d.done closed → cleanup():
                   StopAll() → kills agent processes
                   removeIfOwnedByPID() → PID matches → removes sock + pid
                 return nil → runDaemon returns → process exits
       e. Poll Signal(0) → fails (dead) → return
  2. os.Remove(sockPath) → no-op (already removed by old daemon)
  3. net.Listen → creates socket
  4. writePIDFile → writes new PID
  5. Accept loop begins
```

**If StopAll is slow (>2s)**: `killExistingDaemon` sends SIGKILL. Old daemon's
cleanup is interrupted. Stale socket/PID files remain. New daemon's
`os.Remove` and `writePIDFile` handle them.

## Flow 3: CLI Restart (`argus daemon restart`)

```
runDaemonRestart():
  1. stopDaemon(sockPath):
       → Dial Unix socket → send "R" prefix → RPC "Daemon.Shutdown"
       → RPC handler: go d.Shutdown(); return resp immediately
  2. WaitForShutdown(sockPath, 3s):
       → Poll os.Stat(sockPath) every 50ms until gone
       → Socket removed when old daemon's cleanup() → removeIfOwnedByPID() runs
       → If StopAll is slow: times out after 3s (socket still exists)
  3. runDaemon() → Serve():
       → killExistingDaemon: if old daemon still alive (WaitForShutdown timed
         out), sends SIGTERM → default handler kills it immediately
         (signal.Stop was already called) → proceeds
       → Creates new socket + PID file
```

**Worst case timing**: 3s (WaitForShutdown timeout) + ~50ms (killExistingDaemon
detects death after SIGTERM) = ~3s total.

## Flow 4: TUI Restart (Settings UI)

```
restartDaemonCmd() [runs as tea.Cmd in background goroutine]:
  1. client.Shutdown() → sends RPC "Daemon.Shutdown"
  2. WaitForShutdown(sockPath, 3s) → polls for socket removal
  3. dclient.AutoStart(sockPath):
       → Forks: exec.Command(exe, "daemon", "start") with Setsid
       → cmd.Process.Release() (detach from parent)
       → Polls Connect(sockPath) every 50ms until success (3s timeout)
  4. Returns DaemonRestartedMsg{Client: newClient}
```

The new daemon process runs `runDaemon()` → `Serve()` → Flow 1 or Flow 2.

## Flow 5: TUI Auto-Start on Startup

```
runTUI():
  1. dclient.Connect(sockPath) → fails (no daemon)
  2. dclient.AutoStart(sockPath):
       → Forks "argus daemon start" (detached)
       → Polls Connect until socket appears (3s timeout)
  3. On success: runner = client (daemon-backed)
     On failure: runner = in-process Runner (fallback)
```

## Flow 6: SIGTERM With Active Sessions

```
1. SIGTERM arrives
2. Signal handler goroutine:
     Shutdown() → close(d.done) → ln.Close()
     signal.Stop(sigCh) → default SIGTERM restored
3. Serve goroutine:
     Accept() fails → d.done closed → cleanup():
       StopAll():
         For each session: SIGTERM to process group, wait for exit
         Each session's readLoop detects EOF, fires onFinish callback
       removeIfOwnedByPID() → removes sock + pid
     return nil
4. runDaemon() returns → main() returns → process exits
```

**Double SIGTERM (impatient user)**: After `signal.Stop`, the second SIGTERM
hits the default handler → process terminated immediately. StopAll interrupted.
Agent processes orphaned. Stale files left for next daemon to clean up.

## Flow 7: RPC Shutdown (from TUI or CLI)

```
RPC handler goroutine:
  1. log "rpc.Shutdown: requested"
  2. resp.OK = true
  3. go d.Shutdown()   ← runs in ANOTHER goroutine
  4. return nil        ← RPC response sent immediately

Shutdown goroutine:
  5. close(d.done)
  6. <-d.ready         ← instant (Serve already running)
  7. ln.Close()

Signal handler goroutine:
  8. sees <-d.done → signal.Stop(sigCh) → exits

Serve goroutine (main):
  9. Accept() fails → d.done closed → cleanup()
  10. StopAll()
  11. removeIfOwnedByPID()
  12. return nil → process exits
```

The RPC response (step 4) is sent **before** Shutdown starts (step 5). The
client gets the response quickly. Cleanup happens afterward on the main
goroutine.

## Race Condition Analysis: PID File Ownership

**Could old daemon's `removeIfOwnedByPID` delete the new daemon's socket?**

No. The invariant is enforced by `killExistingDaemon`:

1. New daemon's `killExistingDaemon` kills old daemon and **waits for it to die**
2. Only after old daemon is dead does new daemon proceed to create socket + PID
3. Old daemon cannot run `removeIfOwnedByPID` after it's dead

Even without `killExistingDaemon` (e.g., RPC shutdown path), the `removeIfOwnedByPID`
check prevents theft:

- If old daemon's cleanup runs BEFORE new daemon writes PID → old daemon removes
  its own files (PID matches). New daemon creates new files afterward.
- If old daemon's cleanup runs AFTER new daemon writes PID → `readPIDFile` sees
  new PID → doesn't match → skips removal.

**TOCTOU window**: Between `readPIDFile` and `os.Remove` in `removeIfOwnedByPID`,
could the PID file change? Only if a new daemon writes its PID in that nanosecond
window. But new daemons only write PIDs after killing us via `killExistingDaemon`,
so we'd already be dead. Not exploitable.

## File Lifecycle

| Event | Socket | PID File |
|-------|--------|----------|
| `Serve` starts | Removed (stale) then created by `net.Listen` | Written by `writePIDFile` |
| Daemon running | Exists, accepting connections | Exists, contains our PID |
| `Shutdown` called | Listener closed (file still exists) | Still exists |
| `cleanup()` runs | Removed by `removeIfOwnedByPID` (if we own it) | Removed (if we own it) |
| Process exits | Gone | Gone |
| SIGKILL (forced) | Left on disk (stale) | Left on disk (stale) |
| Next daemon start | `os.Remove` cleans stale, `net.Listen` creates new | `writePIDFile` overwrites |

## Known Limitations

1. **PID reuse**: If the old daemon crashed and its PID was reused by an
   unrelated process, `killExistingDaemon` would SIGTERM that process.
   Mitigated by: single-user tool, short PID file lifetime, unlikely in practice.

2. **Existing zombie cleanup**: The 11+ zombie daemons from before this fix
   aren't in the PID file. `killExistingDaemon` only kills the one PID.
   One-time manual cleanup needed: `pkill -f "argus daemon"`.

3. **Slow restart with many agents**: `WaitForShutdown` waits for socket
   removal, which now happens after `StopAll`. If agents are slow to die,
   restart takes 3s (timeout) before `killExistingDaemon` forcibly kills.
   This is the correct tradeoff — the old behavior (immediate socket removal)
   caused the bugs.
