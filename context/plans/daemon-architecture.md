# Argus Daemon Architecture

## Context

When the Argus TUI exits, PTY master file descriptors are garbage collected, causing child Claude Code processes to receive SIGHUP and die. There's no `runner.StopAll()` in the quit path and no process group management. This means closing Argus kills all running sessions.

The goal is to split Argus into a **daemon** (owns PTY sessions, survives UI close) and a **TUI client** (connects to daemon, pure display layer). Sessions persist across TUI restarts.

## Architecture

```
┌──────────────┐     Unix socket      ┌──────────────────┐
│  argus (TUI) │ ◄──────────────────► │  argusd (daemon)  │
│              │   JSON-RPC + stream  │                    │
│  ui.Model    │                      │  agent.Runner      │
│  db.DB (r/w) │                      │  agent.Session(s)  │
└──────────────┘                      │  db.DB (r/w)       │
                                      └──────────────────┘
                                           │ PTY │ PTY │
                                           ▼     ▼     ▼
                                       claude  claude  claude
```

- **Daemon** owns Runner, Sessions, PTY fds, ring buffers. Long-lived.
- **TUI** connects via `~/.argus/daemon.sock`. Sends input, receives output stream. Ephemeral.
- **DB** accessed independently by both (SQLite WAL handles concurrent access).

## IPC Protocol

**Transport:** Unix domain socket at `~/.argus/daemon.sock`

**RPC (request/response):** `net/rpc` with `jsonrpc` codec. Zero new dependencies.

```
Daemon.Ping()                              → PongResp
Daemon.StartSession(StartReq)              → StartResp{PID}
Daemon.StopSession(TaskIDReq)              → StatusResp
Daemon.StopAll()                           → StatusResp
Daemon.SessionStatus(TaskIDReq)            → SessionInfo{Alive, Idle, PID, PTYCols, PTYRows, WorkDir}
Daemon.ListSessions()                      → []SessionInfo
Daemon.WriteInput(WriteReq{TaskID, Data})  → StatusResp
Daemon.Resize(ResizeReq{TaskID, R, C})     → StatusResp
Daemon.Shutdown()                          → StatusResp
```

**Output streaming (dedicated connection):** TUI opens a second socket connection per session. Sends a JSON header `{"task_id":"X","from_byte":N}`, then daemon replays ring buffer from byte N and streams live output as raw bytes. The daemon registers the stream as an `attachW` on the session. On disconnect, writer is removed. Supports multiple concurrent TUI clients.

**Agent exit notifications:** When a session exits, the daemon writes a 0-byte frame on the stream connection (EOF), then the TUI reads the updated task status from DB. This replaces the current `p.Send(AgentFinishedMsg{...})` pattern.

## Performance

No meaningful performance penalty vs current in-process architecture:

- **Output rendering (hot path):** Output streams over Unix socket into a client-local ring buffer. The 100ms agent view tick reads from the local buffer — identical to today. Unix domain sockets are kernel-local memcpy, sub-microsecond overhead per chunk.
- **Input forwarding:** `WriteInput` → JSON-RPC → PTY write adds ~0.1ms per keystroke. Human keypress intervals are 50-150ms.
- **Status queries:** Called on 1-second ticks. ~0.1ms per RPC is negligible.

## Implementation Phases

### Phase 1: Extract SessionProvider Interface

No daemon yet. Pure refactor to decouple the UI from concrete `*agent.Runner` / `*Session` types.

**New file:** `internal/agent/iface.go`
```go
type SessionProvider interface {
    Start(task *model.Task, cfg config.Config, rows, cols uint16, resume bool) (SessionHandle, error)
    Stop(taskID string) error
    StopAll()
    Get(taskID string) SessionHandle  // returns nil if not found
    Running() []string
    Idle() []string
    HasSession(taskID string) bool
    WorkDir(taskID string) string
}

type SessionHandle interface {
    PID() int
    WriteInput(p []byte) (int, error)
    Resize(rows, cols uint16) error
    RecentOutput() []byte
    TotalWritten() uint64
    IsIdle() bool
    Alive() bool
    PTYSize() (cols, rows int)
    Done() <-chan struct{}
    Err() error
    WorkDir() string
    Stop() error
}
```

**Modify:** `internal/agent/runner.go`
- Change `Runner.Get()` return type from `*Session` to `SessionHandle`
- Change `Runner.Start()` return type from `*Session` to `SessionHandle`
- Add compile-time assertion: `var _ SessionProvider = (*Runner)(nil)`

**Modify:** `internal/agent/session.go`
- Add compile-time assertion: `var _ SessionHandle = (*Session)(nil)`

**Modify:** `internal/ui/root.go`
- Change `runner *agent.Runner` → `runner agent.SessionProvider` on Model struct
- Change `NewModel(database *db.DB, runner *agent.Runner)` → `NewModel(database *db.DB, runner agent.SessionProvider)`

