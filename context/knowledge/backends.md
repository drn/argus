# Backend families

Every agent CLI Argus runs is a **family**: static facts in `internal/backendkind` (kind, binaries,
label, skill prefix) and behavior in `agent.Family` (`internal/agent/backends.go`). Unrecognized
commands get `customFamily`. To add one, follow `.agents/skills/add-backend/SKILL.md`.

## Capability matrix

| | claude | codex | pi | opencode | antigravity | cursor | custom |
|---|---|---|---|---|---|---|---|
| Binaries | `claude` | `codex` | `pi` | `opencode` | `agy` | `cursor-agent` | anything else |
| Session ID | pinned `--session-id` | captured | captured | captured | captured | captured | pinned |
| Resume | `--resume <id>` | `codex resume <id>` (replaces cmd) | `--session <id>` | `--session <id>` | `--conversation <id>` | `--resume <id>` | `--resume <id>` |
| Capture source | `~/.claude/projects/<enc>/*.jsonl` | `state_5.sqlite` `threads.cwd` | `~/.pi/agent/sessions/--<enc>--/` | `opencode.db` `session[_v2].directory` | `~/.gemini/antigravity-cli/conversation_summaries.db` `workspace_uris` | `~/.cursor/chats/<md5(cwd)>/<uuid>/` newest mtime | — |
| Recapture | every exit + resume refresh | once | once | once | every exit + resume refresh | once | never |
| Resume availability | exact account transcript | — | — | — | — | — | — |
| Prompt | `-- <p>` | `-- <p>` | positional | `--prompt` (config) | `--prompt-interactive` (family default) | `-- <p>` | `-- <p>` |
| Model | `--model` (curated aliases) | `--model` (curated) | `--model` | `OPENCODE_CONFIG_CONTENT` | `--model` (custom only) | `--model` (custom only) | none |
| Permission | `defaults.permission_mode` → `--permission-mode …` | baked into command | — | `--auto` launch flag | mode → `--mode …` / `--dangerously-skip-permissions` | mode → `--force` / `--plan` | — |
| MCP | `--mcp-config` per launch | `-c mcp_servers.argus.url` per launch | — | global `opencode.json` | global `~/.gemini/config/mcp_config.json` | global `~/.cursor/mcp.json` (+`--approve-mcps` in command) | — |
| Skills | `--add-dir ~/.argus/skills` | Argus-only `CODEX_HOME` overlay | `--skill <dir>` | `OPENCODE_CONFIG_CONTENT` | global `~/.gemini/config/skills.json` entry | — | — |
| Routing | `--append-system-prompt-file` | `AGENTS.md` in the Argus-only `CODEX_HOME` overlay | `--append-system-prompt <file>` | `instructions` in `OPENCODE_CONFIG_CONTENT` | always-on global rule `~/.gemini/config/rules/argus-routing.md` | — (no native mechanism; see gotchas) | — |
| Sandbox grant | `~/.claude.json*`, `~/.claude` | `~/.codex`, codex-home | `~/.pi` | (base `~/.local`) | `~/.gemini/{antigravity-cli,config,jetski}` (pre-created) | `~/.cursor/{chats,projects,statsig-cache.json}` (launch-only, pre-created; mcp.json/cli-config.json read-only) | — |
| Accounts | `claude_config_dir` | `codex_home` | — | — | — | — | — |
| Launch flag | — | — | — | `--auto` | — | `--trust` | — |
| Session switcher | yes | — | — | — | — | — | yes |
| Bridge mod | `--plugin-dir` | — | — | — | — | — | — |
| Prelaunch | — | — | ollama warmup (20s Start RPC) | — | — | — | — |
| Cost source | Stop hook + bridge mod | daemon rollout sweeper | — (n/a) | — (n/a) | — (n/a) | — (n/a) | Stop hook |

