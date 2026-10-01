## Why

Three interacting defects left the user's backend-budget routing doing the opposite of what he configured. `[hera.worker_budget].enabled` is a trap name that forces every hera worker onto the fallback backend unconditionally, ignoring `threshold_pct` entirely — it forced 26 live tasks onto codex while Claude usage sat at ~40%, nowhere near his configured 90% threshold. Fixing that exposed a second, deeper bug: `agent.CreateAndStart` stamps `cfg.Defaults.Backend` onto every task with no explicit backend BEFORE `agent.ResolveBackend`'s project/tier precedence ever runs, which makes a project-level `Backend` override and the `[backend_routing]` tier list permanently unreachable for every freshly created task — hera or otherwise. And neither defect had a carve-out keeping hera coordinators off budget-tiered routing at all, even though coord-hook (context-size stamping, token/cost accrual, the recycle machinery) only works on Claude Code.

## What Changes

- **BREAKING**: delete `[hera.worker_budget]` entirely (`WorkerBudgetConfig`, `DefaultWorkerBudgetFallbackBackend`, `usagebudget.ResolveWorkerBackend`, and its two call-site wrappers in `internal/mcp/hera.go`/`internal/heragater/heragater.go`). The user must remove this block from his own `~/.argus/config.toml` by hand — no migration shim. `usagebudget.CachedClaudePct`/probe logic are retained; `backendtier`'s tier list is the sole replacement.
- Collapse `agent.ResolveBackend`'s precedence chain and `agent.CreateAndStart`'s task-creation-time backend stamp into one shared function (`resolveDefaultBackendName`), so a project-level `Backend` override and a configured `[backend_routing]` tier list are both actually reachable the moment a task (including a hera worker/freelance spawn) is created — not just at a session-start/resume call site that in practice never ran first.
- Add a hard, structural carve-out (`agent.resolveCoordinatorBackend`) so a hera coordinator or sub-coordinator spawn (`SpawnHeraCoordinator`, `MaterializeHeraSubCoordinator`) always resolves to a Claude-capable backend, regardless of tier/budget state. An explicit caller-supplied backend still wins (operator override), but a non-Claude-capable explicit choice is never accepted silently — it's honored with a `uxlog` warning naming what breaks.
- Surface both cached usage-probe readings (Claude weekly usage, Codex usage) permanently in the TUI status bar (`cla 42% · cdx 76%`), relayed from the daemon's in-memory caches via two new additive `BootInfoResp` fields — the only way to make a silent probe failure visible instead of recreating the exact silent-budget-surprise this investigation started from. A stale or never-probed reading renders as `—`, never a percentage.
- Bump `SupervisorSpawnSurface` (13→14): the backend a freshly created task resolves to can now differ from before this change.
- Make the Codex probe's costed PTY `/status` fallback opt-in and off by default (`config.BackendRoutingConfig.CodexPTYFallbackEnabled`, read live every probe tick). On the investigated machine, the free rollout-file read was silently dead (an isolated `CODEX_HOME` overlay directory never existed), so the probe fell through to the costed fallback on every ~30-minute tick — roughly 330 codex sessions spent since Sep 23 purely to check remaining Codex quota. With the fallback off by default, a dead rollout path now just leaves the cache stale/unknown, the same as any other fail-open probe miss.

## Capabilities

### New Capabilities

(none — this re-scopes and corrects existing capabilities)

### Modified Capabilities

- `usage-budget-routing`: `[hera.worker_budget]` and `usagebudget.ResolveWorkerBackend` are removed; the capability's requirements are replaced by `backend-tier-routing`'s now-unified resolution.
- `backend-tier-routing`: the tier list's scope widens from "general (non-hera) task-backend resolution" to all task-backend resolution except hera coordinator/sub-coordinator spawn, which is a new, explicit exception. Also adds the Codex PTY-fallback opt-in.
- `hera-coordination`: adds the coordinator/sub-coordinator Claude-capable-backend requirement.
- `tui-shell`: adds the always-visible usage-percentage status-bar readout.

## Impact

- **Code**: `internal/config/config.go`, `internal/config/file.go`, `internal/usagebudget/usagebudget.go`, `internal/backendtier/codexprobe.go`, `internal/agent/agent.go`, `internal/agent/create.go`, `internal/agent/hera_spawn.go`, `internal/heragater/heragater.go`, `internal/mcp/hera.go`, `internal/daemon/{daemon,rpc,types,surface}.go`, `internal/tui/{app,widget/statusbar}.go`.
- **Config**: `[hera.worker_budget]` removed from the schema; `~/.argus/config.toml` users of that block must hand-edit it away. `[backend_routing]`'s existing key name and shape are unchanged.
- **RPC**: `BootInfoResp` gains four additive fields (no protocol version bump; plain JSON-RPC).
- **Non-goals, named explicitly** (see design.md): `hera_new_orchestrator`'s self-promotion path is not covered (can't swap a live session's backend); web SPA and macOS app do not get the usage readout (TUI-only, per explicit user choice); root-causing WHY the Codex rollout-file path's `CODEX_HOME` overlay was never created on the investigated machine is a separate, not-yet-filed follow-up — its own failure path is itself silently swallowed by the daemon-side uxlog gap (below), so distinguishing "always failing" from "never triggered" needs that fixed first; fixing the daemon process's own `uxlog.Log` being a structural no-op (it never calls `uxlog.Init`) is a materially larger, orthogonal change, also left as a named follow-up — this PR works around it by surfacing probe state through `BootInfo`/the status bar instead of logs.
