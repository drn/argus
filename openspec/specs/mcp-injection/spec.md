# MCP Config Injection

## Purpose

This capability gives Argus-launched Claude Code and Codex tasks access to the Argus MCP server through process-scoped launch arguments. The daemon removes its old user-wide entries while preserving unrelated MCP configuration. Opencode retains its existing global registration.
## Requirements
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

### Requirement: Argus MCP is scoped to Argus task sessions

When the MCP listener is running, Argus SHALL pass its actual listener URL to each Claude Code and Codex process it launches for a task. Claude SHALL receive the server through `--mcp-config`; Codex SHALL receive it through `-c mcp_servers.argus.url`. The argument SHALL be present for fresh sessions, conversation resumes, rerender restarts, and coordinator recycles, whether the session runs in-process or in the session supervisor. Other user-configured MCP servers SHALL remain available. When no listener is running, Argus SHALL omit the argument.

#### Scenario: Fresh and resumed Claude task

- **WHEN** Argus launches or resumes a Claude task while the MCP server is listening
- **THEN** its command includes an Argus `--mcp-config` entry pointing at the actual listener port

#### Scenario: Fresh and resumed Codex task

- **WHEN** Argus launches or resumes a Codex task while the MCP server is listening
- **THEN** its command includes a Codex `-c` override for `mcp_servers.argus.url` pointing at the actual listener port

#### Scenario: No MCP listener

- **WHEN** Argus launches a task with the MCP server disabled or unavailable
- **THEN** the command does not configure an Argus MCP server

### Requirement: Remove legacy global registration

At daemon startup, Argus SHALL remove only its `argus` and `argus-kb` MCP entries from Claude's and Codex's user-wide configuration. It SHALL preserve unrelated entries and settings, leave malformed files untouched with a logged error, and avoid creating missing files. Argus SHALL no longer set `enableAllProjectMcpServers` in Claude's global settings.

#### Scenario: Existing global entries

- **WHEN** a user-wide config contains Argus and unrelated MCP servers
- **THEN** Argus removes its entries and preserves the unrelated servers

#### Scenario: Missing global config

- **WHEN** a user-wide config file does not exist
- **THEN** cleanup does not create it
