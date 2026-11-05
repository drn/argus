## Why

Argus runs Claude, Codex, Pi, opencode, Antigravity, and Cursor sessions. The author wants Oh My Pi (`omp`, https://omp.sh, Homebrew `can1357/tap/omp`) as a seventh backend. omp is a Pi fork with its own state dir (`~/.omp`), its own session-dir encoding, Claude-style plugins, and a config overlay flag, so it needs its own family rather than riding on Pi's.

## What Changes

Verified against `omp` 18.16.0 (installed locally; probes run under a scratch `HOME` because the Argus sandbox blocks `~/.omp` writes, which itself confirmed the grant below):

- **Detection**: first-word basename `omp`.
- **Default backend** `omp`: command `omp`, prompt after `--` (verified: `omp -p -- "-reply…"` delivers a leading-dash prompt).
- **Sessions are capture-style.** No start-time session-ID flag. Sessions live at `~/.omp/agent/sessions/<dir>/<ISO-ts>_<uuidv7>.jsonl`, where `<dir>` follows omp's own `getDefaultSessionDir`: a cwd under `os.tmpdir()` → `-tmp-<rel>`; under `$HOME` → `-<rel>` (e.g. `-.argus-worktrees-argus-foo`, observed live); otherwise Pi's `--<abs>--`, with `/`, `\`, `:` → `-` on the realpath. Capture takes the newest top-level session file (subagent subdirs and `.lock` files ignored), checking the primary encoding then the absolute fallback. Resume: `--resume <id>`. omp has `/new`, so the ID is recaptured on every exit and refreshed at resume (like agy/Claude).
- **Model**: `--model <id>` (fuzzy match; no curated list).
- **Permission modes** from `defaults.permission_mode` → `--approval-mode`: bypass-active → `yolo`, accept-edits → `write`, default / plan / bypass-allow → `always-ask` (omp's own default is `yolo`, so Argus's prompting default asks explicitly). Skipped when the command already names `--approval-mode`, `--auto-approve`, or `--yolo`.
- **MCP (child-scoped)**: omp has no MCP flag or env var, but it loads Claude-style plugins from `--plugin-dir` and their `.mcp.json` servers (verified: "Connected to MCP server: argus-mcp:argus"). Argus materializes a content-addressed plugin under `~/.argus/cache/omp-mcp/<hash>/argus-mcp/` holding `.claude-plugin/plugin.json` + `.mcp.json` (`type: http`, the per-port URL, Argus's client timeout in ms) and passes `--plugin-dir` per launch. No global config is written.
- **Skills (child-scoped)**: `--config <overlay.yml>` with `skills.customDirectories: [<argus builtin skills dir>]` (verified: `/skill:argus-zzprobe` autocompletes from the overlay dir). Overlay materialized content-addressed under `~/.argus/cache/omp-config/<hash>/`. omp invokes skills as `/skill:<name>`; the family's autocomplete trigger stays `/`, as Pi's does.
- **Routing**: `--append-system-prompt <file>` (same as Pi; omp appends "text or file contents"). omp reads `AGENTS.md`/`CLAUDE.md` natively, so no instruction prefix.
- **Sandbox**: write grant `~/.omp` (agent.db/history.db/models.db with WAL, sessions, logs, run, natives, cache, config lock at the root — too spread to scope to subdirs), emitted **launch-only** so other backends' sandboxed agents cannot rewrite `~/.omp/agent/mcp.json` / `config.yml`.

## Non-Goals (named follow-ups)

- Per-account isolation (`--profile` / `OMP_PROFILE` / `PI_CODING_AGENT_DIR` move the agent dir; capture reads only the default `~/.omp/agent`).
- Usage-budget routing (`omp usage` exists but is unprobed), cost accrual (`CostSource` none → "n/a").
- Claude session switcher stays Claude-only.
- De-duplicating Argus MCP registrations omp also discovers from other CLIs' global configs (`~/.cursor/mcp.json`, opencode, gemini) when those families have been injected — the plugin server is namespaced `argus-mcp:argus`, so it never collides by name.

## Risks

- The session-dir encoding is read from the compiled bundle (`Si`/`fi`/`yi`) and matched against one live dir; a future omp may change it. Capture fails open (keeps the task's existing ID) and tries both encodings.
- The `--config` overlay's `skills.customDirectories` may replace rather than extend a user's own list for that run.

## Capabilities

### Modified Capabilities

`agent-execution`, `config-management`, `mcp-injection`, `sandbox-execution`, `skill-provisioning`, `routing-provisioning`.

## Impact

New `internal/agent/omp.go`; edits in `internal/backendkind`, `internal/agent/backends.go` (+ new `SkillsConfigOverlay` delivery and the omp family), `internal/agent/agent.go` (one BuildCmd case), `internal/config/config.go`, `internal/daemon/surface.go` (spawn-surface bump), MCP/hera backend text, README backend table, `context/knowledge/backends.md`. Frontends read `/api/backends` metadata; no client change.
