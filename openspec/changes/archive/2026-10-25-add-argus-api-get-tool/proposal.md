## Why

Agents inspect daemon state (tasks, bridge state, shadow data) by running `T=$(cat ~/.argus/api-token); curl -H "Authorization: Bearer $T" http://127.0.0.1:$P/api/...`. `curl` is ask-only by policy, so every read costs the user a permission prompt, and the pattern puts the master token on a command line. A stopgap wrapper script exists in a third-party repo but needs a manual symlink and a settings edit. The argus MCP server is already loaded in every agent session, so a built-in tool works for any argus user with zero install.

## What Changes

- New MCP tool `argus_api_get(path)`: performs a GET against the daemon's own REST API **in-process** (the same handlers, via the same router) and returns the response body.
- GET only, by construction: the tool takes no method argument and dispatches only routes registered as `GET /api/...`. Streaming (SSE) routes are refused. Master-only handlers see a non-master caller and refuse.
- Strict path validation: relative API path (a leading `/api/` is optional), no `..`, no scheme/host, no control characters or fragments. Unknown routes return a clean 404 error.
- The API token never appears in tool output: the tool never reads or sends it (no network hop, no auth header), and any occurrence in a response body is redacted defensively.
- The tool description lists the useful routes so agents can discover them.
- Docs: README MCP tool table; the `argus-schedule` builtin skill's read-only curl calls are switched to the tool.

## Capabilities

### New Capabilities

_None._

### Modified Capabilities

- `mcp-server`: adds the `argus_api_get` tool and its gating (registered only when the daemon wires the REST API into the MCP server).

## Impact

- Code: `internal/api/readonly.go` (new in-process dispatcher), `internal/mcp/apiget.go` (tool), `internal/mcp/server.go` (dispatch + tools/list), `internal/daemon/daemon.go` (wiring).
- No REST/wire change, no ProtocolVersion or supervisor spawn-surface change.
- Frontend parity: agent-facing MCP surface only; TUI/web/macOS unaffected.
- Non-goals: any write path (POST/PUT/DELETE), SSE passthrough, exposing master-only handlers, a CLI/shell equivalent.
