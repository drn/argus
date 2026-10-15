## ADDED Requirements

### Requirement: Cursor MCP registration

The system SHALL idempotently register Argus's MCP server as `mcpServers.argus` with `url` in `~/.cursor/mcp.json` at daemon startup (only when Cursor is installed) and before each cursor launch, preserving unrelated keys and an operator's `disabled` choice, leaving an unparseable file untouched, with the default backend command carrying `--approve-mcps`.

#### Scenario: Registration is idempotent and non-destructive
- **WHEN** injection runs twice against a `mcp.json` holding other servers
- **THEN** other servers are unchanged and `argus` appears once with the current URL

#### Scenario: Not installed
- **WHEN** the daemon starts and Cursor is not installed
- **THEN** `~/.cursor` is not written
