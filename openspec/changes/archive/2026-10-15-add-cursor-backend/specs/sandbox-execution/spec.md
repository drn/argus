## ADDED Requirements

### Requirement: Cursor sandbox grant

Sandboxed cursor launches SHALL receive a write grant for `~/.cursor/chats`, `~/.cursor/projects` and `~/.cursor/statsig-cache.json` (never `mcp.json` or `cli-config.json`), emitted only into cursor launches' profiles.

#### Scenario: Grant present for cursor only
- **WHEN** a sandbox profile is generated for a cursor launch
- **THEN** it allows writes under `$HOME/.cursor`, and profiles for other backends do not
