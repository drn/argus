## ADDED Requirements

### Requirement: Idempotent antigravity MCP server registration

Argus SHALL register itself in agy's `~/.gemini/config/mcp_config.json` as `mcpServers.argus = {"serverUrl": "<localhost url on the configured port>", "disabled": false}` at daemon startup, only when agy is installed (`agy` or `antigravity` on PATH, or `~/.gemini` already present). Registration SHALL be idempotent (rewrite only when the entry is absent or points elsewhere), SHALL carry over an existing entry's `disabled` flag so an operator's `agy mcp disable argus` is respected, SHALL preserve every unrelated key, SHALL create the directory and file if missing, and SHALL leave an unparseable file untouched and report an error.

#### Scenario: Entry created
- **WHEN** injection runs against a config with no `argus` entry
- **THEN** `mcpServers.argus` carries the localhost url and `disabled` false

#### Scenario: Idempotent on repeat
- **WHEN** injection runs twice with the same port
- **THEN** the file is not rewritten the second time

#### Scenario: Not installed means no writes
- **WHEN** neither agy binary is on PATH and `~/.gemini` does not exist
- **THEN** no file under `~/.gemini` is created

#### Scenario: Operator disable respected
- **WHEN** the `argus` entry points at the current url with `disabled` true
- **THEN** the file is left unchanged

#### Scenario: Invalid JSON left untouched
- **WHEN** the existing file is not valid JSON
- **THEN** it is unchanged and an error is returned
