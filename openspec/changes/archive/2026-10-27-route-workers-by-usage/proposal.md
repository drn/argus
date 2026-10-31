## Why

Coordinators routinely pass `backend="claude"` to `hera_spawn_worker` even when nothing about the worker needs Claude, which bypasses `[backend_routing]` and leaves Codex quota unused. Live evidence (2026-10-26): every Claude-backed worker in the prior two days was an explicit coordinator override; every worker spawned without a backend landed on Codex as routing intended. The hera guidance primes the override (repeated `backend="claude"` examples, a stale reference to the retired worker fallback), and nothing reports which backend a worker actually got, so the drift was invisible.

## What Changes

- `hera_spawn_worker`'s result reports the worker's resolved backend and whether it was explicit or routed; the daemon's `spawn_worker ok` log line carries the same two fields.
- Tool descriptions and the `hera` / `hera-plan` builtin skills tell coordinators to omit `backend` by default and set it only for a concrete backend-specific need, and to prefer `archetype` over a backend-specific `model`.
- Remove the stale "usage-budget routing may supply its configured worker fallback" wording.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `hera-coordination`: spawn result and log report the resolved backend and its source.

## Impact

`internal/mcp/hera.go` (schema text, result text, log fields), `internal/skills/builtin/hera*/SKILL.md`. Additive MCP result text only; no REST, wire-protocol, or client changes, so TUI/web/macOS parity is unaffected.