**Modify:** `internal/ui/agentview.go`
- Change `runner *agent.Runner` → `runner agent.SessionProvider` on AgentView struct
- Change `NewAgentView(theme Theme, runner *agent.Runner)` → `NewAgentView(theme Theme, runner agent.SessionProvider)`

**Modify:** `internal/ui/preview.go`
- Change `runner *agent.Runner` → `runner agent.SessionProvider` on Preview struct
- Change `NewPreview(theme Theme, runner *agent.Runner)` → `NewPreview(theme Theme, runner agent.SessionProvider)`

**Verify:** `go build ./...` && `go test ./...` — everything works identically with the interface.

### Phase 2: Multi-Writer Support on Session

Currently `attachW` is a single `io.Writer`. The daemon needs to fan out to multiple TUI clients.

**Modify:** `internal/agent/session.go`
- Replace `attachW io.Writer` with `attachWs []io.Writer`
- `readLoop()`: iterate `attachWs` slice, write to each (remove on error)
- New method: `AddWriter(w io.Writer)` — appends to `attachWs` under lock, replays ring buffer to `w`
- New method: `RemoveWriter(w io.Writer)` — removes from `attachWs` under lock
- Existing `Attach()`/`Detach()` continue to work (they use AddWriter/RemoveWriter internally)

### Phase 3: Daemon Core

**New file:** `internal/daemon/types.go` — Shared request/response types
```go
type StartReq struct {
    TaskID    string
    SessionID string
    Prompt    string
    Project   string
    Backend   string
    Worktree  string
    Branch    string
    Rows, Cols uint16
    Resume     bool
}
type StartResp struct { PID int }
type TaskIDReq struct { TaskID string }
type StatusResp struct { OK bool; Error string }
type SessionInfo struct { TaskID string; Alive, Idle bool; PID int; Cols, Rows int; WorkDir string; TotalWritten uint64 }
type WriteReq struct { TaskID string; Data []byte }
type ResizeReq struct { TaskID string; Rows, Cols uint16 }
type StreamHeader struct { TaskID string; FromByte uint64 }
```

**New file:** `internal/daemon/daemon.go`
```go
type Daemon struct {
    db       *db.DB
    runner   *agent.Runner
    listener net.Listener
    streams  map[string][]net.Conn  // taskID → connected stream clients
    mu       sync.Mutex
}
```
- `New(db)` — creates Runner with onFinish that updates DB + notifies stream clients
- `Serve()` — accepts connections, dispatches to RPC server or stream handler (first byte distinguishes: 'R' for RPC, 'S' for stream)
- `Shutdown()` — stops listener, stops all sessions, removes socket/PID file
- Signal handling: trap SIGTERM/SIGINT → graceful shutdown
- PID file at `~/.argus/daemon.pid` (atomic write via temp+rename)
- Resume in-progress sessions on startup (port existing `Init()` logic from root.go)

**New file:** `internal/daemon/rpc.go` — RPC method implementations
- Each method wraps the corresponding `Runner` call
- `StartSession`: builds task from request fields, loads config from DB, calls `runner.Start()`
- `WriteInput`: gets session, calls `sess.WriteInput()`
- etc.

**New file:** `internal/daemon/stream.go` — Output streaming
- Reads `StreamHeader` JSON from connection
- Gets session, calls `sess.AddWriter(conn)` (replays from `FromByte`)
- Blocks until connection closes or session exits
- On session exit: write empty frame, close connection
- On client disconnect: `sess.RemoveWriter(conn)`

**Modify:** `cmd/argus/main.go` — add `daemon` subcommand
- `argus daemon` → opens DB, creates Daemon, calls `Serve()`, blocks until shutdown
- `argus daemon stop` → connects to socket, calls `Daemon.Shutdown`, exits
- `argus` (no args) → existing TUI behavior, but with auto-start daemon + client connection

### Phase 4: Daemon Client

**New file:** `internal/daemon/client/client.go`
```go
type Client struct {
    rpc     *rpc.Client
    streams map[string]*StreamReader  // taskID → active stream
    mu      sync.Mutex
}
```
- Implements `agent.SessionProvider`
- `Start()` → RPC `Daemon.StartSession`, opens stream connection, returns `RemoteSession`
- `Get()` → returns `RemoteSession` from local cache (or creates one with stream)
- `Stop()` → RPC `Daemon.StopSession`
- `Running()`, `Idle()`, etc. → RPC calls

**New file:** `internal/daemon/client/handle.go`
```go
type RemoteSession struct {
    taskID string
    client *Client
    stream *StreamReader  // receives PTY output into local ring buffer
    buf    *agent.RingBuffer  // local copy for RecentOutput()/TotalWritten()
    done   chan struct{}
}
```
- Implements `agent.SessionHandle`
- `WriteInput()` → RPC `Daemon.WriteInput`
- `Resize()` → RPC `Daemon.Resize`
- `RecentOutput()` → reads from local ring buffer (populated by stream)
- `TotalWritten()` → reads from local ring buffer
- `Done()` → channel closed when stream EOF received (daemon signals session exit)
- `Alive()` → check if done channel is closed
- `PID()`, `PTYSize()`, `IsIdle()`, `WorkDir()` → RPC `Daemon.SessionStatus` (cached briefly)

