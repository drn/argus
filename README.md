<p align="center"><img src="favicon.svg" width="120"></p>

# Argus

Every agent at a glance. Built with [tcell](https://github.com/gdamore/tcell) and [tview](https://github.com/rivo/tview).

Manage multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and keyboard-driven workflow.

## Features

- **Multi-session agent management** — Run multiple Claude Code / Codex / custom LLM agents simultaneously with PTY-backed sessions
- **Git worktree isolation** — Each task gets its own worktree at `~/.argus/worktrees/<project>/<task>` with automatic branch creation and cleanup
- **Persistent daemon** — Agent sessions survive TUI restarts; the daemon keeps PTY fds alive and auto-restarts on failure
- **Task lifecycle** — `pending → in_progress → in_review → complete` status workflow with elapsed time tracking and archiving
- **Three-panel agent view** — Git status, agent terminal, and file explorer side by side with inline diff viewer (split + unified)
- **PR reviews** — Browse open PRs and review requests, view diffs with syntax highlighting, approve/request changes/comment directly from the TUI
- **Knowledge base** — FTS5-powered full-text search over Obsidian vaults, exposed as an MCP server auto-injected into agent worktrees
- **Sandbox** — macOS `sandbox-exec` with SBPL profiles for filesystem and credential isolation
- **Live preview** — ANSI-aware agent output preview in the task list
- **Configurable backends** — Define command templates for any LLM CLI tool
- **Session resume** — `--resume` for Claude Code, `codex resume <uuid>` for Codex — conversations survive daemon restarts

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
| `Enter` | Open agent view |
| `s` / `S` | Advance / revert status |
| `a` | Toggle archive |
| `r` | Rename task |
| `o` | Open PR in browser |
| `d` | Delete task |
| `ctrl+d` | Destroy task (kill agent + remove worktree + delete branch) |
| `ctrl+r` | Prune completed tasks |
| `/` | Filter tasks |
| `j` / `k` | Navigate up/down |
| `1` / `2` / `3` | Switch tabs (Tasks / Reviews / Settings) |
| `q` | Quit |

#### Agent View

| Key | Action |
|-----|--------|
| `ctrl+q` / `Esc` | Back (3-level: diff → files → task list) |
| `Cmd+←` / `Cmd+→` | Switch panels |
| `ctrl+p` | Open PR in browser |
| `Shift+↑` / `Shift+↓` | Scroll terminal |

#### File Panel

| Key | Action |
|-----|--------|
| `Enter` | Open diff |
| `s` | Toggle split/unified diff |
| `o` | Reveal in Finder |
| `e` | Open in editor |
| `t` | Open terminal in worktree |

#### Reviews

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate PRs |
| `R` | Refresh PR list |
| `a` | Approve PR |
| `r` | Request changes |
| `c` | Line comment |

## Sandbox

Argus can run agent processes inside macOS `sandbox-exec` for filesystem and credential isolation. Each agent session gets an SBPL profile that restricts reads and writes.

### Configuration

All sandbox settings are managed in the **Settings tab** (`3` key):

| Setting | Description |
|---------|-------------|
| Enabled | Master toggle (global or per-project) |
| Deny Read | Extra paths to block reads from (comma-separated) |
| Extra Write | Extra paths to allow writes to (comma-separated) |

### Built-in defaults

**Filesystem (always denied read):**
- `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud`

**Filesystem (always allowed write):**
- The task's worktree directory
- `/tmp`, `/var/folders`
- `~/.claude.json`, `~/.claude/`
- The main repo's `.git` dir (for worktree git operations)

## Knowledge Base

Argus includes a built-in FTS5 full-text search store that indexes Obsidian vault markdown files. The KB is exposed as an MCP server (port 7742) and auto-injected into every agent worktree, giving agents access to your notes and documentation.

Configure vault paths in the **Settings tab** under the KB section.

## Data

All state (tasks, projects, backends, keybindings, UI settings, KB index) is persisted in SQLite at `~/.argus/data.sql`.
