<p align="center"><img src="favicon.svg" width="120"></p>

<h1 align="center">Argus</h1>

<p align="center"><em>Every agent at a glance.</em></p>

<p align="center">
  <a href="https://github.com/drn/argus/actions/workflows/ci.yml"><img src="https://github.com/drn/argus/actions/workflows/ci.yml/badge.svg" alt="CI"></a>
</p>

Argus is a terminal-native orchestrator for LLM coding agents. Run a swarm of agents — Claude Code, Codex, or any LLM CLI, cloud or local — side by side, each in its own git worktree, all under a single keyboard-driven UI — and reach the same swarm from your phone, from another laptop, from another agent, or from your own notes.

<p align="center">
  <img src="screenshots/task-list.png" width="405" alt="Task list with project folders, live agent preview, and inline git status">
  &nbsp;
  <img src="screenshots/file-diff.png" width="405" alt="Inline diff viewer with split and unified views">
</p>

## Why Argus

Coding agents are cheap to start and expensive to babysit. Five `claude` tabs become five forgotten branches. A `codex` you fire off at lunch is a black box until you `cmd-tab` back. Argus replaces that pile of terminals with a persistent orchestrator that knows what every agent is doing, where its worktree lives, when it goes idle, and who needs your attention next.

- **One keystroke** spins up an isolated worktree, a fresh branch, and a fresh agent, all wired into a live dashboard.
- **Native multi-agent coordination.** A dedicated **Hera** tab turns one agent into a team: a coordinator delegates to workers it spawns, an idle-gated message bus passes work between them, and an orchestration tree shows the whole team at a glance — all first-class in the same UI, no separate tool.
- **A persistent daemon** keeps PTYs alive across TUI restarts and laptop reboots — and a separate session-supervisor keeps them alive across *daemon* restarts too, so you can upgrade Argus mid-flight without interrupting a single agent. Your sessions outlive your terminal.
- **An idle detector** quietly promotes any agent waiting for input to "in review" — so a glance at the list tells you who needs you.
- **A built-in HTTP API + PWA** mirrors every keystroke from your phone, so the dashboard travels with you.
- **A built-in MCP server** lets agents talk to Argus directly — search your notes, spawn other agents, or hand off work between models.
- **Harness- and model-agnostic by design.** Argus orchestrates the workflow, not a single tool. Every backend is just a templated command, so the same worktree → branch → review → notify loop is identical whether the agent underneath is Claude Code, Codex, opencode, or a local model via ollama — pick the harness and model per task, keep one standardized workflow across all of them.

## The Three Pillars

### 📱 Mobile Dashboard (PWA)

Argus ships a real, installable Progressive Web App. Tap **Add to Home Screen** in Safari and you have a phone-shaped operations console for your agents — running locally on your machine, reachable over your Tailscale mesh, never exposed to the public internet.

<p align="center">
  <img src="screenshots/pwa-task-list.png" width="200" alt="PWA task list grouped by project with running/idle/done status and PR badges">
  &nbsp;
  <img src="screenshots/pwa-agent-session.png" width="200" alt="PWA agent terminal with live Claude Code output, the compose bar, and the model/effort status line">
  &nbsp;
  <img src="screenshots/pwa-agent-view.png" width="200" alt="PWA agent terminal rendering a full session over SSE">
  &nbsp;
  <img src="screenshots/pwa-new-task.png" width="200" alt="PWA New Task form with project, agent, model, prompt, and file drop">
</p>

- **Real terminals in the browser** — xterm.js fed by an SSE byte stream, with PTY auto-resize on rotation. Not a polling log viewer.
- **A native compose bar** that catches everything iOS sends — dictation, third-party keyboards, Wispr Flow — and forwards it cleanly into the agent's stdin. Slash-key autocomplete pulls from your `~/.claude/skills/`, per-project skills, and installed plugins.
- **A virtual key bar** with the keys iOS won't give you: Esc, Tab, Shift+Tab (cycle Claude Code modes), arrows. Tap them between dictations without losing the soft keyboard.
- **Web Push notifications** when an agent goes idle. Throttled, VAPID-signed, per-device subscriptions, no third-party push services.
- **Share-sheet target** — Argus shows up natively in the Android share sheet. iOS gets a one-paste Shortcut that does the same. Either way, sharing a URL or a chunk of text into Argus lands you on the New Task tab with the prompt pre-filled.
- **GitHub-style stacked diff view** — every changed file in the worktree as a collapsible panel, expand-all, wrap toggle, optimistic for thumbs.
- **Per-device API tokens** — your iPhone, your iPad, and your laptop each get their own labeled token. Revoke any of them from the dashboard. Master token mints; SHA-256 hashes are all that's stored.
- **Offline-aware** — when the daemon is unreachable (laptop closed, Tailscale off) the PWA flips to a branded offline screen and reconnects automatically.
- **Pure-local** — runs on `localhost` and your Tailscale IP only, never `0.0.0.0`. Hotel/cafe LANs cannot reach the API even with the token.

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

<p align="center">
  <img src="screenshots/knowledge-graph.png" width="820" alt="Obsidian graph view of the indexed vault — session captures, handoffs, and reports linked into a knowledge graph">
</p>

- **`kb_search`** — ranked full-text search across the entire vault, with snippets.
- **`kb_read`** — full markdown by vault-relative path. Wiki-link friendly.
- **`kb_list`** — directory listing with prefix filtering for path-aware browsing.
- **`kb_ingest`** — agents write their own learnings back. Your KB grows from sessions instead of decaying between them.
- **Live re-indexing** — files dropped into the vault are searchable in seconds.
- **Schema-aware** — YAML frontmatter (title + tags) drives retrieval and clustering.

Pair this with the MCP task tools and an agent can read a meeting note, decide what to build, spawn its own worker tasks, and archive itself when done — all in a single conversation.

Disabled by default — see **[Knowledge Base setup](docs/knowledge-base.md)** to enable it, point it at a vault, and verify.

## Also In The Box