**New file:** `internal/daemon/client/stream.go`
- `StreamReader` goroutine: reads raw bytes from stream connection, writes to local ring buffer
- On EOF: close `done` channel on `RemoteSession`

### Phase 5: Wire It Together

**Modify:** `cmd/argus/main.go`
```go
func main() {
    database, err := db.Open(db.DefaultPath())
    // ...

    // Connect to daemon (auto-start if not running)
    client, err := client.Connect("~/.argus/daemon.sock")
    if err != nil {
        // No daemon running — start one
        ensureDaemon()
        client, err = client.Connect("~/.argus/daemon.sock")
    }
    defer client.Close()

    // onFinish: daemon handles DB updates, TUI gets notified via stream EOF
    // The client synthesizes AgentFinishedMsg from stream EOF events
    var p *tea.Program
    client.OnSessionExit(func(taskID string) {
        if p != nil {
            task, _ := database.Get(taskID)  // read updated status from DB
            p.Send(ui.AgentFinishedMsg{TaskID: taskID, ...})
        }
    })

    m := ui.NewModel(database, client)  // client implements SessionProvider
    p = tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
    p.Run()
    // TUI exits — sessions keep running in daemon!
}
```

**Modify:** `internal/ui/root.go` `Init()`
- Remove session resume logic (daemon resumes sessions itself)
- Query `runner.Running()` to discover active sessions and sync UI state

**Auto-start helper:** `ensureDaemon()` in `cmd/argus/main.go`
- Re-exec self as `argus daemon` via `exec.Command(os.Args[0], "daemon")`
- Redirect stdout/stderr → `~/.argus/daemon.log` (for debugging)
- `cmd.Start()` (not `Run` — don't wait), detach from parent
- Poll for socket file (up to 2 seconds, 50ms interval)

### Phase 6: RingBuffer Export

The `ringBuffer` type is currently unexported (lowercase). The daemon client needs its own local buffer.

**Modify:** `internal/agent/ringbuffer.go`
- Export: `RingBuffer`, `NewRingBuffer(size)`, all methods
- Or: create a separate `internal/daemon/client/buffer.go` that duplicates the simple ring buffer (avoids coupling)

Prefer exporting — it's the same package ecosystem, no reason to duplicate.

## Files Summary

| File | Action | Phase |
|------|--------|-------|
| `internal/agent/iface.go` | **Create** — SessionProvider + SessionHandle interfaces | 1 |
| `internal/agent/runner.go` | Modify — return interfaces, compile-time check | 1 |
| `internal/agent/session.go` | Modify — compile-time check; multi-writer (Phase 2) | 1, 2 |
| `internal/agent/ringbuffer.go` | Modify — export types | 6 |
| `internal/ui/root.go` | Modify — interface type, remove resume logic (Phase 5) | 1, 5 |
| `internal/ui/agentview.go` | Modify — interface type | 1 |
| `internal/ui/preview.go` | Modify — interface type | 1 |
| `internal/daemon/types.go` | **Create** — shared RPC types | 3 |
| `internal/daemon/daemon.go` | **Create** — daemon core, signal handling, PID file | 3 |
| `internal/daemon/rpc.go` | **Create** — RPC method implementations | 3 |
| `internal/daemon/stream.go` | **Create** — output streaming handler | 3 |
| `internal/daemon/client/client.go` | **Create** — Client implementing SessionProvider | 4 |
| `internal/daemon/client/handle.go` | **Create** — RemoteSession implementing SessionHandle | 4 |
| `internal/daemon/client/stream.go` | **Create** — stream reader goroutine | 4 |
| `cmd/argus/main.go` | Modify — add `daemon` subcommand, auto-start, use client | 3, 5 |

## Verification

1. **Phase 1:** `go build ./...` && `go test ./...` — pure refactor, no behavior change
2. **Phase 2:** Write test with 2 writers on same session, verify both receive output
3. **Phase 3:** Start `argus daemon` manually, use `nc -U ~/.argus/daemon.sock` to send JSON-RPC ping
4. **Phase 4:** Write integration test: start daemon, connect client, start session, read output
5. **Phase 5:** Full E2E: `argus` auto-starts daemon, create task, start agent, quit TUI, relaunch TUI, verify session still running and output streams from where it left off
6. **Regression:** All existing `go test ./...` must pass throughout

## Decisions

- **Subcommand:** `argus daemon` (not a separate binary). Single binary, no PATH issues.
- **Auto-start:** Running `argus` (TUI) auto-starts the daemon if not already running.
- **Lifecycle:** Daemon stays alive until explicit `argus daemon stop` or SIGTERM. No auto-shutdown.
