<p align="center"><img src="favicon.svg" width="120"></p>

<h1 align="center">Argus</h1>

<p align="center"><em>Every agent at a glance.</em></p>

<p align="center">
  <a href="https://github.com/drn/argus/actions/workflows/ci.yml"><img src="https://github.com/drn/argus/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
</p>

Argus is a terminal-native orchestrator for LLM coding agents. Run a swarm of Claude Code and Codex sessions side by side, each in its own git worktree, all under a single keyboard-driven UI — and reach the same swarm from your phone, from another agent, or from your own notes.

<p align="center">
  <img src="screenshots/task-list.png" width="820" alt="Task list with project folders, live agent preview, and inline git status">
</p>

## Why Argus

Coding agents are cheap to start and expensive to babysit. Five `claude` tabs become five forgotten branches. A `codex` you fire off at lunch is a black box until you `cmd-tab` back. Argus replaces that pile of terminals with a persistent orchestrator that knows what every agent is doing, where its worktree lives, when it goes idle, and who needs your attention next.

- **One keystroke** spins up an isolated worktree, a fresh branch, and a fresh agent, all wired into a live dashboard.
- **A persistent daemon** keeps PTYs alive across TUI restarts and laptop reboots. Your sessions outlive your terminal.
- **An idle detector** quietly promotes any agent waiting for input to "in review" — so a glance at the list tells you who needs you.
- **A built-in HTTP API + PWA** mirrors every keystroke from your phone, so the dashboard travels with you.
- **A built-in MCP server** lets agents talk to Argus directly — search your notes, spawn other agents, or hand off work between models.

## The Three Pillars

### 📱 Mobile Dashboard (PWA)

Argus ships a real, installable Progressive Web App. Tap **Add to Home Screen** in Safari and you have a phone-shaped operations console for your agents — running locally on your machine, reachable over your Tailscale mesh, never exposed to the public internet.

<p align="center">
  <img src="screenshots/agent-view.png" width="820" alt="Agent view with terminal, git status, and file explorer">
</p>

- **Real terminals in the browser** — xterm.js fed by an SSE byte stream, with PTY auto-resize on rotation. Not a polling log viewer.
- **A native compose bar** that catches everything iOS sends — dictation, third-party keyboards, Wispr Flow — and forwards it cleanly into the agent's stdin. Slash-key autocomplete pulls from your `~/.claude/skills/`, per-project skills, and installed plugins.
- **A virtual key bar** with the keys iOS won't give you: Esc, Tab, Shift+Tab (cycle Claude Code modes), arrows. Tap them between dictations without losing the soft keyboard.
- **Web Push notifications** when an agent goes idle. Throttled, VAPID-signed, per-device subscriptions, no third-party push services.
- **Share-sheet target** — Argus shows up natively in the Android share sheet. iOS gets a one-paste Shortcut that does the same. Either way, sharing a URL or a chunk of text into Argus lands you on the New Task tab with the prompt pre-filled.
- **GitHub-style stacked diff view** — every changed file in the worktree as a collapsible panel, expand-all, wrap toggle, optimistic for thumbs.
- **Per-device API tokens** — your iPhone, your iPad, and your laptop each get their own labeled token. Revoke any of them from the dashboard. Master token mints; SHA-256 hashes are all that's stored.
- **Offline-aware** — when the daemon is unreachable (laptop closed, Tailscale off) the PWA flips to a branded offline screen and reconnects automatically.
- **Pure-local** — runs on `localhost`/Tailscale, binds `0.0.0.0` on port 7743, never reaches out to a vendor cloud.

<p align="center">
  <img src="screenshots/file-diff.png" width="820" alt="Inline diff viewer with split and unified views">
</p>

### 🤝 Full MCP Server

Argus exposes itself as a Model Context Protocol server, so any agent can drive Argus the same way you do.

- **Spawn other agents.** An orchestrator agent can call `task_create` to fan work out across worktrees, then watch progress with `task_list` and `task_get`.
- **Hand off cleanly.** When a session is done, the agent calls `task_complete` (status flip) or `task_archive` (out of sight) using its own `pwd` to identify itself — no IDs to track.
- **Schedule itself.** `schedule_create` accepts cron, `@every 30m`, or a one-shot `run_once_at` timestamp. An agent can plant a tomorrow-morning follow-up before signing off.
- **Stage clipboard text** with `argus_clipboard_set` — solves the iOS Safari rule that `clipboard.writeText` requires a synchronous user gesture. The agent stages, you tap **Copy** (PWA) or hit `ctrl+y` (TUI). One tap, no escape-character mangling.
- **Rename, fork, stop, resume** — every TUI verb has an MCP equivalent.

