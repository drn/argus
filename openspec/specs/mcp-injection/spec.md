# MCP Config Injection

## Purpose

This capability registers the Argus MCP server into the agent backends' own config files so that any Claude Code or Codex session launched on the machine can reach Argus's MCP tools (knowledge base and task tools). Injection edits the user's global backend config — Claude Code's `~/.claude.json` (`mcpServers.argus`) and Codex's `~/.codex/config.toml` (`[mcp_servers.argus]`) — and is idempotent so it can run on every Argus startup without churning the files or disturbing unrelated configuration. It also suppresses Claude Code's first-use project-MCP approval prompt.

## Requirements

### Requirement: Idempotent Claude MCP server registration

Argus SHALL register itself as an HTTP MCP server in Claude Code's config under `mcpServers.argus`, pointing at a localhost URL on the configured port with transport type `http`. Registration SHALL be idempotent: the file SHALL be rewritten only when the entry is absent, its URL points at a different port, or it lacks the `http` transport type. All config keys other than `mcpServers.argus` SHALL be preserved verbatim. A config file that exists but is not valid JSON SHALL NOT be modified, and that condition SHALL surface as an error. A missing config file SHALL be created. `InjectGlobal` SHALL resolve the config path as `~/.claude.json`.

#### Scenario: Entry added with HTTP transport on configured port

- **WHEN** injection runs against a Claude config that has no `argus` entry
- **THEN** an `mcpServers.argus` entry is written with type `http` and a localhost URL on the configured port

#### Scenario: Old format without transport is upgraded

- **WHEN** an existing `argus` entry has a URL but no `type` field
- **THEN** the entry is rewritten to include the `http` transport type

#### Scenario: No rewrite when already correct

- **WHEN** injection runs twice with the same port and the entry is already correct
- **THEN** the second run does not rewrite the file (its modification time is unchanged)

#### Scenario: Rewrite on port change

- **WHEN** injection runs with a different port than the existing entry
- **THEN** the entry's URL is updated to the new port

#### Scenario: Unrelated keys preserved

- **WHEN** the config contains other MCP server entries alongside the `argus` entry
- **THEN** those unrelated entries are preserved after injection

#### Scenario: Malformed config left untouched

- **WHEN** the Claude config file exists but is not valid JSON
- **THEN** injection returns an error and does not overwrite the file

#### Scenario: Missing config file created

- **WHEN** the Claude config file does not exist
- **THEN** injection creates it containing the `argus` MCP entry

### Requirement: Idempotent Codex MCP server registration

Argus SHALL register itself as an MCP server in Codex's `~/.codex/config.toml` under a `[mcp_servers.argus]` section whose `url` points at a localhost URL on the configured port, and SHALL ensure `experimental_use_rmcp_client = true` exists at the TOML top level (before the first section header). Registration SHALL be idempotent: the file SHALL be rewritten only when the section is absent or its URL points at a different port. Unrelated top-level keys and sections SHALL be preserved. The `~/.codex` directory and the config file SHALL be created if missing. `InjectGlobal` SHALL resolve the config path as `~/.codex/config.toml`.

#### Scenario: Section created with url and top-level rmcp flag

- **WHEN** injection runs against a Codex config that has no `argus` section
- **THEN** a `[mcp_servers.argus]` section is written with a localhost url on the configured port, and `experimental_use_rmcp_client = true` is present at the TOML top level

#### Scenario: Idempotent on repeat with same port

- **WHEN** injection runs twice with the same port
- **THEN** the file content is identical after the second run

#### Scenario: Port change replaces the old url

- **WHEN** injection runs with a different port than the existing section
- **THEN** the section's url uses the new port and the old port no longer appears in the file

#### Scenario: Existing content preserved

- **WHEN** the config already contains unrelated top-level settings and other sections
- **THEN** those settings and sections remain after the `argus` section is added

#### Scenario: rmcp flag kept at top level, never inside a section

- **WHEN** the config ends with a section header and the rmcp flag must be added
- **THEN** `experimental_use_rmcp_client` is placed before the first section header, not inside any section

#### Scenario: Misplaced rmcp flag migrated without duplication

- **WHEN** an existing config has `experimental_use_rmcp_client` inside a section
- **THEN** the flag is moved to the top level and appears exactly once

### Requirement: Legacy argus-kb entry migration

Both backends SHALL remove the pre-rename `argus-kb` entry on the next injection, replacing it with the current `argus` entry, while preserving all unrelated entries. This migrates configs written by older Argus builds where the MCP server was named `argus-kb`.

#### Scenario: Legacy Claude key migrated, others preserved

- **WHEN** the Claude config contains a legacy `mcpServers.argus-kb` entry alongside an unrelated MCP entry
- **THEN** the legacy entry is removed, the `argus` entry is added, and the unrelated entry is preserved

#### Scenario: Legacy Codex section migrated, others preserved

- **WHEN** the Codex config contains a legacy `[mcp_servers.argus-kb]` section alongside an unrelated section
- **THEN** the legacy section is removed, the `[mcp_servers.argus]` section is added, and the unrelated section is preserved

### Requirement: Claude project MCP trust suppression

Argus SHALL write `enableAllProjectMcpServers: true` to `~/.claude/settings.json` so Claude Code does not show its first-use project-MCP approval prompt for the injected Argus server. The operation SHALL be idempotent (no rewrite when the flag is already set) and SHALL preserve all existing keys in the settings file, creating the `~/.claude` directory and the file if absent.

#### Scenario: Flag created when absent

- **WHEN** the settings file has no `enableAllProjectMcpServers` flag
- **THEN** the flag is written as `true`

#### Scenario: Idempotent when already set

- **WHEN** the flag is already set to true
- **THEN** a subsequent run does not rewrite the file (its modification time is unchanged)

#### Scenario: Existing settings preserved

- **WHEN** the settings file already contains unrelated keys
- **THEN** those keys are preserved and the trust flag is added alongside them
