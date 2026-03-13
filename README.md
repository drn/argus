# Argus

Terminal-native LLM code orchestrator built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

Manage multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and keyboard-driven workflow.

## Install

```bash
go install github.com/darrencheng/argus/cmd/argus@latest
```

## Usage

```bash
argus
```

### Keybindings

| Key | Action |
|-----|--------|
| `n` | New task |
| `Enter` | Attach to agent |
| `s` / `S` | Advance / revert status |
| `d` | Delete task |
| `p` | View prompt |
| `j` / `k` | Navigate up/down |
| `?` | Help |
| `q` | Quit |

## Configuration

Copy `config.example.toml` to `~/.config/argus/config.toml` and edit to your setup.

Tasks are persisted in `~/.config/argus/tasks.json`.
