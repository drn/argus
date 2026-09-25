## REMOVED Requirements

### Requirement: Idempotent Claude MCP server registration

### Requirement: Idempotent Codex MCP server registration

### Requirement: Legacy argus-kb entry migration

### Requirement: Claude project MCP trust suppression

## ADDED Requirements

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
