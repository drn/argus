# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Is

Argus is a terminal-native LLM code orchestrator built with Go + Bubble Tea. It manages multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and keyboard-driven workflow.

## Build & Run

```bash
go build ./...              # build all packages
go build -o argus ./cmd/argus/  # build binary
go vet ./...                # lint
go test ./...               # run all tests
go test ./internal/db/      # run tests for a single package
```

## Architecture

**Elm Architecture (Model → Update → View)** via Bubble Tea. The entire UI is a single `tea.Program` with view switching.

- `cmd/argus/main.go` — Entry point. Parses subcommands (`daemon`, `daemon stop`), opens SQLite database. In TUI mode: tries daemon client first, falls back to in-process runner. Starts `tea.Program` with alt screen and mouse cell motion.
- `internal/ui/root.go` — **Top-level Bubble Tea model**. Owns all sub-views and routes key events based on current view state (`viewTaskList`, `viewNewTask`, `viewHelp`, `viewPrompt`, `viewConfirmDelete`). This is the orchestration hub.
- `internal/ui/worktree.go` — Git worktree discovery, cleanup, and process management helpers. Extracted from root.go to separate infrastructure concerns from UI logic.
- `internal/ui/tasklist.go` — Task list with collapsible project folders, cursor, scrolling, filtering. Tasks are grouped by project name into a flattened row list (project headers + task rows). Only one project is expanded at a time — auto-expands when the cursor enters a project, auto-collapses others. Cursor navigation skips project header rows entirely (`moveCursor`) — the cursor always lands on a task row, never a header. When navigating up across projects, cursor lands on the *last* task of the previous project. The `skipUpPastHeader(prev)` helper handles moving up past any header type (project or archive), chaining through consecutive headers (e.g., project header → archive header). `landOnLastTask(idx, prev)` finds the last task row after a project header and adjusts scroll — used by all upward-skip paths. Not a `tea.Model` itself — it's a plain struct that `root.Model` drives. Includes an **Archive section** at the bottom of the task list — the archive auto-expands when the cursor enters it and auto-collapses when the cursor leaves. Archived projects are only displayed within the archive section, never in the main section. Within the archive, projects auto-expand one at a time as the cursor moves between them (`archiveProject` vs `expanded`).
- `internal/ui/panellayout.go` — Shared `PanelLayout` struct for horizontal multi-panel layouts. Handles percentage-based width splitting with minimums, right-to-left compression when narrow, height padding, and horizontal joining. Used by both `AgentView` and the task list view with identical 20/60/20 ratios to ensure visual consistency.
- `internal/ui/settings.go` — Settings tab view with sections for status warnings, projects, and backends. Left panel is a scrollable section list; right panel shows detail for the selected item. Cursor skips section headers. The `daemonConnected` flag on Model drives the "in-process mode" warning.
- `internal/ui/newtask.go` — New task form using `bubbles/textinput`. Has its own `Update` method but is called by root.
- `internal/model/` — Core domain types. `Task` struct and `Status` enum with `pending → in_progress → in_review → complete` workflow. Status implements `encoding.TextMarshaler` for JSON serialization.
- `internal/db/` — SQLite-backed persistence at `~/.argus/data.sql`. Stores tasks, projects, backends, and config in a single database. Thread-safe with mutex. Seeds defaults on first run.
- `internal/config/config.go` — Config struct types and defaults. Struct types (`Config`, `Backend`, `Project`, `Keybindings`, `UIConfig`) are used throughout the codebase as value types. The `db.DB.Config()` method assembles a `Config` from the database.
- `internal/agent/` — Agent process management with PTY:
  - `agent.go` — Backend resolution and command building (`BuildCmd`). Supports `--session-id` for conversation pinning.
  - `worktree.go` — Git worktree creation under `~/.argus/worktrees/<project>/<task>` with `argus/<task>` branch naming.
  - `iface.go` — `SessionProvider` (manages sessions) and `SessionHandle` (single session) interfaces. UI code depends only on these interfaces, enabling both in-process and daemon-backed implementations.
  - `session.go` — PTY-backed process session via `creack/pty`. Single `readLoop` goroutine tees output to ring buffer + all attached writers. Multi-writer support via `AddWriter`/`RemoveWriter` for fan-out to multiple consumers. Supports attach/detach without stopping the process.
  - `runner.go` — Multi-session manager keyed by task ID. Implements `SessionProvider`. Start/Stop/Get/Attach/Detach. Auto-cleans up on process exit, fires `onFinish` callback.
  - `attach.go` — `AttachCmd` implements `tea.ExecCommand` for Bubble Tea integration. Sets raw terminal mode, resizes PTY, uses detachReader to intercept `ctrl+q` for detach.
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

**Key pattern:** Sub-views (`TaskList`, `StatusBar`, `HelpView`) are plain structs with `View() string` methods — not independent `tea.Model`s. Only `NewTaskForm` has its own `Update` because it manages text input focus. Root model coordinates everything.

**Agent pattern:** A single `readLoop` goroutine is the sole reader of the PTY master fd. It always writes to the ring buffer, and tees output to all attached writers (via `session.writers` slice). Writers are copied under lock before iterating; errored writers are removed automatically. `AddWriter(w)` replays the ring buffer then registers for live output. `Attach()`/`Detach()` use AddWriter/RemoveWriter internally. The detach key (`ctrl+q`) is intercepted by `detachReader` wrapping stdin.

**Daemon pattern:** The daemon (`argus daemon`) owns the Runner and PTY sessions. The TUI connects via Unix socket (`~/.argus/daemon.sock`). First byte on each connection selects the protocol: 'R' for JSON-RPC (request/response), 'S' for output streaming (raw bytes). The TUI's `Client` implements `SessionProvider` so the UI code is identical whether running in-process or via daemon. Sessions survive TUI restarts — the daemon keeps PTY fds alive until explicit stop or shutdown. The TUI auto-starts the daemon if none is running: `autoStartDaemon()` forks the current binary with `Setsid` for process group detachment, then polls the socket until ready (50ms intervals, 3s timeout). Falls back to in-process mode if auto-start fails, with a warning shown in the Settings tab.

**Task/worktree lifecycle:** New task form submission → `handleNewTaskKey` creates worktree BEFORE persisting the task: `agent.CreateWorktree(projDir, project, taskName, branch)` creates worktree at `~/.argus/worktrees/<project>/<task>` with branch `argus/<task>`. If worktree creation fails, the task is NOT created — the form stays open with the error message. On name conflict (directory exists), `CreateWorktree` auto-suffixes with `-1`, `-2`, etc. Only after worktree succeeds: `db.Add(task)` → `startOrAttach` generates session ID → `runner.Start` builds command with `cmd.Dir = t.Worktree` → captures PID in DB. On delete/destroy: stops agent → `removeWorktreeAndBranch(path, branch, repoDir)` removes worktree (via `git worktree remove` from repoDir) → deletes local branch → deletes remote branch → removes from DB.

**Git status pattern:** Git operations (worktree discovery, diff, status) must **never** run synchronously on the UI thread. The `resolvedDirs` cache on `Model` stores task ID → worktree dir mappings. On cache hit, `scheduleGitRefresh()` kicks off `FetchGitStatus` as a `tea.Cmd`. On cache miss, it fires `resolveTaskDirAsync()` which returns a `ResolveTaskDirMsg` — only then does the git status fetch begin. This two-phase async pattern keeps the UI responsive. Maps are reference types in Go, so the cache works correctly even through Bubble Tea's value-receiver `Update` method.

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
