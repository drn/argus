## ADDED Requirements

### Requirement: Oh My Pi per-launch MCP plugin

When the MCP server is enabled, the system SHALL deliver Argus's MCP server to omp launches through `--plugin-dir <dir>`, where `<dir>` is a content-addressed Claude-style plugin directory under `~/.argus/cache/omp-mcp/` containing `.claude-plugin/plugin.json` and a `.mcp.json` whose `mcpServers.argus` entry has `type: "http"`, the current MCP URL, and Argus's client timeout in milliseconds. omp's global configuration SHALL NOT be written. A materialization failure SHALL be logged and the launch SHALL proceed without the flag.

#### Scenario: Plugin dir delivered per launch
- **WHEN** an omp command is built with the MCP port set
- **THEN** it contains `--plugin-dir` pointing at a directory whose `.mcp.json` names the current URL

#### Scenario: Same content reuses the same directory
- **WHEN** the plugin is materialized twice for the same URL
- **THEN** both calls return the same directory and a different URL yields a different directory
