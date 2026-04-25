<p align="center"><img src="favicon.svg" width="120"></p>

# Argus

Every agent at a glance. Built with [tcell](https://github.com/gdamore/tcell) and [tview](https://github.com/rivo/tview).

A terminal-native LLM code orchestrator. Manage multiple Claude Code / Codex sessions with task tracking, git worktree isolation, and keyboard-driven workflow.

## Screenshots

<p align="center">
  <img src="screenshots/splash.png" width="720" alt="Splash screen">
</p>

<p align="center">
  <img src="screenshots/task-list.png" width="720" alt="Task list with project folders and live preview">
</p>

<p align="center">
  <img src="screenshots/new-task.png" width="720" alt="New task form with project, branch, and backend selection">
</p>

<p align="center">
  <img src="screenshots/agent-view.png" width="720" alt="Agent view with terminal, git status, and file explorer">
</p>

<p align="center">
  <img src="screenshots/file-diff.png" width="720" alt="Inline diff viewer with syntax highlighting">
</p>

<p align="center">
  <img src="screenshots/settings.png" width="720" alt="Settings tab">
</p>

## Features

### Agent Management

- **Multi-session orchestration** — Run multiple Claude Code / Codex / custom LLM agents simultaneously with PTY-backed terminal sessions
- **Persistent daemon** — Agent sessions survive TUI restarts via a background daemon that keeps PTY fds alive. Auto-starts on launch, graceful shutdown on exit. Similar to tmux, but purpose-built for agent workflows
- **Session resume** — `--resume` for Claude Code, `codex resume <session-id>` for Codex — conversations survive daemon restarts
- **Configurable backends** — Define command templates for any LLM CLI tool. Per-backend flags, prompt interpolation, and plan mode defaults
- **Skill autocomplete** — `/` anywhere in the prompt field triggers autocomplete from `~/.claude/skills/`, per-project skill directories, and installed Claude Code plugins (plugin items appear as `<plugin>:<name>`, e.g. `/cortex:review`). `$` triggers the same for Codex backends. Select with Enter or Tab
- **Agent forking** — Duplicate a running or finished task with full context (source info, recent output, git diff) injected into the new agent's worktree

### Task Workflow

- **Task lifecycle** — `pending → in_progress → in_review → complete` with elapsed time tracking, archiving, and batch pruning
- **Collapsible project folders** — Tasks grouped by project with auto-expand/collapse. Archive section at the bottom for completed work
- **Live preview** — ANSI-aware agent output preview in the task list, rendered from the PTY ring buffer
- **Idle detection** — Tasks waiting for input are visually promoted to "in review" status until visited

### Obsidian Integration

- **To Dos tab** — Browse an Obsidian vault as a task inbox. Three-panel view: note list, markdown preview, and metadata
- **Auto-launch from vault** — Select a note, pick a project, optionally add a prompt, and launch it as a new agent task. The note content becomes the agent's instructions
- **Task-note linking** — Each launched task tracks its source vault file. Status badges (○ pending, ● running,  review, ✓ done) show progress inline
- **Vault auto-create** — When enabled, the daemon watches the vault directory for new `.md` files and automatically creates and starts agent tasks. Share a note to Obsidian from your phone, and the agent starts working
- **Cleanup** — `ctrl+r` on the To Dos tab deletes vault files for completed tasks, keeping the inbox clean
- **Knowledge base** — A separate FTS5-powered full-text search store indexes another Obsidian vault and exposes it as an MCP server (port 7742), auto-injected into every agent worktree

### Remote Control

- **HTTP REST API** — Full task management API on port 7743 (configurable). Tasks (create, list, get, stop, resume, delete, archive, unarchive, rename, fork, set-status), sessions (stop-all), projects + backends CRUD, git status/diff/files per worktree. Bearer token authentication
- **Real terminal in the browser** — xterm.js + fit-addon vendored locally (no CDN). Live SSE byte stream → terminal grid, keystrokes forwarded to PTY, PTY auto-resizes on rotation. Drop-in replacement for the previous polling-based output viewer
- **Mobile-first PWA** — Installable to home screen on iOS/Android. Manifest, service worker (cache-first shell, network-only API), apple-touch-icon, theme color
- **Virtual key row** — Phone-friendly keys above the soft keyboard: Esc, Tab, Ctrl, ↑↓←→, Ctrl+C, Enter, plus pinch-friendly font size controls. Sticky Ctrl modifier (tap = arm, second tap = lock)
- **Web Push notifications** — VAPID-signed push when an agent goes idle ("needs attention" / "ready for review"). Throttled to 1 push per task per 5 min. Per-device subscriptions, masked endpoints, expired subs auto-pruned
- **Per-device API tokens** — Master token mints labeled per-device tokens. Tokens hashed with SHA-256, plaintext shown once at mint. Revocable from the Settings tab. Master required to mint new tokens
- **Tailscale-friendly** — API binds `0.0.0.0` for access over Tailscale mesh VPN. No public exposure needed

### Git & Worktrees

- **Worktree isolation** — Each task gets its own worktree at `~/.argus/worktrees/<project>/<task>` with automatic `argus/<task>` branch creation
- **Three-panel agent view** — Git status, agent terminal, and file explorer side by side
- **Inline diff viewer** — Split and unified diff views with syntax highlighting. Navigate files with arrow keys, scroll diffs with `j`/`k`
- **PR URL detection** — Automatically detects PR URLs from agent output. Open in browser with `ctrl+p` or `o`
- **Worktree cleanup** — Destroy tasks to remove worktree, delete local and remote branches in one step

### PR Reviews

- **Review dashboard** — Browse open PRs and review requests across configured repos
- **Diff viewer** — Syntax-highlighted diffs with file navigation
- **Inline actions** — Approve, request changes, or leave line comments directly from the TUI

### Sandbox

- **macOS sandbox-exec** — SBPL profiles for filesystem and credential isolation per agent session
- **Credential protection** — Blocks reads to `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud` by default
- **Per-project config** — Global and per-project sandbox settings with deny-read and extra-write path overrides

### Terminal & Rendering

- **Full PTY emulation** — x/vt terminal emulator with direct cell painting to tcell. Supports colors, attributes (bold, faint, italic, strikethrough), underline styles, and OSC 8 hyperlinks
- **Infinite scrollback** — Live scrollback reads from session log files; ring buffer provides fast follow-tail
- **Bracket paste** — Large text pastes delivered as a single event, not thousands of keystrokes
- **Keyboard scroll acceleration** — Hold Shift+Up/Down for progressive scroll speed

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
| `n` | New task (with skill autocomplete in prompt field) |
| `Enter` | Open agent view |
| `ctrl+f` | Fork task (duplicate with context) |
| `s` / `S` | Advance / revert status |
| `a` | Toggle archive |
| `w` | Toggle "Waiting for Review" (own section above Archive) |
| `p` | Open PR in browser |
| `c` | Copy task prompt to clipboard |
| `ctrl+d` | Destroy task (kill agent + remove worktree + delete branch) |
| `ctrl+r` | Prune completed tasks |
| `j` / `k` | Navigate up/down |
| `1` / `2` / `3` / `4` | Switch tabs (Tasks / To Dos / Reviews / Settings) |
| `ctrl+l` | Refresh screen (wipe ghost cells; works in every non-agent tab) |
| `q` | Quit |

#### Agent View

| Key | Action |
|-----|--------|
| `ctrl+q` / `Esc` | Back (3-level: diff → files → task list) |
| `Cmd+←` / `Cmd+→` | Switch panels |
| `Cmd+↑` / `Cmd+↓` | Navigate between tasks |
| `ctrl+p` | Open PR in browser |
| `ctrl+l` | Open link picker (fuzzy search all session URLs) |
| `o` | Open PR in browser (when session is finished) |
| `Shift+↑` / `Shift+↓` | Scroll terminal (with acceleration) |

#### File Panel

| Key | Action |
|-----|--------|
| `Enter` | Open diff |
| `s` | Toggle split/unified diff |
| `o` | Reveal in Finder |
| `e` | Open in editor |
| `t` | Open terminal in worktree |

#### To Dos

| Key | Action |
|-----|--------|
| `Enter` | Launch note as task |
| `j` / `k` | Navigate notes |
| `R` | Refresh vault |
| `ctrl+r` | Clean up completed notes |

#### Modals & Forms

| Key | Action |
|-----|--------|
| `Esc` / `ctrl+q` | Close / cancel |
| `Enter` | Confirm / submit |
| `Tab` / `Shift+Tab` | Navigate fields |

#### Reviews

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate PRs |
| `R` | Refresh PR list |
| `a` | Approve PR |
| `r` | Request changes |
| `c` | Line comment |

#### Settings

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate rows |
| `n` | New project / backend |
| `e` | Edit project / backend |
| `d` | Delete project / set default backend |
| `i` | Quick add projects |
| `Enter` / `◀` / `▶` | Toggle / cycle settings |

## Sandbox

Argus can run agent processes inside macOS `sandbox-exec` for filesystem and credential isolation. Each agent session gets an SBPL profile that restricts reads and writes.

### Configuration

Global sandbox settings are managed in the **Settings tab** (`4` key):

| Setting | Description |
|---------|-------------|
| Enabled | Master toggle — applies to all projects by default |
| Deny Read | Extra paths to block reads from (comma-separated) |
| Extra Write | Extra paths to allow writes to (comma-separated) |

Per-project overrides are set in the **project form** (`e` on a project in Settings):

| Setting | Options |
|---------|---------|
| Sandbox | **Inherit** (use global), **Enabled**, or **Disabled** |

Per-project deny-read and extra-write paths are appended to the global lists.

### Built-in defaults

**Filesystem (always denied read):**
- `~/.ssh`, `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud`

**Filesystem (always allowed write):**
- The task's worktree directory
- `/tmp`, `/var/folders`
- `~/.claude.json`, `~/.claude/`
- The main repo's `.git` dir (for worktree git operations)

## Spinner Styles

The in-progress task indicator uses an animated spinner. Cycle through styles in the **Settings tab** using `Enter` or `◀`/`▶` on the **Spinner** row:

| Style | Frames | Speed |
|-------|--------|-------|
| **Progress** (default) | Nerd Font progress icons | 100ms |
| **Dots** | Braille dots `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏` | 100ms |
| **Braille** | Braille pattern `⣷⣯⣟⡿⢿⣻⣽⣾` | 100ms |
| **Classic** | ASCII `\|/-\\` | 150ms |

## Knowledge Base

Argus includes a built-in FTS5 full-text search store that indexes Obsidian vault markdown files. The KB is exposed as an MCP server (port 7742) and auto-injected into every agent worktree, giving agents access to your notes and documentation.

Configure vault paths in the **Settings tab** under the KB section.

### MCP Tools

The MCP server exposes the following tools to connected agents:

**Knowledge Base:**
| Tool | Description |
|------|-------------|
| `kb_search` | Full-text search with ranked results and snippets |
| `kb_read` | Read full document content by vault-relative path |
| `kb_list` | List documents with optional path prefix filtering |
| `kb_ingest` | Add or update a document in the knowledge base |

**Task Management** (allows agents to orchestrate other agents):
| Tool | Description |
|------|-------------|
| `task_create` | Create a task with worktree and start an agent. Params: `name`, `prompt`, `project` |
| `task_list` | List tasks, filtered by `status` and/or `project` |
| `task_get` | Get task details by `id` |
| `task_stop` | Stop a running agent (moves task to "in review") |
| `task_archive` | Archive or unarchive a task. Pass `cwd` (from the agent's `pwd`) to resolve the task by worktree, or `id`. Omit `archived` to toggle. |

Task management tools enable an external agent (e.g. Claude Code running in another terminal) to programmatically launch and monitor Argus tasks via MCP. A sample `/archive` skill lives at `.claude/skills/archive/SKILL.md` — it calls `task_archive` with the current working directory so an agent can mark its own task done at the end of a session.

## Remote Control

Argus includes a built-in HTTP API and mobile web dashboard for controlling agents from your phone or any device on your network.

### Setup

1. Enable in the **Settings tab** (`4` key) under **Remote API** — toggle to Enabled
2. Restart the daemon (Settings → Restart Daemon) for the API server to start
3. The API token is auto-generated at `~/.argus/api-token`

### Web Dashboard

Open `http://<your-machine>:7743/` in your phone browser. Enter the API token from `~/.argus/api-token` to authenticate. Tap **Add to Home Screen** in Safari to install as a PWA.

The dashboard provides:
- **Task list** — Active and Archived tabs, status badges, Stop-all action
- **Task detail** — Real xterm.js terminal with live SSE byte stream, virtual key row, Stop / Resume / Rename / Fork / Archive / Delete actions
- **Create tasks** — Select a project, enter a prompt, start a new agent. Skill autocomplete (type `/`) suggests per-project and global skills
- **Settings tab** — Push notifications toggle (VAPID), test push button, API token management (mint/revoke per-device tokens), forget local token

The local token persists in `localStorage` until you clear it or tap **Forget token**.

### REST API

All endpoints require auth — either `Authorization: Bearer <token>` header or `?token=<token>` query param (the latter is required for `EventSource`/SSE because browsers cannot set headers on it). The token can be the master token from `~/.argus/api-token` or any non-revoked device token.

#### Tasks

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/status` | Running/idle session counts, task counts by status |
| `GET` | `/api/tasks` | List tasks. Filters: `?status=`, `?project=`, `?archived=1` (or `=all`) |
| `POST` | `/api/tasks` | Create and start a task. Body: `{"name":"...", "prompt":"...", "project":"..."}` |
| `GET` | `/api/tasks/{id}` | Get single task detail (includes `archived`, `worktree_path`, `prompt`) |
| `POST` | `/api/tasks/{id}/stop` | Stop a running agent (moves to `in_review`) |
| `POST` | `/api/tasks/{id}/resume` | Resume a stopped agent |
| `DELETE` | `/api/tasks/{id}` | Delete a task |
| `POST` | `/api/tasks/{id}/archive` | Archive (hidden from default list) |
| `POST` | `/api/tasks/{id}/unarchive` | Restore from archive |
| `POST` | `/api/tasks/{id}/rename` | `{"name":"..."}` |
| `POST` | `/api/tasks/{id}/fork` | Clone to a new task. Body: `{"name?":"...", "prompt?":"...", "project?":"..."}` |
| `POST` | `/api/tasks/{id}/status` | Set status. Body: `{"status":"in_review"\|"complete"\|"pending"\|"in_progress"}` |

#### Sessions / terminal

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/tasks/{id}/output` | Recent output (text). Optional `?bytes=`, `?clean=1` |
| `POST` | `/api/tasks/{id}/input` | Send raw bytes to PTY stdin |
| `GET` | `/api/tasks/{id}/stream` | SSE stream of live output (base64-encoded chunks) |
| `GET` | `/api/tasks/{id}/size` | Current PTY dimensions: `{cols, rows}` |
| `POST` | `/api/tasks/{id}/resize` | Resize PTY: `{"cols":N,"rows":M}` |
| `POST` | `/api/sessions/stop-all` | Stop every running session |

#### Git status / diff / files

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/tasks/{id}/git/status` | git status output + branch diff for the task's worktree |
| `GET` | `/api/tasks/{id}/git/diff?path=<file>` | Unified diff for a single file |
| `GET` | `/api/tasks/{id}/files?dir=<rel>` | Worktree file listing |

#### Projects & backends (full CRUD)

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/projects` | List project names |
| `GET` | `/api/projects/full` | List with path, branch, default_backend |
| `POST` | `/api/projects` | Create. Body: `{"name", "path", "branch?", "backend?"}` |
| `PUT` | `/api/projects/{name}` | Update |
| `DELETE` | `/api/projects/{name}` | Delete |
| `GET` | `/api/backends` | List with command + prompt_flag |
| `POST` | `/api/backends` | Create |
| `PUT` | `/api/backends/{name}` | Update |
| `DELETE` | `/api/backends/{name}` | Delete |
| `GET` | `/api/skills` | Skill autocomplete. Filter: `?project=`, `?prefix=` |

#### Push notifications (Web Push, VAPID)

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/push/vapid-public-key` | VAPID public key (urlsafe base64) for `pushManager.subscribe()` |
| `POST` | `/api/push/subscribe` | Register a subscription. Body: `{"label","endpoint","keys":{"p256dh","auth"}}` |
| `GET` | `/api/push/subscriptions` | List with masked endpoints |
| `DELETE` | `/api/push/subscribe/{id}` | Unsubscribe |
| `POST` | `/api/push/test` | Fan out a test notification to every device |

The daemon polls running sessions every 5s; when a session transitions to idle, every subscription receives a notification (throttled to 1 per task per 5 min). Subscriptions returning `410 Gone` are auto-pruned.

#### Per-device API tokens

| Method | Endpoint | Description |
|--------|----------|-------------|
| `GET` | `/api/tokens` | List tokens with last-4 + label (master only? no — any token can list) |
| `POST` | `/api/tokens` | Mint a new device token. **Master token required.** Body: `{"label":"My iPhone"}` → `{"id","label","token"}` (plaintext shown once) |
| `DELETE` | `/api/tokens/{id}` | Revoke. **Master token required.** |

Tokens are stored as SHA-256 hashes; plaintext is never persisted on the server.

### Tailscale Access

For secure remote access without exposing ports to the internet:

1. Install [Tailscale](https://tailscale.com) on your machine and phone
2. Enable the API in Argus Settings
3. Access the dashboard at `http://<tailscale-ip>:7743/` from your phone

### Vault Auto-Create

When **Task Sync** is enabled in Settings (under Knowledge Base), the daemon watches your Obsidian vault for new `.md` files and automatically creates agent tasks from them.

1. Enable **Task Sync** in Settings
2. Set your **ToDo Project** (the default project for auto-created tasks)
3. Share a note to your Obsidian vault from your phone (via iOS Share Sheet or any sync method)
4. The daemon detects the new file, creates a worktree, and starts an agent with the note content as the prompt

Files are debounced (500ms) to handle iCloud sync latency. Duplicate detection prevents re-creating tasks for files that already have linked tasks.

### Auto-Start ToDos

When **Auto-Start ToDos** is enabled (press `a` on the Knowledge Base row in Settings), the daemon polls the vault directory on a configurable interval (default: every 2 minutes) and automatically creates and starts tasks for any new `.md` files found. This replaces the fsnotify-based watcher with a more reliable polling approach.

The poll interval can be configured via `kb.auto_start_interval` in the database (value in seconds). Enabling auto-start also implicitly enables Task Sync.

## Data

All state (tasks, projects, backends, keybindings, UI settings, KB index) is persisted in SQLite at `~/.argus/data.sql`.
