# Argus

Terminal-native LLM code orchestrator built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

Manage multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and keyboard-driven workflow.

## Features

- **Multi-session agent management** — Run multiple Claude Code / Codex / custom LLM agents simultaneously with PTY-backed sessions
- **Git worktree isolation** — Each task gets its own worktree under `.claude/worktrees/`, with automatic branch creation and cleanup
- **Task lifecycle** — `pending → in_progress → in_review → complete` status workflow with elapsed time tracking
- **Three-panel agent view** — Git status, agent terminal, and file explorer side by side
- **Session persistence** — Agents survive Argus restarts; reattach to running sessions seamlessly
- **ANSI-aware preview** — Live agent output preview with full color support in the task list
- **Tabbed UI** — Switch between Tasks and Projects views
- **Filtering** — Search tasks by name or project
- **Configurable backends** — Define command templates for any LLM CLI tool
- **Customizable keybindings** — Remap every key via SQLite config

## Install

```bash
go install github.com/drn/argus/cmd/argus@latest
```

## Usage

```bash
argus
```

### Keybindings

#### Task List

| Key | Action |
|-----|--------|
| `n` | New task |
| `Enter` | Attach to agent |
| `s` / `S` | Advance / revert status |
| `d` | Delete task |
| `ctrl+d` | Destroy task (kill agent + remove worktree + delete branch) |
| `ctrl+r` | Prune completed tasks |
| `p` | View prompt |
| `w` | Worktree info |
| `/` | Filter tasks |
| `j` / `k` | Navigate up/down |
| `1` / `2` | Switch tabs (Tasks / Projects) |
| `?` | Help |
| `q` | Quit |

#### Agent View

| Key | Action |
|-----|--------|
| `ctrl+q` | Detach from agent |
| `ctrl+←` / `ctrl+→` | Switch panels |

## Data

All state (tasks, projects, backends, keybindings, UI settings) is persisted in SQLite at `~/.argus/data.sql`.
