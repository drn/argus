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

- `cmd/argus/main.go` — Entry point. Loads config, loads task store, creates agent runner, starts `tea.Program` with alt screen.
- `internal/ui/root.go` — **Top-level Bubble Tea model**. Owns all sub-views and routes key events based on current view state (`viewTaskList`, `viewNewTask`, `viewHelp`, `viewPrompt`, `viewConfirmDelete`). This is the orchestration hub.
- `internal/ui/tasklist.go` — Task list with cursor, scrolling, filtering. Not a `tea.Model` itself — it's a plain struct that `root.Model` drives.
- `internal/ui/newtask.go` — New task form using `bubbles/textinput`. Has its own `Update` method but is called by root.
- `internal/model/` — Core domain types. `Task` struct and `Status` enum with `pending → in_progress → in_review → complete` workflow. Status implements `encoding.TextMarshaler` for JSON serialization.
- `internal/store/store.go` — JSON file persistence at `~/.config/argus/tasks.json`. Thread-safe with mutex. Auto-creates config dir on first write.
- `internal/config/config.go` — TOML config from `~/.config/argus/config.toml`. Falls back to defaults if file missing. Defines backends (command templates), projects (repo registry), keybindings, and UI prefs.
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

- Config dir: `~/.config/argus/` (respects `XDG_CONFIG_HOME`)
- Config format: TOML (`config.toml`)
- Task persistence: JSON (`tasks.json`)
- Backends are command templates with prompt flag interpolation, not SDK integrations

## Key Learnings

- PTY child processes need a real terminal size at launch (`pty.StartWithSize`), not 0x0. TUI apps like claude won't render with zero dimensions.
- Use `charmbracelet/x/term` for raw mode (cross-platform) instead of platform-specific ioctls (`TIOCGETA` is macOS-only, `TCGETS` is Linux-only).
- `tea.ExecCommand.SetStdin/SetStdout` must capture and use Bubble Tea's `p.input`/`p.output` — don't hardcode `os.Stdin`/`os.Stdout`.
- The single-reader-tee pattern (one goroutine reads PTY, tees to buffer + optional writer) is critical. Two goroutines reading the same fd causes data loss.

## Planned but Not Yet Implemented

- Git worktree integration (`internal/worktree/`) — Phase 3
- Task import from markdown/JSON (`internal/import/`) — Phase 4
