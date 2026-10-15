## Why

Argus runs Claude, Codex, Pi, opencode, and Antigravity sessions. The author wants Cursor's CLI (`cursor-agent`, also linked as `agent`; Homebrew cask `cursor-cli`) as a sixth backend. The backend family registry makes this one `Family` entry plus a capture function and an MCP injector.

## What Changes

Verified against `cursor-agent` 2026.10.14 (installed locally; **not signed in**, so a live chat could not be run — see Risks):

- **Detection**: first-word basename `cursor-agent` only. The installer also links a bare `agent`, which is too generic to claim (it stays a custom command).
- **Default backend** `cursor`: command `cursor-agent --approve-mcps`, prompt delivered after `--`. `--trust` is a family `LaunchFlag` so an unattended worktree never blocks on the workspace-trust prompt.
- **Sessions are capture-style.** No start-time ID flag. Chats live at `~/.cursor/chats/<md5(abs cwd)>/<uuid>/store.db`; post-exit capture lists that dir for the canonicalized worktree and takes the newest `<uuid>` by mtime (no SQLite read needed). Resume: `--resume <id>`. Captured once (no in-session conversation switch is known).
- **Model**: `--model <id>` (custom ids; `agent models` needs a signed-in account, so no curated list).
- **Permission modes** from `defaults.permission_mode`: bypass-active → `--force`, plan / bypass-allow → `--plan`, accept-edits / default → nothing (Cursor has no accept-edits mode; it uses an allowlist). Skipped when the command already names `--force`, `--yolo`, `--plan`, or `--mode`.
- **MCP**: no per-launch MCP flag (only `.cursor/mcp.json` / `~/.cursor/mcp.json`). Argus idempotently registers `mcpServers.argus.url` in `~/.cursor/mcp.json` at daemon startup (only when Cursor is installed) and before each launch; the default command carries `--approve-mcps` so the server is not blocked on an approval prompt. Same accepted global-reach trade-off as Antigravity.
- **Routing**: prompt prefix (Cursor reads `AGENTS.md` natively; no CLAUDE.md injection), as opencode.
- **Sandbox**: write grants `~/.cursor/{chats,projects,statsig-cache.json}`; `mcp.json` and `cli-config.json` stay read-only so a sandboxed agent cannot rewrite config an unsandboxed Cursor trusts.

## Non-Goals (named follow-ups)

- Builtin skills delivery (Cursor skills discovery is unverified; `SkillsNone`). Skills self-gate and are optional.
- Per-account isolation (`CURSOR_DATA_DIR` exists but its scope vs. auth is unverified), usage-budget routing, coordinator Stop-hook context size.
- Claude session switcher stays Claude-only.

## Risks

- Capture layout (`md5` of cwd, `store.db` per chat) is read from the CLI bundle, not observed live; the task's Verify step runs after `agent login`. If cwd hashing uses an unresolved path, capture also tries the non-canonical worktree path.

## Capabilities

### Modified Capabilities

`agent-execution`, `config-management`, `mcp-injection`, `sandbox-execution`.

## Impact

New `internal/agent/cursor.go`, `internal/inject/cursor`; edits in `internal/backendkind`, `internal/agent/backends.go`, `internal/config/config.go`, `internal/daemon/surface.go` (spawn-surface bump), README backend table, `context/knowledge/backends.md`. Frontends read `/api/backends` metadata already; macOS name fallbacks only for old daemons — no client change beyond verification (parity noted).
