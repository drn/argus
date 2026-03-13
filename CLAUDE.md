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
go test ./internal/store/   # run tests for a single package
```

## Architecture

**Elm Architecture (Model → Update → View)** via Bubble Tea. The entire UI is a single `tea.Program` with view switching.

- `cmd/argus/main.go` — Entry point. Opens SQLite database, creates agent runner, starts `tea.Program` with alt screen.
- `internal/ui/root.go` — **Top-level Bubble Tea model**. Owns all sub-views and routes key events based on current view state (`viewTaskList`, `viewNewTask`, `viewHelp`, `viewPrompt`, `viewConfirmDelete`). This is the orchestration hub.
- `internal/ui/tasklist.go` — Task list with cursor, scrolling, filtering. Not a `tea.Model` itself — it's a plain struct that `root.Model` drives.
- `internal/ui/newtask.go` — New task form using `bubbles/textinput`. Has its own `Update` method but is called by root.
- `internal/model/` — Core domain types. `Task` struct and `Status` enum with `pending → in_progress → in_review → complete` workflow. Status implements `encoding.TextMarshaler` for JSON serialization.
- `internal/db/` — SQLite-backed persistence at `~/.argus/data.sql`. Stores tasks, projects, backends, and config in a single database. Thread-safe with mutex. Auto-migrates from legacy JSON/TOML files on first run.
- `internal/config/config.go` — Config struct types and defaults. Struct types (`Config`, `Backend`, `Project`, `Keybindings`, `UIConfig`) are used throughout the codebase as value types. The `db.DB.Config()` method assembles a `Config` from the database.
- `internal/store/store.go` — Legacy JSON file persistence (superseded by `internal/db/`). Kept for reference but no longer imported by production code.
- `internal/agent/` — Agent process management with PTY:
  - `agent.go` — Backend resolution and command building (`BuildCmd`). Supports `--session-id` for conversation pinning.
  - `session.go` — PTY-backed process session via `creack/pty`. Single `readLoop` goroutine tees output to ring buffer + attached writer. Supports attach/detach without stopping the process.
  - `runner.go` — Multi-session manager keyed by task ID. Start/Stop/Get/Attach/Detach. Auto-cleans up on process exit, fires `onFinish` callback.
  - `attach.go` — `AttachCmd` implements `tea.ExecCommand` for Bubble Tea integration. Sets raw terminal mode, resizes PTY, uses detachReader to intercept `ctrl+q` for detach.
  - `ringbuffer.go` — Fixed-size circular buffer for output replay on reattach.
  - `errors.go` — Sentinel errors.

**Key pattern:** Sub-views (`TaskList`, `StatusBar`, `HelpView`) are plain structs with `View() string` methods — not independent `tea.Model`s. Only `NewTaskForm` has its own `Update` because it manages text input focus. Root model coordinates everything.

**Agent pattern:** A single `readLoop` goroutine is the sole reader of the PTY master fd. It always writes to the ring buffer, and when a writer is attached (via `session.attachW`), also tees output there. This avoids competing readers on the same fd. The detach key (`ctrl+q`) is intercepted by `detachReader` wrapping stdin.

## Config & Persistence

- Data dir: `~/.argus/`
- Database: SQLite (`data.sql`) via `modernc.org/sqlite` (pure Go, no CGO)
- Legacy config dir: `~/.config/argus/` (respects `XDG_CONFIG_HOME`) — auto-migrated to SQLite on first run
- Backends are command templates with prompt flag interpolation, not SDK integrations

## Key Learnings

- PTY child processes need a real terminal size at launch (`pty.StartWithSize`), not 0x0. TUI apps like claude won't render with zero dimensions.
- Use `charmbracelet/x/term` for raw mode (cross-platform) instead of platform-specific ioctls (`TIOCGETA` is macOS-only, `TCGETS` is Linux-only).
- `tea.ExecCommand.SetStdin/SetStdout` must capture and use Bubble Tea's `p.input`/`p.output` — don't hardcode `os.Stdin`/`os.Stdout`.
- The single-reader-tee pattern (one goroutine reads PTY, tees to buffer + optional writer) is critical. Two goroutines reading the same fd causes data loss.
- **Backend default config must be self-healing.** The default claude backend command MUST include `--dangerously-skip-permissions` and MUST NOT use `-p` as the prompt flag. `-p` puts Claude in non-interactive print mode (process prompt → exit), which silently breaks agent sessions — they show "waiting for output" then auto-complete. The `fixupBackends()` method in `internal/db/migrate.go` runs on every `Open()` to detect and repair outdated backend configs. Any change to `DefaultConfig()` backend values must be mirrored in `fixupBackends()` detection logic, or existing databases will retain stale values.

## Development Rules

- **Every change must include tests.** When adding new functionality, fixing bugs, or refactoring, always add or update corresponding tests. Run `go test ./...` to verify all tests pass before considering work complete.

## Planned but Not Yet Implemented

- Git worktree integration (`internal/worktree/`) — Phase 3
- Task import from markdown/JSON (`internal/import/`) — Phase 4