- **Remote TUI** — `argus --remote https://your-mac.tail-xxxx.ts.net --token "$ARGUS_TOKEN"` launches the full TUI against a daemon running on another machine. Same keybindings, same panels, same agent stream — over Tailscale. No local SQLite, no daemon socket; every call rides the REST API the PWA already uses.
- **Multi-backend** — Claude Code, Codex, or any LLM CLI as a templated command. Per-backend prompt flags, plan-mode defaults, and a default model, plus a per-task model override injected as `--model` at launch.
- **Worktree isolation** — every task gets `~/.argus/worktrees/<project>/<task>` and an `argus/<task>` branch, all transactionally created and cleaned up.
- **Session resume** — `--resume` on Claude Code, `codex resume <id>` on Codex. Your conversation survives a daemon restart.
- **Consistent scrollback across viewers** — switch between the TUI and the PWA at very different widths and the agent re-emits the conversation at the new size. Idle-gated so it never fires mid-tool-call; the SPA reattaches transparently.
- **Agent forking** — duplicate a running task with full context (source info, recent output, git diff) injected into the new worktree.
- **Smart auto-naming** — a Claude Haiku call quietly turns a free-form prompt into a kebab-case task name. Falls open to a regex slug if `claude` is unavailable.
- **Scheduled tasks** — cron, descriptors, intervals, or one-shot runs. Each fire spawns a fresh task. Manage from TUI, PWA, or MCP.
- **macOS sandbox-exec** — per-session SBPL profiles. `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud` blocked by default.
- **Session-supervisor** — agent PTYs live in a long-lived out-of-process supervisor, not the daemon, so bouncing the daemon (for an upgrade or a config change) re-attaches to still-running agents instead of killing them. On by default; one flag rolls back to the legacy in-process runner.
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

## Getting Started

### Prerequisites

