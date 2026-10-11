## MODIFIED Requirements

### Requirement: Argus MCP is scoped to Argus task sessions

When the MCP listener is running, Argus SHALL pass its actual listener URL to each Claude Code and Codex process it launches for a task. Claude SHALL receive the server through `--mcp-config`; Codex SHALL receive it through `-c mcp_servers.argus.url`. The argument SHALL be present for fresh sessions, conversation resumes, rerender restarts, and coordinator recycles, whether the session runs in-process or in the session supervisor. Other user-configured MCP servers SHALL remain available. When no listener is running, Argus SHALL omit the argument. Backends whose CLI offers no per-launch MCP mechanism (opencode, antigravity) are an explicit exception: Argus registers itself in that CLI's global configuration instead (see the opencode and antigravity registration requirements), which also exposes the Argus server to sessions of that CLI that Argus did not launch.

#### Scenario: Fresh and resumed Claude task

- **WHEN** Argus launches or resumes a Claude task while the MCP server is listening
- **THEN** its command includes an Argus `--mcp-config` entry pointing at the actual listener port

#### Scenario: Fresh and resumed Codex task

- **WHEN** Argus launches or resumes a Codex task while the MCP server is listening
- **THEN** its command includes a Codex `-c` override for `mcp_servers.argus.url` pointing at the actual listener port

#### Scenario: No MCP listener

- **WHEN** Argus launches a task with the MCP server disabled or unavailable
- **THEN** the command does not configure an Argus MCP server

## ADDED Requirements

### Requirement: Idempotent antigravity MCP server registration

Argus SHALL register itself in agy's `~/.gemini/config/mcp_config.json` as `mcpServers.argus = {"serverUrl": "<localhost url on the configured port>", "disabled": false}` at daemon startup — only when agy is installed (`agy` on PATH, or `~/.gemini/antigravity-cli` already present; bare `~/.gemini` also belongs to Gemini CLI and does not count) — and again before each antigravity launch, unconditionally, since the launch itself proves agy is installed (a launchd daemon's PATH may not include it). Registration SHALL be idempotent (rewrite only when the entry is absent or points elsewhere), SHALL carry over an existing entry's `disabled` flag so an operator's `agy mcp disable argus` is respected, SHALL preserve every unrelated key, SHALL create the directory and file if missing, and SHALL leave an unparseable file untouched and report an error.

#### Scenario: Entry created
- **WHEN** injection runs against a config with no `argus` entry
- **THEN** `mcpServers.argus` carries the localhost url and `disabled` false

#### Scenario: Idempotent on repeat
- **WHEN** injection runs twice with the same port
- **THEN** the file is not rewritten the second time

#### Scenario: Not installed means no writes
- **WHEN** neither agy binary is on PATH and `~/.gemini/antigravity-cli` does not exist (even if `~/.gemini` does)
- **THEN** no file under `~/.gemini` is created

#### Scenario: Operator disable respected
- **WHEN** the `argus` entry points at the current url with `disabled` true
- **THEN** the file is left unchanged

#### Scenario: Invalid JSON left untouched
- **WHEN** the existing file is not valid JSON
- **THEN** it is unchanged and an error is returned
