# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Argus is a terminal-native LLM code orchestrator built with Go + tcell/tview. It manages multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and keyboard-driven workflow.

## Build & Run

```bash
make build                  # go build ./...
make vet                    # go vet ./...
make test                   # go test -race -count=1 ./...
make test-pkg PKG=./internal/db/  # single package, verbose
make test-cover             # coverage profile + summary
make test-watch             # gotestsum --watch (install: go install gotest.tools/gotestsum@latest)
go build -o argus ./cmd/argus/    # build binary
```

## Test-Driven Development

Follow Red-Green-Refactor as the default workflow:
1. **Red** — Write a failing test first using `internal/testutil` assertions
2. **Green** — Write the minimum code to make it pass
3. **Refactor** — Clean up while keeping tests green

Use `make test-watch` for continuous feedback. Use `make test-pkg` for focused iteration on a single package.

**Assertions** — use `internal/testutil` (not raw `if got != want`):
```go
import "github.com/drn/argus/internal/testutil"

testutil.Equal(t, got, want)           // comparable types
testutil.DeepEqual(t, got, want)       // structs/slices via go-cmp
testutil.NoError(t, err)               // err == nil
testutil.ErrorIs(t, err, target)       // errors.Is
testutil.Nil(t, val)                   // handles nil-interface trap
testutil.Contains(t, s, substr)        // string contains
```

All table-driven tests must use `t.Run` subtests. Guard slow tests with `testing.Short()`.

## Architecture

**tcell/tview UI** with direct cell painting for the agent terminal pane. The `App` struct owns the `tview.Application`, DB, runner, and all sub-views.

- `cmd/argus/main.go` — Entry point. Parses subcommands (`daemon`, `daemon stop`), opens SQLite database. In TUI mode: tries daemon client first, falls back to in-process runner. Starts the tcell/tview app.
- `internal/tui2/app.go` — **Top-level tview application**. Owns all sub-views and routes key events via `tapp.SetInputCapture()`. View switching via `tview.Pages`. Layout uses `tview.Flex` (vertical: header + pages + statusbar).
- `internal/tui2/tasklist.go` — Task list with collapsible project folders, cursor, scrolling, filtering. Tasks are grouped by project name into a flattened row list (project headers + task rows). Only one project is expanded at a time — auto-expands when the cursor enters a project, auto-collapses others. Cursor navigation skips project header rows entirely. Includes an **Archive section** at the bottom — the archive auto-expands when the cursor enters it and auto-collapses when the cursor leaves. Archived projects are only displayed within the archive section, never in the main section.
- `internal/tui2/terminalpane.go` — Custom `tview.Box` widget for the agent terminal. Feeds PTY bytes to an x/vt emulator and paints cells directly to `tcell.Screen` via `paintVT()`. Supports live mode (incremental byte feeding), scrollback (x/vt native `Scrollback()` buffer), and log replay for finished sessions. Damage tracking via `Touched()` for efficient incremental repainting.
- `internal/tui2/gitstatus.go` — `GitPanel` for git status/diff/branch display in both agent view and task list.
- `internal/tui2/fileexplorer.go` — `FilePanel` with auto-expand, cursor navigation, and status icons.
- `internal/tui2/reviews.go` — Reviews tab: three-panel layout (PR list / diff / comments) with GitHub API integration.
- `internal/tui2/settings.go` — Settings tab with sections for status, sandbox, projects, backends, KB, and UX logs.
- `internal/tui2/newtaskform.go` — New task form as modal overlay via `tview.Pages.AddPage`.
- `internal/tui2/taskpage.go` — Task list page wrapper with three-panel layout (tasks | git+preview | details) and empty-state banner.
- `internal/app/agentview/` — Runtime-agnostic agent view state: `State`, `Panel`, `DiffState`, `TerminalAdapter` interface, `SessionLookup`.
- `internal/model/` — Core domain types. `Task` struct and `Status` enum with `pending → in_progress → in_review → complete` workflow. Status implements `encoding.TextMarshaler` for JSON serialization.
- `internal/db/` — SQLite-backed persistence at `~/.argus/data.sql`. Stores tasks, projects, backends, and config in a single database. Thread-safe with mutex. Seeds defaults on first run.
- `internal/config/config.go` — Config struct types and defaults. Struct types (`Config`, `Backend`, `Project`, `Keybindings`, `UIConfig`) are used throughout the codebase as value types. The `db.DB.Config()` method assembles a `Config` from the database.
- `internal/agent/` — Agent process management with PTY:
  - `agent.go` — Backend resolution and command building (`BuildCmd`). Supports `--session-id` for conversation pinning.
  - `worktree.go` — Git worktree creation under `~/.argus/worktrees/<project>/<task>` with `argus/<task>` branch naming.
  - `iface.go` — `SessionProvider` (manages sessions) and `SessionHandle` (single session) interfaces. UI code depends only on these interfaces, enabling both in-process and daemon-backed implementations.
  - `session.go` — PTY-backed process session via `creack/pty`. Single `readLoop` goroutine tees output to ring buffer + all attached writers. Multi-writer support via `AddWriter`/`RemoveWriter` for fan-out to multiple consumers. Supports attach/detach without stopping the process.
  - `runner.go` — Multi-session manager keyed by task ID. Implements `SessionProvider`. Start/Stop/Get/Attach/Detach. Auto-cleans up on process exit, fires `onFinish` callback.
  - `attach.go` — `AttachCmd` for full-screen terminal attach. Sets raw terminal mode, resizes PTY, uses detachReader to intercept `ctrl+q` for detach.
  - `ringbuffer.go` — Exported `RingBuffer` — fixed-size circular buffer for output replay on reattach. Used by both in-process sessions and daemon client's local buffer.
  - `errors.go` — Sentinel errors.