- **Go 1.26+** — to `go install` the binary above.
- **Git** — every project Argus drives must be a git repository.
- **At least one agent CLI on your `PATH`.** Argus shells out to whatever backend you pick; it doesn't bundle a model. The default backend is **Claude Code** (`claude`), and `codex` and `pi` come pre-configured too. Install the one you use and make sure it runs from a plain shell (`claude --version`).
- **Optional:** [`gh`](https://cli.github.com) (GitHub CLI) powers the open-repo / open-PR keys and the PR-status indicator — features degrade quietly if it's absent. [Tailscale](https://tailscale.com) is recommended for reaching the PWA from your phone.

### First run

```bash
argus
```

The first launch creates `~/.argus/data.sql`, seeds the `claude` / `codex` / `pi` backends, and auto-starts the background daemon. You land on an empty task list — **no projects are seeded, so add one before creating a task.**

1. **Register a project.** Press `3` for the **Settings** tab, move to the **Projects** section, and either:
   - press `i` to **quick-add** — point it at a directory (e.g. `~/src`) and Argus scans for git repos; select the ones to import; or
   - press `n` to add one **manually** — give it a name and the absolute path to the repo root (base branch and backend are optional; they fall back to git's default and `claude`).
2. **Create your first task.** Press `1` for the **Tasks** tab, then `n`. Pick the project, type a prompt, and hit `Enter`. Argus cuts a fresh worktree at `~/.argus/worktrees/<project>/<task>` on an `argus/<task>` branch, starts the agent, and drops you into the agent view with its live terminal.
3. **Drive it.** `Enter` reopens an agent, `s` advances status, `ctrl+z` toggles the git/file side panes, `ctrl+q` steps back out. The full keymap is in the [Keybindings](#keybindings) reference below.
4. **Go mobile (optional).** Enable **Remote API** in Settings and open the PWA as described under [Install](#install).

---

## Reference

The sections below are the dense usage docs — keybindings, REST endpoints, configuration tables. Skim if you're getting started; bookmark if you're already running.

### Keybindings

#### Task List

| Key       | Action                                                          |
| --------- | --------------------------------------------------------------- |
| `n`       | New task (with skill autocomplete in prompt field)              |
| `Enter`   | Open agent view                                                 |
| `ctrl+f`  | Fork task (duplicate with context)                              |
| `s` / `S` | Advance / revert status                                         |
| `a`       | Toggle archive                                                  |
| `P`       | Toggle pin (★ section pinned to the top of the task list)       |
| `c`       | Copy task prompt to clipboard                                   |
| `r`       | Rename task (display name only; branch/worktree stay locked)    |
| `H`       | Toggle hidden Hera-spawned workers (hidden by default — they live in the Hera tab) |
| `f`       | Freelancers only — hide all hera-managed tasks (live coordinator/worker bindings); toggle off to restore |
| `ctrl+d`  | Destroy task (kill agent + remove worktree + delete branch)     |
| `ctrl+o`  | Open the project's GitHub repo in browser (via `gh repo view --web`) |
| `ctrl+r`  | Prune completed tasks                                           |
| `j` / `k` | Navigate up/down                                                |
| `1` / `2` / `3` | Switch tabs (Tasks / Hera / Settings) |
| `ctrl+l`  | Refresh screen (wipe ghost cells; works in every non-agent tab) |
| `q`       | Quit                                                            |

#### Agent View

| Key                   | Action                                                                    |
| --------------------- | ------------------------------------------------------------------------- |
| `ctrl+q`              | Back, 3-level (diff → files panel → task list)                            |
| `Esc`                 | Refocus terminal from diff/files; on the terminal, forwarded to the agent (does NOT exit the agent view) |
| `Cmd+←` / `Cmd+→`     | Switch panels (no-op when zoomed — side panels are hidden)                |
| `Cmd+↑` / `Cmd+↓`     | Navigate between tasks                                                    |
| `ctrl+k`              | Open task switcher (fuzzy-search all tasks by name; tasks needing input pinned to the top) |
| `ctrl+z`              | Toggle the git + file side panes (default layout set by Settings → Appearance → "Default agent view") |
| `ctrl+l`              | Open link picker (fuzzy search all session URLs)                          |
| `ctrl+r`              | Switch Claude session (searchable picker of this task's conversations; resumes the chosen one). Claude backends only |
| `ctrl+p`              | Open PR for the worktree branch in browser (via `gh pr view --web`)       |
| `ctrl+y`              | Copy agent-staged text (only when payload pending; otherwise sent to PTY) |
| `Shift+↑` / `Shift+↓` | Scroll terminal (with acceleration)                                       |

#### Hera Tab

The Hera tab (`2`) has three regions: a left **rail**, a middle **coordinator pane**, and a right **details** region. The rail lists active orchestrators with their coordinator/worker roles, plus **Pinned**, **Freelance**, and a collapsed **Archive** section. Keys act on the rail selection:

| Key             | Action                                                                                 |
| --------------- | -------------------------------------------------------------------------------------- |
| `j` / `k`       | Move the rail cursor down / up                                                          |
| `Space`         | Collapse / expand an orchestrator, or the Freelance / Archive section                  |
| `/`             | Filter the rail by name (substring, ancestry-preserving); `↑`/`↓` navigate the filtered rows while typing, `Enter` accepts, `Esc` clears |
| `Tab` / `Shift+Tab` | Cycle focus across rail → coordinator pane → details region                        |
| `ctrl+z`        | Fullscreen the focused content pane (rail stays; the other pane hides). Also traps `^Z` so it can never suspend the pane's agent |
| `Enter`         | Enter the selected role's pane, reviving its session first — a dead session is restarted, and a suspended/stuck worker is resumed in place via `--session-id` |
| `w`             | Spawn a worker under the selected coordinator (opens the full new-task modal: project / branch / backend / model / prompt, project defaulted to the coordinator's) |
| `n`             | Create a new top-level coordinator (same new-task modal); bootstraps a fresh orchestrator + `coord` role bound to a new task. Works on an empty rail |
| `r`             | Rename the selected role / orchestrator                                                 |
| `a`             | Archive / unarchive the selected role / orchestrator                                    |
| `P`             | Pin / unpin the selected role / orchestrator                                            |
| `s` / `S`       | Advance / revert the selected **Hera role** status (`idle → working → blocked → done`)  |
| `J`             | Adopt a freelancer into, or re-parent a coordinator under, a chosen orchestrator (type-to-filter picker) |
| `R`             | Retire the selected worker (confirm): stop the session, archive the task (worktree **kept** — reversible), end this role's binding, archive the role. Multi-bound tasks are preserved |
| `C`             | Prune the selected coordinator's **archived** descendant workers (confirm): complete their tasks and reclaim their worktrees + branches |
| `ctrl+r`        | Prune **all** finished coordinators + agents rail-wide (confirm): complete their tasks, reclaim worktrees, and close fully-finished orchestrators. Rail-scoped — never collides with the agent-view `ctrl+r` session switcher |
| `ctrl+d`        | Delete the selected role / orchestrator; on a nested sub-coordinator row, cascade-delete the whole sub-team (confirm-gated) |
| `Cmd+↑` / `Cmd+↓` | Move the rail cursor up / down without changing the focused pane (the mod-7 escape sequence is consumed — the pane's PTY never sees it) |
| `ctrl+q`        | Return focus to the rail                                                                |

When a **worker** is selected the details region shows its live agent terminal. When a **coordinator** is selected it stacks a read-only roster of that orchestrator's roles (status, ready-to-close, PR marks) over the embedded **orchestration tree** (coordinator → workers → sub-coordinators) — both render at once, no toggle. The tree is the interactive surface: arrows / `j`/`k` move the cursor and `Enter` jumps to the selected node's agent view.

The end-of-life ladder is **retire → prune**: `R` archives a finished worker (reversible, keeps the worktree); `C` and `ctrl+r` reclaim the worktrees of already-archived / finished work. Every destructive key is confirm-gated and honors multi-binding isolation (a task bound live under another orchestrator is never destroyed).

#### File Panel

| Key     | Action                    |
| ------- | ------------------------- |
| `Enter` | Open diff                 |
| `s`     | Toggle split/unified diff |
| `f`     | Reveal in Finder          |
| `o`     | Open file (default app)   |
| `e`     | Open in editor (`$EDITOR`) |
| `t`     | Open terminal in worktree |

#### Modals & Forms

| Key                 | Action           |
| ------------------- | ---------------- |
| `Esc` / `ctrl+q`    | Close / cancel   |
| `Enter`             | Confirm / submit |
| `Tab` / `Shift+Tab` | Navigate fields  |

#### Settings

| Key                   | Action                                                   |
| --------------------- | -------------------------------------------------------- |
| `j` / `k`             | Navigate rows                                            |
| `n`                   | New project / backend / schedule                         |
| `e`                   | Edit project / backend / schedule                        |
| `d`                   | Delete project / set default backend / delete schedule   |
| `a`                   | Edit project's AppleEvents allowlist (on a project row)  |
| `m`                   | Edit backend's default model (on a backend row)          |
| `t`                   | Toggle schedule enabled (on the Scheduled Tasks section) |
| `r`                   | Run schedule now (on the Scheduled Tasks section)        |
| `i`                   | Quick add projects                                       |
| `Enter` / `◀` / `▶` | Toggle / cycle settings                                  |

### Remote TUI

```bash
argus --remote https://mbp-2026.tail1efd7.ts.net --token "$ARGUS_TOKEN"
```

Launches the TUI pointed at a remote argus daemon instead of the local one. No local SQLite is opened, no daemon socket is contacted — every persistence call goes through the REST API the daemon already serves on port 7743 (the same surface the PWA uses). `--token` falls back to `ARGUS_TOKEN`.

A few local-only operations gracefully degrade in remote mode: spawning a fresh task via the new-task form, forking, schedule fires, and prune-completed all require local worktree access. The status bar surfaces the equivalent REST endpoint when these are attempted remotely. Everything else — task list, attach, input, resize, archive/rename/status flips, settings — works identically against the remote.

### Self-Update

From the **Settings tab** (Status section, when the daemon is connected) the **Source path** row holds the path to your local Argus checkout, and the **Update Argus** row runs `git pull --ff-only` followed by `go install ./...` and then restarts the daemon so the new binary takes over. Active sessions reattach across the restart. The same controls are exposed in the web UI under **Settings → Argus update** (master token only).

### Hera (native multi-agent coordination)

Hera is Argus's native layer for running a *team* of agents. It introduces **roles** — a `coordinator` plus the `worker`s and `freelance`rs it spawns — bound to argus tasks and addressed by name. A coordinator delegates work to workers it spawns (`hera_spawn_worker` / the rail's `w` key), they trade messages over the same idle-gated bus that powers inter-task messaging, and the whole team renders as an **orchestration tree** (coordinator → workers → sub-coordinators) folded into the Hera tab's details pane. The whole surface is the second tab (`2`) — see the [Hera Tab](#hera-tab) keybindings above. The coordination layer runs in-process in the daemon; the view renders directly in the TUI. Agents drive it over MCP (the [`hera_*` tools](#mcp-tools)).

**Native Hera and the external Hera plugin are mutually exclusive, selected by `hera.enabled` (default ON):**

- **`hera.enabled = true` (default)** — native Hera is active. It stores its state in the same `~/.argus/data.sql` (the `hera_*` tables), exposes the `hera_*` MCP tools in-process, and owns the second tab. The legacy Hera plugin's tools are suppressed so they never double-register.
- **`hera.enabled = false`** — the native `hera_*` MCP tools are not served, and you can instead run the external **Hera plugin** over the [plugin substrate](#plugin-substrate). The plugin keeps its own `~/.hera` state and plugin view, entirely unaffected by Argus. The TUI's second tab is always the native Hera view regardless of this flag.

The two run **independently and share no state.** Switching to native Hera performs **no migration** of any prior `~/.hera` data — native Hera starts fresh. Set the flag in `config.toml` (`[hera] enabled = …`) or the DB.

### Daemon & session-supervisor

Argus splits agent supervision across **two** background processes:

- The **daemon** (`argus daemon`) owns coordination — hera, the REST API, MCP, the scheduler, the DB, and the TUI's Unix socket. It is **bounce-able**: restarting it (for an upgrade, a config change, or to iterate on coordination) is cheap.
- The **session-supervisor** (`argus session-supervisor`) owns the agent PTYs themselves — the `exec.Cmd`, the master fd, the read/wait loops, the ring buffers, and the real exit codes. It is **long-lived and rarely restarted**. The daemon connects to it over `~/.argus/supervisor.sock` and proxies every session through it.

Because the supervisor — not the daemon — is the agent's parent process, **bouncing the daemon no longer interrupts agents.** A daemon restart re-attaches to the still-running sessions (the in-flight turn continues); only restarting the *supervisor* interrupts agents (they get SIGHUP when their PTY master closes), which is why the supervisor's interface is kept strict and it almost never needs to restart. Self-update therefore restarts the daemon, not the supervisor — your agents keep running across the swap.

**Cycling the daemon (the safe, common case — agents survive):**

```bash
argus daemon restart   # stop + wait for socket cleanup + start a fresh daemon
argus daemon stop      # graceful shutdown
argus daemon start     # start (auto-starts a supervisor too if none is answering)
```

A daemon restart is the path you want for an upgrade or config change: the supervisor keeps the PTYs alive and the new daemon re-attaches to the still-running sessions. You can also trigger it from **Settings → System → Restart Daemon** (Enter).

**Cycling the supervisor (rare — this interrupts agents):** the daemon auto-starts a supervisor on its own startup if none is answering on the socket (Setsid-detached, so it outlives daemon bounces), so you rarely drive it by hand. When you must — e.g. to load a new supervisor binary — the subcommands exist:

```bash
argus session-supervisor start    # start the supervisor (auto-started by the daemon if absent)
argus session-supervisor stop     # stop it — INTERRUPTS all agents (they re-resume on next start)
argus session-supervisor status   # show supervisor pid/socket/protocol state
```

Or use **Settings → System → Restart Session Supervisor** (Enter), which is gated behind a confirmation prompt because it SIGHUPs every running agent. Under the hood it stops the supervisor and then restarts the daemon, since the daemon holds the supervisor connection and has no mid-life reconnect — bouncing the daemon is how it picks up the freshly-started supervisor. Active tasks are interrupted and flip to **In Review**. The row only appears when supervisor mode is on.

**Supervisor mode is ON by default** (`supervisor.enabled`, see the config table below). To **roll back** to the legacy in-process path — where the daemon owns the PTYs itself, exactly as before the supervisor existed — set `supervisor.enabled = false` (config.toml or the DB) and restart the daemon. The in-process path is retained as a supported fallback for one release.

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

Global sandbox settings are managed in the **Settings tab** (`3` key):

| Setting     | Description                                        |
| ----------- | -------------------------------------------------- |
| Enabled     | Master toggle — applies to all projects by default |
| Deny Read   | Extra paths to block reads from (comma-separated)  |
| Extra Write | Extra paths to allow writes to (comma-separated)   |

Per-project overrides are set in the **project form** (`e` on a project in Settings) — **Inherit**, **Enabled**, or **Disabled**. Per-project deny-read and extra-write paths are appended to the global lists.

**Always denied read:** `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud`
**Always allowed write:** the task's worktree directory, `/tmp`, `/var/folders`, `~/.claude.json`, `~/.claude/`, `~/Library/Application Support/Google/Chrome` (Chrome's crashpad writes there regardless of `--user-data-dir`), the main repo's `.git` dir.
**Always allowed (IOKit):** user-client opens (`iokit-open` / `iokit-open-user-client`) — required for headful Chrome (Playwright/Puppeteer), which calls `IOServiceOpen` on `IOPMrootDomain` at startup and SIGSEGVs on the denied open otherwise. The crashpad write rule above is necessary but not sufficient on its own.

### Running inside tmux

argus renders via [tcell](https://github.com/gdamore/tcell) v2.13+ which automatically wraps every frame in DECSET 2026 (Synchronized Output / BSU+ESU) when the terminal claims to be `XTermLike` — tmux's terminfo does. This means the inner application emits an atomic-frame sequence that, when honored, eliminates rendering tearing during fast updates (typing, PTY streaming, cursor nav).

**tmux 3.2+ does not honor those inner sequences by default — you have to opt in.** Without the opt-in, you'll see occasional visual artifacts (stale cells, partial frames) during rapid screen updates. Add this to `~/.tmux.conf`:

```tmux
set -g default-terminal "tmux-256color"
set -as terminal-features ',xterm*:sync'
```

Reload tmux config (`Prefix + :` then `source-file ~/.tmux.conf`, or restart tmux entirely) and the artifacts disappear. This is the same fix used by Claude Code, neovim, kakoune, and other modern tcell/ncurses apps that emit synchronized output.

If you still see visible flashing after this config, that's a different issue — file a bug. argus calls `screen.Sync()` (the only thing that emits `CSI 2J` / clear-screen) in just two places: when you press `Ctrl+L` (manual refresh, one flash expected) and on tmux pane focus regain (recovers from any drift while you were on another window). Every other UI update flows through tcell's diff and should be invisible inside a properly-configured tmux.

### Spinner Styles

Cycle through styles in the **Settings tab** using `Enter` or `◀`/`▶` on the **Spinner** row:

| Style                  | Frames                     | Speed |
| ---------------------- | -------------------------- | ----- |
| **Progress** (default) | Nerd Font progress icons   | 150ms |
| **Dots**               | Braille dots `⠋⠙⠹⠸⠼⠴⠦⠧⠇⠏`  | 100ms |
| **Braille**            | Braille pattern `⣷⣯⣟⡿⢿⣻⣽⣾` | 100ms |
| **Classic**            | ASCII `\|/-\\`             | 150ms |

### MCP Tools

Argus runs an MCP server on port 7742 and auto-injects it into every agent worktree.

**Knowledge Base:**

| Tool        | Description                                        |
| ----------- | -------------------------------------------------- |
| `kb_search` | Full-text search with ranked results and snippets  |
| `kb_read`   | Read full document content by vault-relative path  |
| `kb_list`   | List documents with optional path prefix filtering |
| `kb_ingest` | Add or update a document in the knowledge base     |
| `kb_delete` | Remove a document by vault-relative path           |

**Task Management** (lets agents orchestrate other agents):

| Tool                   | Description                                                                                                                                                        |
| ---------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `task_create`          | Create a task with worktree and start an agent. Params: `name`, `prompt`, `project`, `model` (optional `--model` override), `base_branch` (stacked-PR start point), `upsert`. The session starts immediately. |
| `task_list`            | List tasks, filtered by `status` and/or `project`.                                                                                                                |
| `task_get`             | Get task details by `id`                                                                                                                                           |
| `task_stop`            | Stop a running agent (moves task to "in review")                                                                                                                   |
| `task_archive`         | Archive or unarchive a task. Pass `cwd` (from the agent's `pwd`) to resolve by worktree, or `id`. Omit `archived` to toggle.                                       |
| `task_rename`          | Rename a task. Updates only the display name (branch and worktree paths stay locked to the original slug). Pass `cwd` or `id` plus `name`.                         |
| `task_complete`        | Mark a task as complete (sets status, stamps `EndedAt`). Pass `cwd` or `id`. Does NOT stop a running agent — call `task_stop` first if needed.                     |
| `task_set_result`      | Persist an opaque JSON result blob the orchestrator can read (PR URL, milestone, failure reason). Pass `cwd` or `id` plus `result`. Up to 64 KiB.                  |

Sample skills at `.claude/skills/archive/SKILL.md` and `.claude/skills/argus-complete/SKILL.md` let an agent finalize its own task at the end of a session via `cwd` resolution. Completing and archiving are independent axes.

**Inter-Task Messaging** (peer-to-peer between live or paused tasks):

| Tool                | Description                                                                                                                                                                                                                                              |
| ------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `task_message_send` | Send a peer-to-peer message. Params: `to`, `body`, optional `kind` (`note` / `question` / `answer`), optional `in_reply_to`. Caller resolved via `cwd` or `id`. Body ≤ 64 KiB. Recipient inbox capped at 500 unread; sender rate-limited to 50/min.      |
| `task_inbox`        | Read messages addressed to the caller, oldest-first. Filters: `unread_only` (default true), `sender`, `since` (RFC3339), `limit` (default 50, max 500). Does NOT auto-mark read.                                                                         |
| `task_message_ack`  | Mark messages read. Pass `message_ids` (up to 500). IDs not addressed to the caller are silently ignored.                                                                                                                                                |
| `task_ask`          | Convenience: send a question and optionally block until a reply lands. Params: `to`, `body`, optional `timeout_seconds` (default 0 = return immediately; max 120). When blocking, polls the answer at 500 ms cadence; callers wanting longer waits poll. |

If the recipient has a live agent session the daemon also writes a single notification line into their PTY (best-effort). Same surface available over REST: `GET /api/tasks/{id}/inbox`, `POST /api/tasks/{id}/inbox/ack`, `POST /api/tasks/{id}/messages`.

**Hera** (native multi-agent coordination — served only when `hera.enabled`; see the [Hera](#hera-native-multi-agent-coordination) reference):

| Tool                    | Description                                                                                                                                       |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------- |
| `hera_new_orchestrator` | Bootstrap a new orchestrator and claim its `coordinator` role for the calling task.                                                               |
| `hera_join`             | Claim the calling task's existing role + unread count, or (with `role_name` + `kind`) attach a new `worker`/`freelance` role under an orchestrator. |
| `hera_spawn_worker`     | Spawn a born-bound worker task + session under the caller's orchestrator (caller must hold a live coordinator binding). Optional `model` picks the worker's model by task complexity (backend-scoped; empty = backend default). |
| `hera_send`             | Send a role-addressed message; workers/freelancers default to the coordinator when `to` is omitted, coordinators must name a recipient.           |
| `hera_inbox`            | Fetch the caller role's unread messages (oldest first), cancel their pending pane deliveries, and mark them read.                                 |
| `hera_mark_read`        | Mark a specific list of message IDs read and cancel their pending deliveries.                                                                     |
| `hera_status`           | Set the caller role's status (`idle`/`working`/`blocked`/`done`), mirrored to `task_meta`; a worker reporting `done` rolls its task to in-review. |
| `hera_tree_updates`     | Scan the caller's orchestrator subtree for messages since a per-role cursor; returns TLDR subject lines only and auto-advances the cursor.        |
| `hera_get_messages`     | Fetch full message bodies by ID (after `hera_tree_updates`), scoped to the caller's orchestrator subtree.                                         |

**Schedule Management:**

| Tool               | Description                                                                                                                                                                                               |
| ------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `schedule_list`    | List all schedules with name, project, cron expression, enabled state, next/last fire timestamps                                                                                                          |
| `schedule_create`  | Create. Params: `name`, `project`, `prompt`, plus exactly one of `schedule` (cron or `@every <duration>`) or `run_once_at` (RFC3339 UTC); optional `backend`, `enabled`                                   |
| `schedule_update`  | Partial update — pass `id` plus any fields to change. Toggling `enabled`, rotating prompts, or converting between cron and one-shot (set the new field; the other clears automatically).                  |
| `schedule_delete`  | Remove a schedule by `id`. Tasks already created by previous fires are unaffected.                                                                                                                        |
| `schedule_run_now` | Fire a schedule immediately, out of cycle. Bookkeeping is updated so the next regular tick will not double-fire. One-shot rows auto-disable. Does NOT send a push notification — only cron-tick fires do. |

**Agent-Staged Clipboard:**

| Tool                  | Description                                                                                                                                                                     |
| --------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `argus_clipboard_set` | Stage text for the user to copy with one tap (PWA Copy button) or one keypress (TUI `ctrl+y`). Params: `text` (required), `id` or `cwd`. Last-write-wins, 5-min TTL, 1 MiB max. |

**Artifacts:**

| Tool                | Description                                                                                                                                                                                          |
| ------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `artifact_register` | Register a file the agent produced (HTML report, PDF, markdown, image, or text) so it renders in Argus Web. Params: `path` (required), `title`, `type`, `id` or `cwd`. Self-contained files render best; 25 MiB max. |

### Remote Control: REST API

All endpoints require auth — `Authorization: Bearer <token>` header or `?token=<token>` query param (the latter is required for `EventSource`/SSE because browsers cannot set headers on it). The token can be the master token from `~/.argus/api-token` or any non-revoked device token.

Every authenticated token has the same permissions **except** a small master-only denylist: **backends CRUD** (command templates can run arbitrary code), **self-update** (`/api/source-path`, `/api/update`), and **token list/mint/revoke** (`/api/tokens`). Those endpoints return `403` for device tokens; everything else — tasks, projects, schedules, settings, messages, push — accepts any token. One extra carve-out lives inside `PUT /api/settings`: the **`sandbox` section is master-only** (it governs the host sandbox-exec boundary), while KB/API/UX-defaults are open.

#### Tasks

| Method   | Endpoint                    | Description                                                                                                                                                                                                                                                                      |
| -------- | --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`    | `/api/status`               | Running/idle session counts, task counts by status                                                                                                                                                                                                                               |
| `GET`    | `/api/tasks`                | List tasks. Filters: `?status=`, `?project=`, `?archived=1` (or `=all`). Each task carries `idle: true` when `in_progress` but the session is missing or waiting for input.                                                                                                      |
| `POST`   | `/api/tasks`                | Create and start a task. JSON `{"name", "prompt", "project", "backend?", "model?"}`, OR `multipart/form-data` with `name`/`prompt`/`project`/`backend`/`model` plus `files` parts (uploaded into `<worktree>/.context/`, paths appended to the prompt). Per-file 10MB / total 50MB / 20 files cap. |
| `GET`    | `/api/tasks/{id}`           | Get single task detail (includes `archived`, `worktree_path`, `prompt`, `idle`)                                                                                                                                                                                                  |
| `POST`   | `/api/tasks/{id}/stop`      | Stop a running agent (moves to `in_review`)                                                                                                                                                                                                                                      |
| `POST`   | `/api/tasks/{id}/resume`    | Resume a stopped agent (un-pause an `in_review` task)                                                                                                                                                                                                                            |
| `POST`   | `/api/tasks/{id}/restart`   | Re-spawn a finished session in the same worktree (resumes the prior conversation)                                                                                                                                                                                                |
| `DELETE` | `/api/tasks/{id}`           | Delete a task                                                                                                                                                                                                                                                                    |
| `POST`   | `/api/tasks/{id}/archive`   | Archive (hidden from default list)                                                                                                                                                                                                                                               |
| `POST`   | `/api/tasks/{id}/unarchive` | Restore from archive                                                                                                                                                                                                                                                             |
| `POST`   | `/api/tasks/{id}/rename`    | `{"name":"..."}`                                                                                                                                                                                                                                                                 |
| `POST`   | `/api/tasks/{id}/fork`      | Clone to a new task. Body: `{"name?", "prompt?", "project?"}`                                                                                                                                                                                                                    |
| `POST`   | `/api/tasks/{id}/status`    | Set status. Body: `{"status":"in_review"\|"complete"\|"pending"\|"in_progress"}`                                                                                                                                                                                                 |

#### Sessions / terminal

| Method | Endpoint                 | Description                                                                                                                                                                                                                                                                                                                 |
| ------ | ------------------------ | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/tasks/{id}/output` | Recent output (text). Optional `?bytes=`, `?clean=1`                                                                                                                                                                                                                                                                        |
| `GET`  | `/api/tasks/{id}/links`  | Extract http/https URLs from terminal output. Returns `{"links":[{"label","url"}]}`. Powers the PWA's "Open link" overflow item.                                                                                                                                                                                            |
| `POST` | `/api/tasks/{id}/input`  | Send raw bytes to PTY stdin                                                                                                                                                                                                                                                                                                 |
| `POST` | `/api/tasks/{id}/upload` | Upload files mid-session. `multipart/form-data` with `files` parts; saved to `<worktree>/.context/<name>` (auto-suffixed on collision) and returns `{paths:[]}`. Same 10MB/50MB/20-file caps as create.                                                                                                                     |
| `GET`  | `/api/tasks/{id}/stream` | SSE stream of live output (base64-encoded chunks)                                                                                                                                                                                                                                                                           |
| `GET`  | `/api/tasks/{id}/size`   | Current PTY dimensions: `{cols, rows}`                                                                                                                                                                                                                                                                                      |
| `POST` | `/api/tasks/{id}/resize` | Resize PTY: `{"cols":N,"rows":M}`. Returns `{cols,rows,rerendered}` — `rerendered:true` means the resize crossed the rerender margin (≥15 col delta from session-start width) and the daemon queued a kill+resume so the agent re-emits scrollback at the new width. The SPA's exit-event handler reattaches automatically. |
| `POST` | `/api/sessions/stop-all` | Stop every running session                                                                                                                                                                                                                                                                                                  |

#### Git status / diff / files

| Method | Endpoint                               | Description                                             |
| ------ | -------------------------------------- | ------------------------------------------------------- |
| `GET`  | `/api/tasks/{id}/git/status`           | git status output + branch diff for the task's worktree |
| `GET`  | `/api/tasks/{id}/git/diff?path=<file>` | Unified diff for a single file                          |
| `GET`  | `/api/tasks/{id}/files?dir=<rel>`      | Worktree file listing                                   |

#### Session artifacts

| Method | Endpoint                                  | Description                                                                                                            |
| ------ | ----------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/tasks/{id}/artifacts`               | List artifacts the agent registered via `artifact_register` (name, title, type, size)                                |
| `GET`  | `/api/tasks/{id}/artifacts/{filename}`    | Serve one artifact's raw bytes. Scoped to the registered manifest set (no path traversal); HTML served in a sandbox. |

#### Maintenance

| Method | Endpoint                            | Description                                                                                  |
| ------ | ----------------------------------- | -------------------------------------------------------------------------------------------- |
| `POST` | `/api/maintenance/prune-completed`  | Delete all completed tasks — removes worktrees/branches and sweeps orphans (mirrors TUI `ctrl+r`) |

#### Projects & backends (full CRUD)

| Method   | Endpoint               | Description                                                                                                                                                                         |
| -------- | ---------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`    | `/api/projects`        | List project names                                                                                                                                                                  |
| `GET`    | `/api/projects/full`   | List with path, branch, default_backend                                                                                                                                             |
| `POST`   | `/api/projects`        | Create. Body: `{"name", "path", "branch?", "backend?", "sandbox?"}` where `sandbox` is `{"enabled": true\|false\|null, "deny_read":[], "extra_write":[]}` (`null` = inherit global) |
| `PUT`    | `/api/projects/{name}` | Update                                                                                                                                                                              |
| `DELETE` | `/api/projects/{name}` | Delete                                                                                                                                                                              |
| `GET`    | `/api/backends`        | List with command + prompt_flag + model                                                                                                                                             |
| `POST`   | `/api/backends`        | Create. Body includes optional `model` (default `--model`). **Master token required** (command templates can run arbitrary code).                                                   |
| `PUT`    | `/api/backends/{name}` | Update. **Master token required.**                                                                                                                                                  |
| `DELETE` | `/api/backends/{name}` | Delete. **Master token required.**                                                                                                                                                  |
| `GET`    | `/api/skills`          | Skill autocomplete. Filter: `?project=`, `?filter=` (case-insensitive substring)                                                                                                    |

#### Hera orchestration

| Method | Endpoint    | Description                                                                                                                                                                                                                            |
| ------ | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/hera` | Read-only orchestration roster. Returns `{orchestrators:[{id,name,pinned,archived,roles:[…]}], freelance:[…]}`. Each role carries `kind` (coordinator/worker/freelance), `status`, bound `task_id`/`task_name`/`task_status`, `live`, and `ready_to_close`. Feeds the webapp's **Hera** tab. |

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
| `GET`    | `/api/schedules`          | List schedules with `next_run_at`, `last_run_at`, `last_task_id`, `last_error`. |
| `POST`   | `/api/schedules`          | Create. Body: `{"name","project","prompt","schedule","backend?","enabled"}`. Returns the created row. |
| `PUT`    | `/api/schedules/{id}`     | Partial update — every field optional. Useful for toggling `enabled`. |
| `DELETE` | `/api/schedules/{id}`     | Remove. Tasks already created by the schedule are not affected. |
| `POST`   | `/api/schedules/{id}/run` | Fire the schedule now, regardless of cron timing. Returns `{"task_id"}`. |

Schedule expressions accept the standard 5-field cron syntax (e.g. `0 9 * * 1-5`), descriptors (`@hourly`, `@daily`, `@weekly`, `@monthly`, `@yearly`), and intervals (`@every 30m`).

#### Settings & logs

| Method | Endpoint                         | Description                                                                                                                                  |
| ------ | -------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/settings`                  | Returns sandbox / KB / API / defaults config plus `sandbox.available` (whether `sandbox-exec` is on this host).                              |
| `PUT`  | `/api/settings`                  | Partial update — every section is optional. Body: `{"sandbox":{...}, "kb":{...}, "api":{...}, "defaults":{...}}`. The `sandbox` section is **master token required**; other sections accept any token. |
| `GET`  | `/api/logs/{ux\|daemon}?bytes=N` | Tail the last N bytes of the log (default 64K, max 1M). Missing files return `200` with empty body.                                          |

### Plugin substrate

Argus can host external programs as **plugins** — a separate process (on `127.0.0.1`) that registers MCP tools, Settings forms, and full-screen views, and consumes a live event stream. This is the substrate for out-of-tree orchestrators and tools driven entirely over these endpoints. **Hera** now also ships *natively* in-tree (default — see the [Hera](#hera-native-multi-agent-coordination) section); the plugin substrate remains the path for the external Hera plugin when `hera.enabled = false`, and for any other third-party orchestrator.

Plugins authenticate with a **scope token** (`X-Argus-Auth: scope:<name>`), a third tier alongside master and device tokens. Scope tokens are minted programmatically by the daemon (not over HTTP). A scope token may only register/unregister tools, sections, and views under **its own scope** — cross-scope access returns `403`. Revoking a scope token cascades: every tool, section, and view registered under that scope is dropped. Scope tokens are still blocked from the master-only denylist (token mint/revoke, backends CRUD, self-update, sandbox settings).

**MCP tool registration** — a plugin exposes tools through Argus's MCP server (port 7742); on invoke, the daemon POSTs `{tool, input, context}` to the tool's `callback_url`.

| Method   | Endpoint                | Token         | Description                                                                                                                          |
| -------- | ----------------------- | ------------- | ----------------------------------------------------------------------------------------------------------------------------------- |
| `POST`   | `/api/mcp/tools`        | `scope:<n>`   | Register a tool. Body: `{name, description, input_schema, callback_url, auth_header?}`. `name` must start with `<scope>_`. 100/scope, idle-swept after 10 min. |
| `DELETE` | `/api/mcp/tools/{name}` | master / owner | Unregister a tool                                                                                                                    |

**Plugin views** — a plugin hosts a full-screen TUI pane, dialed over WebSocket at `callback_url`. While active, Argus surrenders all keystrokes to the plugin (binary frames); the plugin sends control frames back (`hotkeys`, `help`, `release`) and receives `resize`/`focus`/`blur`. A double-`ctrl+q` within 400 ms is the reserved failsafe to force back to Argus.

| Method   | Endpoint                  | Token         | Description                                                          |
| -------- | ------------------------- | ------------- | ------------------------------------------------------------------- |
| `POST`   | `/api/plugins/views`      | master / `scope:<n>` | Register a view. Body: `{title, hotkey, callback_url}` (ws:// URL). Opened by its hotkey in the TUI. |
| `GET`    | `/api/plugins/views`      | master / `scope:<n>` | List views (scope sees only its own)                                |
| `DELETE` | `/api/plugins/views/{id}` | master / owner | Delete a view                                                       |

**Plugin settings sections** — a plugin registers a form (fields: `bool` / `int` / `string` / `enum`) or a live `stream` section that appears in Argus Settings. On save, the daemon POSTs the `{key: value}` map to the section's `callback_url`.

| Method   | Endpoint                                                | Token         | Description                                                        |
| -------- | ------------------------------------------------------- | ------------- | ----------------------------------------------------------------- |
| `POST`   | `/api/plugins/settings/sections`                        | `scope:<n>`   | Register. Body: `{title, type:"form"\|"stream", callback_url, auth_header?, fields?}` |
| `GET`    | `/api/plugins/settings/sections`                        | any           | List all registered sections                                      |
| `POST`   | `/api/plugins/settings/sections/{scope}/{title}/submit` | any           | Proxy a user's saved values to the plugin's callback              |
| `DELETE` | `/api/plugins/settings/sections/{scope}/{title}`        | master / owner | Unregister a section                                              |

**Event stream** — clients (PWA, plugins) subscribe to a live SSE feed of daemon events.

| Method | Endpoint                       | Token | Description                                                                                                                                                                                |
| ------ | ------------------------------ | ----- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `GET`  | `/api/events/stream?since=<n>` | any   | SSE feed of `task.*`, `session.*`, `message.*`, `link.*` events. `since` is an exclusive cursor that replays missed events; a cursor older than the ring emits a synthetic `resync` so the client re-snapshots. 30 s keepalives. |

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

### Config file (`~/.argus/config.toml`)

An optional `~/.argus/config.toml` overrides any setting, layered on top of the built-in defaults and the SQLite-backed settings (precedence: **defaults < settings menu < `config.toml`**). It's the alacritty-style power-user layer — customize beyond what the settings menu exposes, and keep your config in version control. The file is optional; a missing file changes nothing. Edits are picked up live on the next read.

Behavior notes:

- **The file wins over the settings menu** — any key present here masks changes you make in Settings. That's intentional.
- **Unknown or misspelled keys are silently ignored** (the file stays forward-compatible), so check the spelling against the tables below if an override seems to do nothing.
- **A malformed file is ignored** — logged to `~/.argus/ux.log` (or `~/.argus/daemon.log` when the daemon reads it) — and Argus falls back to the defaults + settings-menu values until the file parses again.
- **Maps merge by key.** `[backends.<name>]` / `[projects.<name>]` add a new key or replace an existing one *wholesale* — a partial entry zeroes the fields you omit (it can blank a project's `path`), so define those entries in full.

Every option below is overridable. A ⚠️ marks options that are **read but not yet honored** — they're persisted and accepted by the file, but no code consumes them yet (key remapping and theming are planned; see the table notes).

#### `[defaults]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `backend` | string | `"claude"` | Backend used for a new task when none is chosen in the New Task form. Must name a key under `[backends]`. |
| `share_project` | string | `""` | Project preselected in the New Task form when the PWA share target (iOS/Android share sheet) lands a payload. Empty falls back to the currently expanded project folder. |
| `permission_mode` | string | `"bypass-active"` | Permission flags injected into Claude-style backends at launch. One of `default`, `acceptEdits`, `plan`, `bypass-allow`, `bypass-active`. |

#### `[backends.<name>]`

Command templates, keyed by name. Seeded with `claude`, `codex`, and `pi`.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `command` | string | — | Executable plus base flags for the agent CLI (e.g. `claude`, `codex --dangerously-bypass-approvals-and-sandbox`). Permission flags come from `defaults.permission_mode` and are **not** baked in here. |
| `prompt_flag` | string | `""` | Flag used to pass the initial prompt to the backend (empty = positional/piped). |
| `model` | string | `""` | Default model for this backend, injected as `--model <value>` for known CLIs (claude, codex, pi). Empty = the CLI's own default. A per-task model overrides it. |

#### `[projects.<name>]`

Registered repos, keyed by name. The DB projects table is the primary source; entries here override it.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `path` | string | — | Absolute path to the git repository. |
| `branch` | string | — | Base branch new worktrees fork from. |
| `backend` | string | — | Per-project backend override; falls back to `defaults.backend`. |

`[projects.<name>.sandbox]` — per-project sandbox overrides. **These untagged fields match by lowercased Go name, not snake_case** (`denyread`, not `deny_read`):

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | inherit | Override the global sandbox on/off for this project (omit to inherit `sandbox.enabled`). |
| `denyread` | []string | `[]` | Extra paths appended to the global deny-read list for this project. |
| `extrawrite` | []string | `[]` | Extra writable paths appended to the global list. |
| `allowappleevents` | []string | `[]` | Extra AppleEvent destination bundle IDs allowed for this project. |

#### `[ui]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `spinner_style` | string | `"progress"` | Spinner animation: `progress`, `dots`, `braille`, or `classic`. |
| `default_agent_zoom` | bool | `true` | Resting agent-view layout: `true` opens single-pane/zoomed (side panels collapsed); `false` opens the 1:3:1 three-pane layout. `Ctrl+Z` toggles at runtime. |
| `theme` | string | `"default"` | ⚠️ Color theme name. Only `default` exists today and nothing reads this yet — reserved for a future theming layer. |
| `show_elapsed` | bool | `true` | ⚠️ Reserved — show elapsed time on task rows. Not yet consumed. |
| `show_icons` | bool | `true` | ⚠️ Reserved — show status icons. Not yet consumed. |
| `cleanup_worktrees` | bool | `true` | ⚠️ Reserved — auto-remove worktrees on task delete. Not yet consumed (worktrees are currently always cleaned up). |

#### `[keybindings]` ⚠️

All keybindings are **reserved**: they're loaded into config but the TUI key routing is still hardcoded, so setting them has no effect yet. This is the "more robust config backend → remap hotkeys" work that's planned, not shipped.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `new` | string | `"n"` | New task. |
| `attach` | string | `"enter"` | Attach to / open the selected task's agent. |
| `status` | string | `"s"` | Advance task status. |
| `delete` | string | `"d"` | Delete task. |
| `quit` | string | `"q"` | Quit. |
| `help` | string | `"?"` | Help overlay. |
| `filter` | string | `"/"` | Filter the task list. |
| `prompt` | string | `"p"` | Open the prompt modal. |
| `worktree` | string | `"w"` | Worktree action. |

#### `[sandbox]`

macOS `sandbox-exec` (SBPL) controls for agent processes.

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `false` | Wrap agent processes in a per-session SBPL profile. |
| `deny_read` | []string | `[]` | Paths denied read access, on top of the always-denied `~/.gnupg`, `~/.aws`, `~/.kube`, `~/.config/gcloud`. |
| `extra_write` | []string | `[]` | Additional writable paths. |
| `allow_apple_events` | []string | `[]` | CFBundleIdentifiers allowed as AppleEvent destinations (e.g. `com.apple.iChat`) — required to script Messages/Finder from a sandboxed agent. |

#### `[kb]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `false` | Run the knowledge-base MCP server. |
| `http_port` | int | `7742` | KB server port. |
| `metis_vault_path` | string | iCloud Metis vault | Obsidian vault indexed for the KB. |

Full enable/verify walkthrough: **[docs/knowledge-base.md](docs/knowledge-base.md)**.

#### `[api]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `false` | Run the HTTP REST API + PWA for remote control. |
| `http_port` | int | `7743` | API port (binds `127.0.0.1` + the Tailscale IP only — never `0.0.0.0`). |

#### `[hera]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `true` | Enable [native Hera](#hera-native-multi-agent-coordination) — serves the `hera_*` MCP tools in-process, backed by the `hera_*` tables in `data.sql`. Set `false` to serve the external Hera *plugin*'s tools instead (its `~/.hera` state is independent; no migration is performed). The TUI's second tab is always the native Hera view regardless of this flag. |

#### `[supervisor]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `enabled` | bool | `true` | Drive agent PTYs through the out-of-process [session-supervisor](#daemon--session-supervisor) so the daemon can bounce without interrupting agents. Set `false` to **roll back** to the legacy in-process runner (daemon owns the PTYs); retained one release as a supported fallback. config.toml wins over the DB. |

#### `[argus]`

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `source_path` | string | `""` | Local clone of the Argus repo used by the self-update (`go install`) flow. |

#### Example

```toml
[defaults]
backend = "claude"
permission_mode = "bypass-active"

[ui]
spinner_style = "braille"
default_agent_zoom = true

# Maps merge by key; an existing key is replaced wholesale, so list every
# field you want to keep.
[backends.claude]
command = "claude"
prompt_flag = ""

[projects.argus]
path = "/Users/me/code/argus"
branch = "master"

[projects.argus.sandbox]
enabled = true
denyread = ["~/.ssh"]   # note: lowercased, not deny_read

[sandbox]
enabled = false
deny_read = ["~/.gnupg", "~/.aws"]

[api]
enabled = true
http_port = 7743
```
