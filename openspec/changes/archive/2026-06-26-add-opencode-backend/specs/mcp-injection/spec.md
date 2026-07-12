## ADDED Requirements

### Requirement: Idempotent opencode MCP server registration

Argus SHALL register itself as an MCP server in opencode's
`~/.config/opencode/opencode.json` under the `mcp` object as an `argus` entry of
the form `{"type": "remote", "url": "<localhost url on the configured port>",
"enabled": true}`. Registration SHALL be idempotent: the file SHALL be rewritten
only when the entry is absent or its url points at a different port. All
unrelated keys (including other `mcp` entries and top-level config) SHALL be
preserved verbatim. The `~/.config/opencode` directory and the config file SHALL
be created if missing. When the file exists but is not valid JSON, the system
SHALL leave it untouched and report an error rather than overwriting it.
`InjectGlobal` SHALL resolve the config path as `~/.config/opencode/opencode.json`.

#### Scenario: Entry created with remote type and url

- **WHEN** injection runs against an opencode config that has no `argus` mcp entry
- **THEN** an `mcp.argus` entry is written with `type` "remote", a localhost url on the configured port, and `enabled` true

#### Scenario: Idempotent on repeat with same port

- **WHEN** injection runs twice with the same port
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