- `internal/daemon/` — Daemon architecture for persistent agent sessions:
  - `daemon.go` — `Daemon` struct: owns Runner, accepts Unix socket connections, dispatches RPC vs stream (first byte 'R'/'S'). PID file at `~/.argus/daemon.pid`. Signal handling (SIGTERM/SIGINT → graceful shutdown).
  - `types.go` — Shared RPC request/response types (`StartReq`, `SessionInfo`, `StreamHeader`, etc.).
  - `rpc.go` — `RPCService` implementing JSON-RPC methods: Ping, StartSession, StopSession, StopAll, SessionStatus, ListSessions, WriteInput, Resize, Shutdown.
  - `stream.go` — Output streaming handler. Client sends `StreamHeader` JSON, daemon calls `AddWriter(conn)` on the session. Raw bytes flow until session exit or client disconnect.
- `internal/uxlog/` — UX debug logging for the TUI layer. Writes to `~/.argus/ux.log`, separate from daemon logs. Logs task start/stop/finish, status transitions, stream connect/disconnect, RPC timeouts. Viewable in Settings → UX Logs.
- `internal/daemon/client/` — TUI-side daemon client:
  - `client.go` — `Client` implementing `SessionProvider` via JSON-RPC to daemon. Manages `RemoteSession` lifecycle.
  - `handle.go` — `RemoteSession` implementing `SessionHandle`. Local `RingBuffer` populated by stream reader. RPC calls for WriteInput, Resize, PTYSize, etc.
  - `stream.go` — Goroutine reads raw bytes from daemon stream connection into local ring buffer.
- `internal/gitutil/` — Git operations, diff parsing, changed files. Pure Go with no UI dependencies. Used by tui2 for git status, file diffs, and worktree management.
- `internal/skills/` — Skill loading for autocomplete. Scans `~/.claude/skills/` and project-specific skill directories.

**Key pattern:** Sub-views are custom `tview.Box` widgets with `Draw(screen tcell.Screen)` methods. Async updates via `tapp.QueueUpdateDraw()` from the tick goroutine. Key routing via `tapp.SetInputCapture()`.

**Agent pattern:** A single `readLoop` goroutine is the sole reader of the PTY master fd. It always writes to the ring buffer, and tees output to all attached writers (via `session.writers` slice). Writers are copied under lock before iterating; errored writers are removed automatically. `AddWriter(w)` replays the ring buffer then registers for live output. `Attach()`/`Detach()` use AddWriter/RemoveWriter internally. The detach key (`ctrl+q`) is intercepted by `detachReader` wrapping stdin.

**Terminal rendering:** PTY bytes → x/vt emulator (`charmbracelet/x/vt`) → cells painted directly to `tcell.SetContent()`. No ANSI string intermediary. Damage tracking via `Touched()` enables incremental repainting. Scrollback uses x/vt's native `Scrollback()` buffer. The cursor is rendered unconditionally with high-contrast colors regardless of `CursorVisible()`.

**Daemon pattern:** The daemon (`argus daemon`) owns the Runner and PTY sessions. The TUI connects via Unix socket (`~/.argus/daemon.sock`). First byte on each connection selects the protocol: 'R' for JSON-RPC (request/response), 'S' for output streaming (raw bytes). The TUI's `Client` implements `SessionProvider` so the UI code is identical whether running in-process or via daemon. Sessions survive TUI restarts — the daemon keeps PTY fds alive until explicit stop or shutdown. The TUI auto-starts the daemon if none is running: `autoStartDaemon()` forks the current binary with `Setsid` for process group detachment, then polls the socket until ready (50ms intervals, 3s timeout). Falls back to in-process mode if auto-start fails, with a warning shown in the Settings tab.

**Task/worktree lifecycle:** New task form submission creates worktree BEFORE persisting the task: `agent.CreateWorktree(projDir, project, taskName, branch)` creates worktree at `~/.argus/worktrees/<project>/<task>` with branch `argus/<task>`. If worktree creation fails, the task is NOT created — the form stays open with the error message. On name conflict (directory exists), `CreateWorktree` auto-suffixes with `-1`, `-2`, etc. Only after worktree succeeds: `db.Add(task)` → `startOrAttach` generates session ID → `runner.Start` builds command with `cmd.Dir = t.Worktree` → captures PID in DB. On delete/destroy: stops agent → `removeWorktreeAndBranch(path, branch, repoDir)` removes worktree (via `git worktree remove` from repoDir) → deletes local branch → deletes remote branch → removes from DB.

**Git status pattern:** Git operations (worktree discovery, diff, status) must **never** run synchronously on the UI thread. Git commands run in background goroutines and deliver results via `QueueUpdateDraw` callbacks. Resolved paths are cached to avoid repeated lookups.

## Config & Persistence

- Data dir: `~/.argus/`
- Database: SQLite (`data.sql`) via `modernc.org/sqlite` (pure Go, no CGO)
- Backends are command templates with prompt flag interpolation, not SDK integrations

## Breaking Changes Policy

- Only one user (the author) — breaking changes are fine, no backwards compatibility needed
- No legacy migration code — if a schema change requires data migration, write a one-off script
- `internal/store/` (legacy JSON persistence) and `config.toml` support have been removed

## Key Learnings

@context/knowledge/key-learnings.md

## Planned but Not Yet Implemented

- Task import from markdown/JSON (`internal/import/`) — Phase 4
- Task list preview panel (small terminal snapshot per task)
