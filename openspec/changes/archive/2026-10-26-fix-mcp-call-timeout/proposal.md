## Why

Spawned agents abort long `hera_inbox` waits at the MCP client's default request deadline, before Argus's supported 120-second wait can finish. Aaron approved raising the client timeout above that cap.

## What changes

- Share the inbox cap and a client deadline with 30 seconds of margin.
- Set Claude's Argus server timeout and its spawn environment fallback, Codex's server tool timeout, and OpenCode's remote server timeout.
- Test launch/config delivery and document the client deadline invariant.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `mcp-injection`: client deadlines exceed the longest supported inbox wait.

## Impact

Agent launch arguments/environment, OpenCode's global MCP entry, shared MCP limits, and supervisor spawn surface. No REST contract or frontend changes: TUI, web, and macOS all use the same spawn path.
