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
- `internal/ui/worktree.go` — Git worktree discovery, cleanup, and process management helpers. Extracted from root.go to separate infrastructure concerns from UI logic.
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

**Git status pattern:** Git operations (worktree discovery, diff, status) must **never** run synchronously on the UI thread. The `resolvedDirs` cache on `Model` stores task ID → worktree dir mappings. On cache hit, `scheduleGitRefresh()` kicks off `FetchGitStatus` as a `tea.Cmd`. On cache miss, it fires `resolveTaskDirAsync()` which returns a `ResolveTaskDirMsg` — only then does the git status fetch begin. This two-phase async pattern keeps the UI responsive. Maps are reference types in Go, so the cache works correctly even through Bubble Tea's value-receiver `Update` method.

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
- **Never run git commands or filesystem discovery synchronously in `Update()`.** Even fast git commands take 50-500ms which freezes the entire TUI. Use `tea.Cmd` to run them in background goroutines and deliver results via messages. Cache resolved paths in a `map` on the model to avoid repeated lookups.
- **Backend default config must be self-healing.** The default claude backend command MUST include `--dangerously-skip-permissions` and MUST NOT use `-p` as the prompt flag. `-p` puts Claude in non-interactive print mode (process prompt → exit), which silently breaks agent sessions — they show "waiting for output" then auto-complete. The `fixupBackends()` method in `internal/db/migrate.go` runs on every `Open()` to detect and repair outdated backend configs. Any change to `DefaultConfig()` backend values must be mirrored in `fixupBackends()` detection logic, or existing databases will retain stale values.
- **Use incremental vt10x feeding, not full replay.** The agent view's terminal panel uses a persistent `vt10x.Terminal` that receives only new bytes each tick (`renderIncremental`), not the entire 256KB ring buffer. Full replay is O(buffer_size) and causes input lag when the buffer is large. The persistent terminal is reset on resize, task switch, or ring buffer wrap. The full-replay path (`formatTerminalOutput`) is only used for scrollback mode and finished sessions.
- **Sub-view structs that need mutation must be pointers.** Bubble Tea's `Update` uses a value receiver, so helper methods with value receivers (like `scheduleGitRefresh`) get a copy of the model. Mutations to value-type fields inside those helpers are silently lost. Any sub-view struct that is mutated outside the direct `Update` method body must be stored as a pointer (e.g., `gitstatus *GitStatus`, `agentview *AgentView`). Fields that are only read (or only mutated directly in `Update`) can stay as values. Maps (`resolvedDirs`) are already reference types and work correctly.
- **Use `msg.Type` not `msg.String()` for Bubble Tea key matching.** String comparison fails when terminals encode modifier keys differently (e.g., urxvt sends `\x1b[Od` for ctrl+left, which Bubble Tea parses as `KeyCtrlLeft` with `Alt=true` → `String()` returns `"alt+ctrl+left"` instead of `"ctrl+left"`). Always match on `msg.Type` and check `msg.Alt` separately. For agent view pane switching, plain left/right arrows are the primary binding (macOS captures ctrl+left/right for Mission Control), with ctrl+arrow and alt+arrow as fallbacks for cross-terminal compatibility.

## Development Rules

### Testing Requirements

- **Every change must include tests.** When adding new functionality, fixing bugs, or refactoring, always add or update corresponding tests. Run `go test ./...` to verify all tests pass before considering work complete.
- **Run `go test ./... -cover` after writing tests** to verify coverage improved. Aim for ≥80% coverage on any package you touch.
- **Test file placement:** Tests go in `*_test.go` files in the same package (not `_test` suffix packages). Use the existing `testModel(t)`, `testDB(t)`, and similar helpers.
- **What to test:**
  - All exported functions and methods
  - Pure logic functions (parsers, state transitions, builders) — these are easy to test, no excuses
  - View/render functions — verify they return non-empty output and contain expected content strings
  - Edge cases: nil inputs, empty collections, boundary values, error paths
  - State machines: status transitions, cursor navigation, focus cycling
- **What's okay to skip:** Functions requiring a real terminal (raw mode, ioctl), functions that shell out to external processes (git commands, process management), and `cmd/argus/main.go` (entry point)
- **Testing patterns in this codebase:**
  - `db.OpenInMemory()` for database tests (no filesystem needed)
  - `agent.NewRunner(nil)` for UI tests that need a runner but don't start processes
  - `exec.Command("echo", "hello")` or `exec.Command("sleep", "10")` for agent/session tests
  - `DefaultTheme()` for any UI component tests
  - Table-driven tests with `[]struct{ input, expected }` for functions with many cases

## Context Directory

- `context/` stores durable cross-session knowledge checked into git
- `context/knowledge/index.md` is the knowledge graph index — read it when you need project history or domain context
- `context/research/` holds investigation notes and spike results
- `context/plans/` holds strategic plans and proposals

## Planned but Not Yet Implemented

- Git worktree integration (`internal/worktree/`) — Phase 3
- Task import from markdown/JSON (`internal/import/`) — Phase 4
