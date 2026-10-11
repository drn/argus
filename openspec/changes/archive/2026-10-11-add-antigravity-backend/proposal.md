## Why

Argus runs Claude, Codex, Pi, and opencode sessions. The author wants Google's Antigravity CLI (`agy`, Homebrew cask `antigravity-cli`) as a fifth backend at full parity. Adding the fourth backend (opencode) meant touching ~15 files of hand-rolled `IsCodex || IsPi || IsOpencode` branches plus hardcoded name lists in the web SPA and macOS app. A fifth backend should be one registry entry, not another sweep.

## What Changes

- **Backend family registry (refactor).** A leaf package `internal/backendkind` classifies a command into a stable kind (`claude`, `codex`, `pi`, `opencode`, `antigravity`, or custom). `internal/agent` holds one `Family` descriptor per kind declaring every per-backend capability: session pinning, resume form, post-exit capture and recapture policy, model delivery, skill delivery, routing delivery, prompt delivery, per-launch MCP flag, permission-mode flags, launch flag injection, prelaunch, start timeout, global config injection, and sandbox state-dir grants. Every call site (BuildCmd, CaptureSessionID, NeedsSessionRecapture, RefreshResumeSessionID, create.go, TUI, REST, daemon client, prelaunch, config.AccountSupports, profiles, sandbox, daemon startup injection) reads the registry. Existing backends' launched commands stay byte-identical.
- **REST exposes the family.** `GET /api/backends` adds `kind`, `label`, `skill_prefix`, and `session_switcher` per backend. The web SPA and macOS app read these instead of hardcoded backend-name lists.
- **Antigravity backend** (all verified against `agy` 1.3.3):
  - Detected by first-word basename `agy` (or the cask's raw `antigravity` binary).
  - Default backend entry `antigravity`: command `agy`, prompt flag `--prompt-interactive` (agy rejects a positional prompt). The family also defaults to `--prompt-interactive` when a stored backend has no prompt flag.
  - Permission mode mapped from `defaults.permission_mode`: bypass-active → `--dangerously-skip-permissions`, accept-edits → `--mode accept-edits`, plan / bypass-allow → `--mode plan`, default → nothing. Skipped if the command already names one.
  - Model via `--model` (custom ids only; `agy models` needs a signed-in account, so no curated list).
  - Capture-style sessions: no start-time ID; post-exit capture reads `~/.gemini/antigravity-cli/conversation_summaries.db` (`conversation_id` whose `workspace_uris` contains the worktree, newest `last_modified_time`); re-captured on every exit and refreshed at resume, since a `/new` in agy starts a new conversation. Resume: `--conversation <id>`.
  - MCP: idempotent global registration in `~/.gemini/config/mcp_config.json` (`mcpServers.argus.serverUrl`) at daemon startup — agy has no per-launch MCP flag or config-dir override.
  - Skills: idempotent global `~/.gemini/config/skills.json` entry pointing at Argus's materialized builtin skills dir (agy's documented global manifest; no per-process hook exists). Builtin skills self-gate on argus sandbox residency.
  - Routing context prepended to the first prompt (Codex/opencode parity); agy reads `AGENTS.md`/`GEMINI.md` natively, so no CLAUDE.md injection.
  - Sandbox grants writes to `~/.gemini`.
- **New project skill** `.agents/skills/add-backend/` documenting every capability a new backend must declare, with a checklist and verification steps; plus `context/knowledge/backends.md` reference.

## Non-Goals (named follow-ups)

- Antigravity per-account isolation (agy has no config-dir override; `AGY_ACCOUNT` is undocumented).
- Antigravity usage-budget probe / tier routing (no CLI usage surface).
- Coordinator context-size Stop hook for agy (`hooks.json` Stop exists but its payload differs from Claude's; separate change).
- Claude session switcher stays Claude-only by design.

## Capabilities

### Modified Capabilities

- `agent-execution`: registry-driven backend dispatch; antigravity pinning/resume/capture/permission/prompt rules; resume-time refresh generalized to every recapture-every-exit family.
- `config-management`: built-in `antigravity` backend.
- `mcp-injection`: idempotent antigravity MCP registration.
- `sandbox-execution`: `~/.gemini` write grant; backend grants come from the registry.
- `skill-provisioning`: antigravity global skills manifest entry.
- `rest-api`: backend listing carries family metadata.
- `llm-backends`: backend family classification.

## Impact

- Code: new `internal/backendkind`, `internal/agent/backends.go`, `internal/agent/antigravity.go`, `internal/inject/antigravity`; edits in `internal/agent/{agent,create,prelaunch,resume,sandbox}.go`, `internal/config/{config,accounts}.go`, `internal/daemon/{daemon,surface}.go`, `internal/daemon/client/client.go`, `internal/api/{handlers,claudesessions}.go`, `internal/tui/{app,newtaskform}.go`, `internal/api/static/index.html` (+ `sw.js` bump), `macos/Sources/...`.
- Supervisor spawn surface bump (BuildCmd behavior changes for a new backend family).
- Frontend parity: all three clients consume the new REST metadata in this PR.
