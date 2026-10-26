## Context

Claude's HTTP/SSE per-request timer defaults to 60 seconds. Its per-server `timeout` in milliseconds raises that deadline; progress notifications do not extend it. Codex also defaults tool calls to 60 seconds. Argus permits `hera_inbox` to wait 120 seconds.

## Goals / Non-goals

**Goals:** configure a 150-second deadline, derived from the shared inbox cap plus 30 seconds, for existing Claude, Codex, and OpenCode injection paths.

**Non-goals:** `mcp-timeout-antigravity-cursor-parity` is a named follow-up to verify supported timeout settings for Antigravity and Cursor before changing their global JSON. Pi and custom families have no managed MCP delivery. Changing server wait behavior or REST/frontend behavior is out of scope.

## Decisions

- Put the cap and derived client deadline in a small dependency-free `internal/mcplimits` package. Importing `internal/mcp` from agent would cycle; duplicating literals could drift.
- Claude receives numeric `timeout` in its `--mcp-config` JSON. The installed binary contains the HTTP timeout schema and request timer logic, but inline flag parsing has not been conclusively traced; also export `MCP_TOOL_TIMEOUT` at spawn as the approved fallback. Codex receives `tool_timeout_sec`; OpenCode receives `timeout` in milliseconds, including upgrades of existing entries while preserving `enabled: false`.
- Keep existing spawn/config logging and add timeout delivery logging under `[mcp]` for Claude/Codex. Reuse OpenCode's existing injection success/failure logs.
- Bump and re-record the supervisor spawn surface after the concurrent spawn change merges, so supervisors holding old launch logic are detected.

## Risks / Trade-offs

- Claude's fallback applies to all MCP servers in that child process → per-server Argus configuration is primary; the fallback ensures older inline parsing paths still get the required deadline.
- Existing sessions retain their launch configuration → after deployment a newly spawned session must run `hera_inbox(timeout_seconds=90)` with no mail and return a clean empty timeout.
- Shared-host gate contention → run targeted tests first; wait for the coordinator's merge notification and full gate slot before pushing.