Rows map to `Family` fields: Session ID → `PinsSessionID`, Resume → `ResumeFlag`/`ResumeCmd`,
Capture → `Capture`, Recapture → `RecaptureEveryExit`, Resume availability → `ResumeAvailable`, Prompt → `DefaultPromptFlag`/
`PromptPositional`, Model → `ModelDelivery`/`KnownModels`, Permission →
`PermissionFlags`/`PermissionFlagTokens`, Launch flag → `LaunchFlag`, MCP → `MCPFlag`/`InjectGlobal`,
Skills → `SkillDelivery`, Routing → `RoutingDelivery`, Sandbox grant → `SandboxGrants`/
`SandboxGrantsLaunchOnly`/`PrecreateSandboxGrants`, Session switcher → `SessionSwitcher`,
Prelaunch → `Prelaunch`/`StartTimeout`, Bridge mod → `BridgeMod`, Cost source → `CostSource`.

## Why the non-obvious cells are what they are

- **Codex cost comes from the daemon, not the session.** Codex runs neither the Claude Stop hook nor the bridge mod, so `internal/codexcost` (a 60s daemon sweeper) reads the task's Codex rollout files via `state_5.sqlite` `threads.cwd` and prices each call into the hera binding. Pi/opencode/agy/cursor have no cost source yet — their roles show "n/a" (named follow-up in `add-codex-cost-accrual`).

- **Bridge mod is Claude-only.** Mods are a Claude Code feature; BuildCmd appends `--plugin-dir <argus-bridge dir>` for `Family.BridgeMod` families when `[bridge] enabled`. Every other family keeps screen-reading as its state source. See `gotchas/bridge.md`.

- **antigravity is global-config only.** `agy` reads only `$HOME/.gemini/config` (no env
  override; `AGY_ACCOUNT` is undocumented), so MCP and skills are injected at daemon startup —
  only when agy is installed (on PATH or `~/.gemini/antigravity-cli` exists — Gemini CLI also owns
  `~/.gemini`), and refreshed before every agy launch. Its own `agy mcp add` writes `serverUrl`; Argus writes the same key,
  accepts `url` too, and carries over an operator's `disabled: true`.
- **antigravity capture ignores sub-agent rows** (non-empty `parent_conversation_id`): they share
  the worktree and are often newer, but only the top-level conversation is the task's.
- **antigravity rejects positional prompts** ("Prompts are read only from -p/--print,
  -i/--prompt-interactive, or stdin"), hence the family-level `DefaultPromptFlag` that survives a
  stored backend row with an empty prompt flag.
- **antigravity recaptures every exit** because `/new` starts a new conversation; the resume-time
  refresh (`RefreshResumeSessionID`) covers hera workers that idle instead of exiting.
- **custom keeps Claude-style pinning** so test fixtures and wrapper scripts behave as before the
  registry; it never receives Claude-only flags.
- **bypass-allow → `--mode plan` for agy**: agy has no "plan with bypass reachable" mode.
- **antigravity's sandbox grant is three subdirs, not `~/.gemini`**: Gemini CLI keeps OAuth creds at
  the root. A sandboxed agy can't `mkdir ~/.gemini` itself, so BuildCmd pre-creates the grant dirs
  unsandboxed for families that set `PrecreateSandboxGrants` (only agy), and `SandboxGrantsLaunchOnly`
  emits those grants only into agy's own launches.
- **cursor is capture-style with no sqlite read.** `cursor-agent` has no start-time session-id flag;
  chats live at `~/.cursor/chats/<md5(abs cwd)>/<uuid>/store.db`, so capture lists that dir (canonical
  and as-given worktree path) and takes the newest uuid dir. Layout was read from the CLI bundle, not
  observed live (unsigned-in at build time) — re-verify after `agent login`.
- **Only `cursor-agent` classifies as cursor**: the installer also links a bare `agent`, too generic to claim.
- **cursor MCP has no per-launch flag**: `~/.cursor/mcp.json` is edited at daemon start (only if installed)
  and before each launch; `--approve-mcps` lives in the default command, not the family, so a user who
  strips it still gets the prompt. `--trust` is the family LaunchFlag.
- **Only `agy` classifies as antigravity** — a bare `antigravity` may be the IDE launcher.
