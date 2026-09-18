## Why

Aaron's weekly Claude usage cap is a shared, account-wide resource — every hera worker spawned across every argus project draws on the same pool, and there is currently no way to protect it. He just hit 94% of his weekly cap (confirmed via a headless `/usage` probe) with three days left before reset, entirely because of hera worker/freelance fan-out. Argus already ships first-class Codex backend support with CLAUDE.md + skill parity (add-nonclaude-context-parity), so there is now a safe fallback backend to route to under budget pressure instead of either hitting a hard cap or manually remembering to pass `backend="codex"` on every spawn.

## What Changes

- Add a **usage-budget routing** capability: a background daemon-side probe periodically samples Aaron's current weekly Claude usage percentage (reusing the proven `/usage`-in-a-headless-session technique) and caches the result (percentage + reset timestamp) for fast synchronous reads. The probe is best-effort and fails open — a stale or unavailable reading never blocks a spawn.
- Add a **global config switch** (`~/.argus/config.toml`, under `[hera]`) with two independent controls that share one effect — defaulting non-coordinator hera spawns to a fallback backend (default `codex`):
  - a manual always-on override, and
  - an automatic threshold (e.g. 90%) that activates the same fallback only while the cached weekly-usage percentage is at or above it, and automatically deactivates at the next weekly reset (or as soon as a fresh reading drops back below threshold).
- Add a new backend-resolution tier consulted ONLY for hera **worker/freelance** role spawns (never coordinators, root or nested — confirmed scope) and ONLY when the caller did not pass an explicit `backend`: `hera_spawn_worker`'s MCP handler and the plan-DAG gater's leaf-worker materialization path (`heragater.materializeNode`, excluding the `subcoord` branch) both consult it before falling through to the existing `ResolveBackend` project/default precedence.
- Explicit `backend=` on `hera_spawn_worker` or a plan node always wins — this tier only fills in what would otherwise have been empty.

## Capabilities

### New Capabilities
- `usage-budget-routing`: background weekly-usage probe + cache, the config switch (manual + threshold modes), and the resolution function that turns "budget pressure" into a fallback backend choice.

### Modified Capabilities
- `hera-coordination`: `hera_spawn_worker` and the plan-DAG gater's worker-materialization path gain a new backend-resolution tier (budget-aware fallback) that runs before their existing "empty backend falls through to project/default" behavior, scoped to worker/freelance roles only.

## Impact

- New package (e.g. `internal/usagebudget`): probe + cache + resolution function.
- `internal/config`: new `[hera.worker_budget]` config fields (global, not per-project — usage is an account-wide resource).
- `internal/mcp/hera.go` (`toolHeraSpawnWorker`): consult the resolver when `p.Backend == ""`.
- `internal/heragater/heragater.go` (`materializeNode`): consult the resolver in place of the hardcoded `""` backend argument, worker-kind path only.
- `internal/daemon/daemon.go`: start the probe's background goroutine alongside the existing PR poller (`runPRPoller` precedent).
- No changes to coordinator or subcoord spawn paths, no changes to explicit-backend spawns.
