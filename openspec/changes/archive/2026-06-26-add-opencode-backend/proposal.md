# Add opencode as a first-class backend

## Why

The README already markets opencode as a supported harness ("Claude Code, Codex,
opencode, or a local model via ollama"), but no code backs that claim — opencode
is not in the seeded backends, not detected by `agent`, and not MCP-injected. A
user who picks opencode today gets an unconfigured backend.

opencode is a templated-command CLI like the existing backends, so it fits
Argus's "every backend is just a command" model. The only non-trivial wiring is
session handling: opencode mints its own session IDs (it has **no start-time
`--session-id`**), so it joins the codex/pi "capture-post-exit" family rather
than the Claude "pin-at-start" family, and resumes via `opencode --session <id>`.

## What Changes

A single PR adding opencode with full parity to codex/pi:

- **Seeded backend.** `config.DefaultConfig().Backends` gains an `opencode` entry
  (`command = "opencode"`, `prompt_flag = "--prompt"`). The DB `fixupBackends`
  path already inserts newly-shipped defaults into existing databases, so
  upgraders pick it up with no schema bump. Permissions are deliberately left to
  the user's own opencode config — the bare command is the default.
- **Backend detection.** New `agent.IsOpencodeBackend(command)` (basename
  `opencode`), mirroring `IsCodexBackend` / `IsPiBackend` / `IsClaudeBackend`.
- **Command construction.** opencode joins the "no start-time session-id" family
  (like codex/pi); the prompt rides the configured `--prompt` flag (existing
  prompt-flag path, no new branch); `--model` injection extends to opencode (its
  `provider/model` value is passed through verbatim); resume appends
  `--session <id>` (identical shape to pi).
- **Post-exit session capture + resume.** `CaptureOpencodeSessionID(worktree)`
  recovers the most-recently-updated opencode session for the task's worktree so
  a conversation survives a daemon restart. opencode keys sessions by git
  root-commit (shared across worktrees), so capture filters by the per-session
  **directory** field, newest by **updated time**. Current opencode (v1.14+)
  stores this in SQLite (`~/.local/share/opencode/opencode.db`); older opencode
  uses JSON files under `~/.local/share/opencode/storage/session/`. Capture tries
  SQLite first, falls back to the JSON walk, and **fails open** (no resume) when
  neither yields a match. opencode is added to `CaptureSessionID` /
  `NeedsSessionRecapture` (recapture only while the ID is still empty, like
  codex/pi).
- **MCP injection.** New `internal/inject/opencode` writes the `mcp.argus` remote
  entry into `~/.config/opencode/opencode.json` (`{type:"remote", url, enabled}`),
  wired into the daemon's startup injection alongside Claude/Codex. Only the MCP
  entry is touched — the user's permission posture is never modified.
- **UI/UX parity.** The new-task model selector offers default + custom typing for
  opencode (no curated `KnownModels` list — the `provider/model` space depends on
  the user's authenticated providers; a curated list is reachable via the
  backend's `models` config field). The ctrl+r Claude session switcher stays
  Claude-only (opencode excluded). Session-exit logging tags `kind = "opencode"`.

Non-goals: no opencode permission auto-configuration; no opencode-specific
prelaunch (that is pi/ollama only); no new keybindings.

## Impact

- Affected specs: `agent-execution` (session pinning/resume + post-exit capture
  families gain opencode), `mcp-injection` (new opencode registration),
  `config-management` (default backends + per-backend model list).
- Affected code: `internal/config/config.go`, `internal/agent/agent.go`,
  `internal/agent/create.go`, `internal/inject/opencode/` (new),
  `internal/daemon/daemon.go`, `internal/tui/app.go`, plus tests and README
  Reference table.
- No schema change. No breaking change to existing backends.