The same MCP server is auto-injected into every worktree Argus creates, so newly-spawned agents inherit the toolset without any per-project config.

### 🧠 Knowledge Base

Argus indexes your Obsidian vault as a SQLite FTS5 store and serves it over MCP. Every agent it spawns sees your notes — your design docs, your meeting captures, your durable preferences — as a first-class lookup, not a copy-paste afterthought.

- **`kb_search`** — ranked full-text search across the entire vault, with snippets.
- **`kb_read`** — full markdown by vault-relative path. Wiki-link friendly.
- **`kb_list`** — directory listing with prefix filtering for path-aware browsing.
- **`kb_ingest`** — agents write their own learnings back. Your KB grows from sessions instead of decaying between them.
- **Live re-indexing** — files dropped into the vault are searchable in seconds.
- **Schema-aware** — YAML frontmatter (title + tags) drives retrieval and clustering.

Pair this with the MCP task tools and an agent can read a meeting note, decide what to build, spawn its own worker tasks, and archive itself when done — all in a single conversation.

## Also In The Box

- **Multi-backend** — Claude Code, Codex, or any LLM CLI as a templated command. Per-backend prompt flags and plan-mode defaults.
- **Worktree isolation** — every task gets `~/.argus/worktrees/<project>/<task>` and an `argus/<task>` branch, all transactionally created and cleaned up.
- **Session resume** — `--resume` on Claude Code, `codex resume <id>` on Codex. Your conversation survives a daemon restart.
- **Agent forking** — duplicate a running task with full context (source info, recent output, git diff) injected into the new worktree.
- **Smart auto-naming** — a Claude Haiku call quietly turns a free-form prompt into a kebab-case task name. Falls open to a regex slug if `claude` is unavailable.
- **Scheduled tasks** — cron, descriptors, intervals, or one-shot runs. Each fire spawns a fresh task. Manage from TUI, PWA, or MCP.
- **PR review dashboard** — open PRs across configured repos, syntax-highlighted diffs, approve / request changes / line-comment from the TUI.
- **macOS sandbox-exec** — per-session SBPL profiles. `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud` blocked by default.
- **Self-update** — `git pull` + `go install` + daemon restart from a single Settings row. Active sessions reattach across the swap.
- **Auto-start at login** — install the daemon as a launchd LaunchAgent so your agents survive reboots without launching the TUI.
- **Full PTY emulation** — `charmbracelet/x/vt` painting cells directly to `tcell`. Colors, attributes, OSC 8 hyperlinks, infinite scrollback, bracket paste.

## Install

```bash
go install github.com/drn/argus/cmd/argus@latest
argus
```

