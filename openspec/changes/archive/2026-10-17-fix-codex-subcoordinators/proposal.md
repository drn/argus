# fix-codex-subcoordinators

## Why

Hera coordinators accidentally create Codex (non-Claude) sub-coordinators, which lack coord-hook
context-size stamping, token/cost accrual, and recycle. Root cause: the MCP handlers for
`hera_plan_node` and `hera_plan` accept `kind` (`subcoord`) and `goal`, but the ADVERTISED input
schemas declare neither. Agents cannot discover the declarative sub-coordinator route (which forces a
Claude-capable backend via `resolveCoordinatorBackend`), so they fall back to "worker-promotion":
`hera_spawn_worker` (routed by `[backend_routing]`, often Codex) whose prompt tells the worker to call
`hera_new_orchestrator` on itself. Incident: prod-speed / prod-speed-claude.

## What Changes

- Advertise `kind` (`worker`|`subcoord`, default `worker`) and `goal` on `hera_plan_node` and on each
  node of `hera_plan`; `prompt` is no longer schema-required (subcoord nodes use `goal`). Descriptions
  state that `kind=subcoord` is THE way to stand up a sub-coordinator.
- A schema/handler drift test asserts every parameter the handlers decode is advertised.
- `hera_new_orchestrator` REJECTS a caller whose task backend is not Claude-capable, redirecting to
  `hera_plan_node(kind="subcoord", goal=...)` or `hera_spawn_worker(backend="claude")`. No MCP-path
  override; humans keep the TUI new-coordinator form.
- Coordinator orientation + `hera` / `hera-plan` skills: worker-promotion requires `backend="claude"`;
  `kind=subcoord` is preferred.

## Non-Goals

- Frontend parity: MCP-only change; TUI, web, and macOS surfaces are unaffected (the TUI new-coordinator
  form is deliberately unguarded).
- No change to `hera_spawn_worker` routing or the gater's subcoord materialization.
