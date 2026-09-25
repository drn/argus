## Why

Argus currently writes its MCP server into Claude Code and Codex user-wide configuration. This exposes Argus tools to sessions started outside Argus and leaves a global Claude project-MCP trust setting behind.

## What Changes

- Pass the daemon's actual MCP listener port to the agent launcher, including the session supervisor.
- Add the Argus MCP server through Claude's `--mcp-config` and Codex's `-c` on every fresh and resumed Argus task session, including rerenders and coordinator recycles.
- Remove the Argus-owned `argus` and legacy `argus-kb` entries from Claude and Codex global configuration without disturbing other servers. Stop setting global Claude project-MCP trust.
- Keep opencode's existing injection behavior outside this change.

## Impact

- Task launch and daemon/supervisor RPC wiring change; no MCP tool or HTTP API changes.
- Existing Argus sessions keep their current configuration until resumed or restarted. Global cleanup runs at daemon startup.
- The MCP server remains conditional on `kb.enabled`; no launch flag is added when it is unavailable.