Pure Go, no CGO. SQLite via `modernc.org/sqlite`. Built with [tcell](https://github.com/gdamore/tcell) and [tview](https://github.com/rivo/tview).

```bash
argus daemon install   # macOS — auto-start at login via launchd
```

To open the PWA, enable **Remote API** in Settings, then point your phone at `http://<your-machine>:7743/` and paste the master token from `~/.argus/api-token`. Tailscale recommended.

---

## Reference

The sections below are the dense usage docs — keybindings, REST endpoints, configuration tables. Skim if you're getting started; bookmark if you're already running.

### Keybindings

#### Task List

| Key             | Action                                                          |
| --------------- | --------------------------------------------------------------- |
| `n`             | New task (with skill autocomplete in prompt field)              |
| `Enter`         | Open agent view                                                 |
| `ctrl+f`        | Fork task (duplicate with context)                              |
| `s` / `S`       | Advance / revert status                                         |
| `a`             | Toggle archive                                                  |
| `w`             | Toggle "Waiting for Review" (own section above Archive)         |
| `p`             | Open PR in browser                                              |
| `c`             | Copy task prompt to clipboard                                   |
| `ctrl+d`        | Destroy task (kill agent + remove worktree + delete branch)     |
| `ctrl+r`        | Prune completed tasks                                           |
| `j` / `k`       | Navigate up/down                                                |
| `1` / `2` / `3` | Switch tabs (Tasks / Reviews / Settings)                        |
| `ctrl+l`        | Refresh screen (wipe ghost cells; works in every non-agent tab) |
| `q`             | Quit                                                            |

#### Agent View

| Key                   | Action                                                                    |
| --------------------- | ------------------------------------------------------------------------- |
| `ctrl+q` / `Esc`      | Back (3-level: diff → files → task list)                                  |
| `Cmd+←` / `Cmd+→`     | Switch panels                                                             |
| `Cmd+↑` / `Cmd+↓`     | Navigate between tasks                                                    |
| `ctrl+p`              | Open PR in browser                                                        |
| `ctrl+l`              | Open link picker (fuzzy search all session URLs)                          |
| `ctrl+y`              | Copy agent-staged text (only when payload pending; otherwise sent to PTY) |
| `o`                   | Open PR in browser (when session is finished)                             |
| `Shift+↑` / `Shift+↓` | Scroll terminal (with acceleration)                                       |

#### File Panel

| Key     | Action                    |
| ------- | ------------------------- |
| `Enter` | Open diff                 |
| `s`     | Toggle split/unified diff |
| `o`     | Reveal in Finder          |
| `e`     | Open in editor            |
| `t`     | Open terminal in worktree |

#### Modals & Forms

| Key                 | Action           |
| ------------------- | ---------------- |
| `Esc` / `ctrl+q`    | Close / cancel   |
| `Enter`             | Confirm / submit |
| `Tab` / `Shift+Tab` | Navigate fields  |

#### Reviews

| Key       | Action          |
| --------- | --------------- |
| `j` / `k` | Navigate PRs    |
| `R`       | Refresh PR list |
| `a`       | Approve PR      |
| `r`       | Request changes |
| `c`       | Line comment    |

#### Settings

| Key                   | Action                                                   |
| --------------------- | -------------------------------------------------------- |
| `j` / `k`             | Navigate rows                                            |
| `n`                   | New project / backend / schedule                         |
| `e`                   | Edit project / backend / schedule                        |
| `d`                   | Delete project / set default backend / delete schedule   |
| `t`                   | Toggle schedule enabled (on the Scheduled Tasks section) |
| `r`                   | Run schedule now (on the Scheduled Tasks section)        |
| `i`                   | Quick add projects                                       |
| `Enter` / `◀` / `▶` | Toggle / cycle settings                                  |

### Self-Update

From the **Settings tab** (Status section, when the daemon is connected) the **Source path** row holds the path to your local Argus checkout, and the **Update Argus** row runs `git pull --ff-only` followed by `go install ./...` and then restarts the daemon so the new binary takes over. Active sessions reattach across the restart. The same controls are exposed in the web UI under **Settings → Argus update** (master token only).

### Auto-start at Login (macOS)

Toggle from **Settings → Status → Auto-start at login** (Enter), or use the CLI:

```bash
argus daemon install     # write ~/Library/LaunchAgents/com.drn.argus.daemon.plist and bootstrap into launchd
argus daemon uninstall   # bootout and remove the plist
argus daemon status      # show plist path + installed/loaded state
```

The plist is configured with `RunAtLoad` and `KeepAlive { SuccessfulExit = false }`, which means launchd starts the daemon at login and restarts it if it crashes (non-zero exit) — but a clean `argus daemon stop` is honored and won't trigger a respawn. Stdout/stderr are written to `~/.argus/launchd.log`. The plist points at `~/.argus/argusd`, a symlink to the resolved argus binary; reinstalling rewrites the symlink so launchd picks up the new binary on next start. macOS only — Linux/Windows show no toggle.

### Sandbox

Argus can run agent processes inside macOS `sandbox-exec` for filesystem and credential isolation. Each agent session gets an SBPL profile that restricts reads and writes.

Global sandbox settings are managed in the **Settings tab** (`4` key):

| Setting     | Description                                        |
| ----------- | -------------------------------------------------- |
| Enabled     | Master toggle — applies to all projects by default |
| Deny Read   | Extra paths to block reads from (comma-separated)  |
| Extra Write | Extra paths to allow writes to (comma-separated)   |

Per-project overrides are set in the **project form** (`e` on a project in Settings) — **Inherit**, **Enabled**, or **Disabled**. Per-project deny-read and extra-write paths are appended to the global lists.

**Always denied read:** `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud`
**Always allowed write:** the task's worktree directory, `/tmp`, `/var/folders`, `~/.claude.json`, `~/.claude/`, the main repo's `.git` dir.

### Spinner Styles

Cycle through styles in the **Settings tab** using `Enter` or `◀`/`▶` on the **Spinner** row:

| Style                  | Frames                     | Speed |
| ---------------------- | -------------------------- | ----- |
| **Progress** (default) | Nerd Font progress icons   | 100ms |
| **Dots**               | Braille dots `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`  | 100ms |
| **Braille**            | Braille pattern `⣷⣯⣟⡿⢿⣻⣽⣾` | 100ms |
| **Classic**            | ASCII `\|/-\\`             | 150ms |

### MCP Tools

Argus runs an MCP server on port 7742 and auto-injects it into every agent worktree.

**Knowledge Base:**

| Tool         | Description                                          |
| ------------ | ---------------------------------------------------- |
| `kb_search`  | Full-text search with ranked results and snippets    |
| `kb_read`    | Read full document content by vault-relative path    |
| `kb_list`    | List documents with optional path prefix filtering   |
| `kb_ingest`  | Add or update a document in the knowledge base       |
| `kb_delete`  | Remove a document by vault-relative path             |

**Task Management** (lets agents orchestrate other agents):

| Tool            | Description                                                                                                                                     |
| --------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `task_create`   | Create a task with worktree and start an agent. Params: `name`, `prompt`, `project`                                                             |
| `task_list`     | List tasks, filtered by `status` and/or `project`                                                                                               |
| `task_get`      | Get task details by `id`                                                                                                                        |
| `task_stop`     | Stop a running agent (moves task to "in review")                                                                                                |
| `task_archive`  | Archive or unarchive a task. Pass `cwd` (from the agent's `pwd`) to resolve by worktree, or `id`. Omit `archived` to toggle.                    |
| `task_rename`   | Rename a task. Updates only the display name (branch and worktree paths stay locked to the original slug). Pass `cwd` or `id` plus `name`.      |
| `task_complete` | Mark a task as complete (sets status, stamps `EndedAt`). Pass `cwd` or `id`. Does NOT stop a running agent — call `task_stop` first if needed.  |

Sample skills at `.claude/skills/archive/SKILL.md` and `.claude/skills/argus-complete/SKILL.md` let an agent finalize its own task at the end of a session via `cwd` resolution. Completing and archiving are independent axes.

**Schedule Management:**

| Tool                | Description                                                                                                                                                                                           |
| ------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `schedule_list`     | List all schedules with name, project, cron expression, enabled state, next/last fire timestamps                                                                                                      |
| `schedule_create`   | Create. Params: `name`, `project`, `prompt`, plus exactly one of `schedule` (cron or `@every <duration>`) or `run_once_at` (RFC3339 UTC); optional `backend`, `enabled`                               |
| `schedule_update`   | Partial update — pass `id` plus any fields to change. Toggling `enabled`, rotating prompts, or converting between cron and one-shot (set the new field; the other clears automatically).             |
| `schedule_delete`   | Remove a schedule by `id`. Tasks already created by previous fires are unaffected.                                                                                                                    |
| `schedule_run_now`  | Fire a schedule immediately, out of cycle. Bookkeeping is updated so the next regular tick will not double-fire. One-shot rows auto-disable. Does NOT send a push notification — only cron-tick fires do. |

**Agent-Staged Clipboard:**

| Tool                  | Description                                                                                                                                                  |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `argus_clipboard_set` | Stage text for the user to copy with one tap (PWA Copy button) or one keypress (TUI `ctrl+y`). Params: `text` (required), `id` or `cwd`. Last-write-wins, 5-min TTL, 1 MiB max. |

### Remote Control: REST API

All endpoints require auth — `Authorization: Bearer <token>` header or `?token=<token>` query param (the latter is required for `EventSource`/SSE because browsers cannot set headers on it). The token can be the master token from `~/.argus/api-token` or any non-revoked device token.

#### Tasks

| Method   | Endpoint                    | Description                                                                                                                                                                                                                                                                                                                                                                               |
| -------- | --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`    | `/api/status`               | Running/idle session counts, task counts by status                                                                                                                                                                                                                                                                                                                                        |
| `GET`    | `/api/tasks`                | List tasks. Filters: `?status=`, `?project=`, `?archived=1` (or `=all`). Each task carries `idle: true` when `in_progress` but the session is missing or waiting for input.                                                                                                                                                                                                              |
| `POST`   | `/api/tasks`                | Create and start a task. JSON `{"name", "prompt", "project", "backend?"}`, OR `multipart/form-data` with `name`/`prompt`/`project`/`backend` plus `files` parts (uploaded into `<worktree>/.context/`, paths appended to the prompt). Per-file 10MB / total 50MB / 20 files cap.                                                                                                          |
| `GET`    | `/api/tasks/{id}`           | Get single task detail (includes `archived`, `worktree_path`, `prompt`, `idle`)                                                                                                                                                                                                                                                                                                           |
| `POST`   | `/api/tasks/{id}/stop`      | Stop a running agent (moves to `in_review`)                                                                                                                                                                                                                                                                                                                                               |
| `POST`   | `/api/tasks/{id}/resume`    | Resume a stopped agent                                                                                                                                                                                                                                                                                                                                                                    |
| `DELETE` | `/api/tasks/{id}`           | Delete a task                                                                                                                                                                                                                                                                                                                                                                             |
| `POST`   | `/api/tasks/{id}/archive`   | Archive (hidden from default list)                                                                                                                                                                                                                                                                                                                                                        |
| `POST`   | `/api/tasks/{id}/unarchive` | Restore from archive                                                                                                                                                                                                                                                                                                                                                                      |
| `POST`   | `/api/tasks/{id}/rename`    | `{"name":"..."}`                                                                                                                                                                                                                                                                                                                                                                          |
| `POST`   | `/api/tasks/{id}/fork`      | Clone to a new task. Body: `{"name?", "prompt?", "project?"}`                                                                                                                                                                                                                                                                                                                             |
| `POST`   | `/api/tasks/{id}/status`    | Set status. Body: `{"status":"in_review"\|"complete"\|"pending"\|"in_progress"}`                                                                                                                                                                                                                                                                                                          |

#### Sessions / terminal

| Method | Endpoint                 | Description                                                                                                                                                                                             |
| ------ | ------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/tasks/{id}/output` | Recent output (text). Optional `?bytes=`, `?clean=1`                                                                                                                                                    |
| `GET`  | `/api/tasks/{id}/links`  | Extract http/https URLs from terminal output. Returns `{"links":[{"label","url"}]}`. Powers the PWA's "Open link" overflow item.                                                                       |
| `POST` | `/api/tasks/{id}/input`  | Send raw bytes to PTY stdin                                                                                                                                                                             |
| `POST` | `/api/tasks/{id}/upload` | Upload files mid-session. `multipart/form-data` with `files` parts; saved to `<worktree>/.context/<name>` (auto-suffixed on collision) and returns `{paths:[]}`. Same 10MB/50MB/20-file caps as create. |
| `GET`  | `/api/tasks/{id}/stream` | SSE stream of live output (base64-encoded chunks)                                                                                                                                                       |
| `GET`  | `/api/tasks/{id}/size`   | Current PTY dimensions: `{cols, rows}`                                                                                                                                                                  |
| `POST` | `/api/tasks/{id}/resize` | Resize PTY: `{"cols":N,"rows":M}`                                                                                                                                                                       |
| `POST` | `/api/sessions/stop-all` | Stop every running session                                                                                                                                                                              |

#### Git status / diff / files

| Method | Endpoint                               | Description                                             |
| ------ | -------------------------------------- | ------------------------------------------------------- |
| `GET`  | `/api/tasks/{id}/git/status`           | git status output + branch diff for the task's worktree |
| `GET`  | `/api/tasks/{id}/git/diff?path=<file>` | Unified diff for a single file                          |
| `GET`  | `/api/tasks/{id}/files?dir=<rel>`      | Worktree file listing                                   |

#### Projects & backends (full CRUD)

| Method   | Endpoint               | Description                                                                                              |
| -------- | ---------------------- | -------------------------------------------------------------------------------------------------------- |
| `GET`    | `/api/projects`        | List project names                                                                                       |
| `GET`    | `/api/projects/full`   | List with path, branch, default_backend                                                                  |
| `POST`   | `/api/projects`        | Create. Body: `{"name", "path", "branch?", "backend?", "sandbox?"}` where `sandbox` is `{"enabled": true\|false\|null, "deny_read":[], "extra_write":[]}` (`null` = inherit global) |
| `PUT`    | `/api/projects/{name}` | Update                                                                                                   |
| `DELETE` | `/api/projects/{name}` | Delete                                                                                                   |
| `GET`    | `/api/backends`        | List with command + prompt_flag                                                                          |
| `POST`   | `/api/backends`        | Create                                                                                                   |
| `PUT`    | `/api/backends/{name}` | Update                                                                                                   |
| `DELETE` | `/api/backends/{name}` | Delete                                                                                                   |
| `GET`    | `/api/skills`          | Skill autocomplete. Filter: `?project=`, `?filter=` (case-insensitive substring)                         |

#### Push notifications (Web Push, VAPID)

| Method   | Endpoint                     | Description                                                                    |
| -------- | ---------------------------- | ------------------------------------------------------------------------------ |
| `GET`    | `/api/push/vapid-public-key` | VAPID public key (urlsafe base64) for `pushManager.subscribe()`                |
| `POST`   | `/api/push/subscribe`        | Register a subscription. Body: `{"label","endpoint","keys":{"p256dh","auth"}}` |
| `GET`    | `/api/push/subscriptions`    | List with masked endpoints                                                     |
| `DELETE` | `/api/push/subscribe/{id}`   | Unsubscribe                                                                    |
| `POST`   | `/api/push/test`             | Fan out a test notification to every device                                    |

The daemon polls running sessions every 5s; when a session transitions to idle, every subscription receives a notification (throttled to 1 per task per 5 min). Subscriptions returning `410 Gone` are auto-pruned.

#### Per-device API tokens

| Method   | Endpoint           | Description                                                                                                                         |
| -------- | ------------------ | ----------------------------------------------------------------------------------------------------------------------------------- |
| `GET`    | `/api/tokens`      | List tokens with last-4 + label                                                                                                     |
| `POST`   | `/api/tokens`      | Mint a new device token. **Master token required.** Body: `{"label":"My iPhone"}` → `{"id","label","token"}` (plaintext shown once) |
| `DELETE` | `/api/tokens/{id}` | Revoke. **Master token required.**                                                                                                  |

Tokens are stored as SHA-256 hashes; plaintext is never persisted on the server.

#### Scheduled tasks

| Method   | Endpoint                  | Description                                                                                                                                           |
| -------- | ------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`    | `/api/schedules`          | List schedules with `next_run_at`, `last_run_at`, `last_task_id`, `last_error`. **Master token required** (prompts can carry sensitive instructions). |
| `POST`   | `/api/schedules`          | Create. Body: `{"name","project","prompt","schedule","backend?","enabled"}`. **Master token required.** Returns the created row.                      |
| `PUT`    | `/api/schedules/{id}`     | Partial update — every field optional. Useful for toggling `enabled`. **Master token required.**                                                      |
| `DELETE` | `/api/schedules/{id}`     | Remove. Tasks already created by the schedule are not affected. **Master token required.**                                                            |
| `POST`   | `/api/schedules/{id}/run` | Fire the schedule now, regardless of cron timing. Returns `{"task_id"}`. **Master token required.**                                                   |

Schedule expressions accept the standard 5-field cron syntax (e.g. `0 9 * * 1-5`), descriptors (`@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`), and intervals (`@every 30m`).

#### Settings & logs (master only for mutations)

| Method | Endpoint                         | Description                                                                                                                                  |
| ------ | -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/settings`                  | Returns sandbox / KB / API / defaults config plus `sandbox.available` (whether `sandbox-exec` is on this host). Device tokens may read.      |
| `PUT`  | `/api/settings`                  | Partial update — every section is optional. Body: `{"sandbox":{...}, "kb":{...}, "api":{...}, "defaults":{...}}`. **Master token required.** |
| `GET`  | `/api/logs/{ux\|daemon}?bytes=N` | Tail the last N bytes of the log (default 64K, max 1M). Missing files return `200` with empty body.                                          |

### Keep the host awake

The daemon runs as a normal process on the host machine. When the host sleeps, HTTP responses stall, SSE streams disconnect, and push notifications stop firing. PTY sessions pause where they were and resume when the host wakes.

For a clamshell-mode laptop driving an external display:

- Use `caffeinate -is` (no `-d`) or [KeepingYouAwake](https://github.com/newmarcel/KeepingYouAwake) with **Allow display sleep** enabled — keeps system + idle awake while letting the display sleep.
- For a permanent setup on AC power: `sudo pmset -c sleep 0 disablesleep 1 displaysleep 1`.
- Sleeping the external display via `pmset displaysleepnow` (or a hot corner) is fine; physically disconnecting it will sleep the Mac because the lid is closed.

### Tailscale Access

For secure remote access without exposing ports to the internet:

1. Install [Tailscale](https://tailscale.com) on your machine and phone
2. Enable the API in Argus Settings
3. Access the dashboard at `http://<tailscale-ip>:7743/` from your phone

When the PWA cannot reach the API — daemon stopped, host asleep, or Tailscale off — it flips to an offline screen with the Argus banner and a Tailscale reminder, then auto-reconnects once the daemon is reachable again.

### Data

All state (tasks, projects, backends, keybindings, UI settings, KB index) is persisted in SQLite at `~/.argus/data.sql`.
