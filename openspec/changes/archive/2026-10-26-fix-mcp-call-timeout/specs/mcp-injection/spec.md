## MODIFIED Requirements

### Requirement: Idempotent opencode MCP server registration

Argus SHALL register itself as an MCP server in opencode's
`~/.config/opencode/opencode.json` under the `mcp` object as an `argus` entry of
the form `{"type": "remote", "url": "<localhost url on the configured port>",
"enabled": true, "timeout": 150000}`. Registration SHALL be idempotent: the file SHALL be rewritten
only when the entry is absent, its url points at a different port, or its timeout differs from the shared client deadline, and an
existing entry's `enabled: false` SHALL be carried over, never reset. All
unrelated keys (including other `mcp` entries and top-level config) SHALL be
preserved verbatim. The `~/.config/opencode` directory and the config file SHALL
be created if missing. When the file exists but is not valid JSON, the system
SHALL leave it untouched and report an error rather than overwriting it.
`InjectGlobal` SHALL resolve the config path as `~/.config/opencode/opencode.json`.

#### Scenario: Entry created with remote type and url

- **WHEN** injection runs against an opencode config that has no `argus` mcp entry
- **THEN** an `mcp.argus` entry is written with `type` "remote", a localhost url on the configured port, `enabled` true, and the shared client timeout in milliseconds

#### Scenario: Idempotent on repeat with same port

- **WHEN** injection runs twice with the same port and timeout
- **THEN** the file content is identical after the second run

#### Scenario: Port change replaces the old url

- **WHEN** injection runs with a different port than the existing entry
- **THEN** the entry's url uses the new port and the old port no longer appears in the file

#### Scenario: Existing content preserved

- **WHEN** the config already contains unrelated top-level settings and other `mcp` entries
- **THEN** those settings and entries remain after the `argus` entry is added or updated

#### Scenario: Invalid JSON is left untouched

- **WHEN** the opencode config file exists but does not parse as JSON
- **THEN** the file is not modified and an error is returned

## ADDED Requirements

### Requirement: MCP client deadlines exceed the inbox wait cap

Argus SHALL derive its managed MCP client deadline from the shared maximum `hera_inbox` server wait plus 30 seconds of margin. The inbox cap SHALL remain 120 seconds. When the MCP listener is running, fresh and resumed Claude tasks SHALL receive that deadline in milliseconds as the numeric `timeout` of the Argus server in `--mcp-config` and as `MCP_TOOL_TIMEOUT` in the child environment. Fresh and resumed Codex tasks SHALL receive the same deadline in seconds through `mcp_servers.argus.tool_timeout_sec`. OpenCode's managed global Argus entry SHALL receive the deadline in milliseconds as `timeout`; injection SHALL upgrade missing or stale timeout values while preserving the operator's `enabled` choice and unrelated configuration. With no MCP listener, Claude and Codex SHALL receive no Argus timeout override.

#### Scenario: Fresh and resumed task deadlines

- **WHEN** Argus launches or resumes a Claude or Codex task with an MCP listener
- **THEN** the configured client deadline is 150 seconds and exceeds the inbox wait cap
- **AND** Claude's JSON and environment agree on the deadline

#### Scenario: Upgrade an existing OpenCode entry

- **WHEN** OpenCode's Argus entry has the current URL and a missing or stale timeout
- **THEN** injection updates the timeout to 150000 milliseconds, preserves its enabled choice, and preserves unrelated settings
- **AND** another injection with the same values does not rewrite the file

#### Scenario: No MCP listener

- **WHEN** Argus launches Claude or Codex with no MCP listener
- **THEN** Argus adds no MCP timeout argument or environment override
