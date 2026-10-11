# Backend families

Every agent CLI Argus runs is a **family**: static facts in `internal/backendkind` (kind, binaries,
label, skill prefix) and behavior in `agent.Family` (`internal/agent/backends.go`). Unrecognized
commands get `customFamily`. To add one, follow `.agents/skills/add-backend/SKILL.md`.

## Capability matrix

| | claude | codex | pi | opencode | antigravity | custom |
|---|---|---|---|---|---|---|
| Binaries | `claude` | `codex` | `pi` | `opencode` | `agy`, `antigravity` | anything else |
| Session ID | pinned `--session-id` | captured | captured | captured | captured | pinned |
| Resume | `--resume <id>` | `codex resume <id>` (replaces cmd) | `--session <id>` | `--session <id>` | `--conversation <id>` | `--resume <id>` |
| Capture source | `~/.claude/projects/<enc>/*.jsonl` | `state_5.sqlite` `threads.cwd` | `~/.pi/agent/sessions/--<enc>--/` | `opencode.db` `session[_v2].directory` | `~/.gemini/antigravity-cli/conversation_summaries.db` `workspace_uris` | — |
| Recapture | every exit + resume refresh | once | once | once | every exit + resume refresh | never |
| Prompt | `-- <p>` | `-- <p>` | positional | `--prompt` (config) | `--prompt-interactive` (family default) | `-- <p>` |
| Model | `--model` (curated aliases) | `--model` (curated) | `--model` | `OPENCODE_CONFIG_CONTENT` | `--model` (custom only) | none |
| Permission | `defaults.permission_mode` → `--permission-mode …` | baked into command | — | `--auto` launch flag | mode → `--mode …` / `--dangerously-skip-permissions` | — |
| MCP | `--mcp-config` per launch | `-c mcp_servers.argus.url` per launch | — | global `opencode.json` | global `~/.gemini/config/mcp_config.json` | — |
| Skills | `--add-dir ~/.argus/skills` | Argus-only `CODEX_HOME` overlay | `--skill <dir>` | `OPENCODE_CONFIG_CONTENT` | global `~/.gemini/config/skills.json` entry | — |
| Routing | `--append-system-prompt-file` | prompt prefix | — | prompt prefix | prompt prefix | — |
| Sandbox grant | `~/.claude.json*`, `~/.claude` | `~/.codex`, codex-home | `~/.pi` | (base `~/.local`) | `~/.gemini` | — |
| Accounts | `claude_config_dir` | `codex_home` | — | — | — | — |
| Session switcher | yes | — | — | — | — | yes |
| Prelaunch | — | — | ollama warmup (20s Start RPC) | — | — | — |

## Why the non-obvious cells are what they are

- **antigravity is global-config only.** `agy` reads only `$HOME/.gemini/config` (no env
  override; `AGY_ACCOUNT` is undocumented), so MCP and skills are injected at daemon startup. Its
  own `agy mcp add` writes `serverUrl`; Argus writes the same key and accepts `url` too.
- **antigravity rejects positional prompts** ("Prompts are read only from -p/--print,
  -i/--prompt-interactive, or stdin"), hence the family-level `DefaultPromptFlag` that survives a
  stored backend row with an empty prompt flag.
- **antigravity recaptures every exit** because `/new` starts a new conversation; the resume-time
  refresh (`RefreshResumeSessionID`) covers hera workers that idle instead of exiting.
- **custom keeps Claude-style pinning** so test fixtures and wrapper scripts behave as before the
  registry; it never receives Claude-only flags.
- **bypass-allow → `--mode plan` for agy**: agy has no "plan with bypass reachable" mode.
