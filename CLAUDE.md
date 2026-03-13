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

- `cmd/argus/main.go` — Entry point. Loads config, loads task store, starts `tea.Program` with alt screen.
- `internal/ui/root.go` — **Top-level Bubble Tea model**. Owns all sub-views and routes key events based on current view state (`viewTaskList`, `viewNewTask`, `viewHelp`, `viewPrompt`, `viewConfirmDelete`). This is the orchestration hub.
- `internal/ui/tasklist.go` — Task list with cursor, scrolling, filtering. Not a `tea.Model` itself — it's a plain struct that `root.Model` drives.
- `internal/ui/newtask.go` — New task form using `bubbles/textinput`. Has its own `Update` method but is called by root.
- `internal/model/` — Core domain types. `Task` struct and `Status` enum with `pending → in_progress → in_review → complete` workflow. Status implements `encoding.TextMarshaler` for JSON serialization.
- `internal/store/store.go` — JSON file persistence at `~/.config/argus/tasks.json`. Thread-safe with mutex. Auto-creates config dir on first write.
- `internal/config/config.go` — TOML config from `~/.config/argus/config.toml`. Falls back to defaults if file missing. Defines backends (command templates), projects (repo registry), keybindings, and UI prefs.

**Key pattern:** Sub-views (`TaskList`, `StatusBar`, `HelpView`) are plain structs with `View() string` methods — not independent `tea.Model`s. Only `NewTaskForm` has its own `Update` because it manages text input focus. Root model coordinates everything.

## Config & Persistence

- Config dir: `~/.config/argus/` (respects `XDG_CONFIG_HOME`)
- Config format: TOML (`config.toml`)
- Task persistence: JSON (`tasks.json`)
- Backends are command templates with prompt flag interpolation, not SDK integrations

## Planned but Not Yet Implemented

- Agent runner with PTY management (`internal/agent/`) — Phase 2
- Git worktree integration (`internal/worktree/`) — Phase 3
- Task import from markdown/JSON (`internal/import/`) — Phase 4
