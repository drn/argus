## REMOVED Requirements

### Requirement: Budget-aware backend resolution for worker/freelance spawns only

**Reason**: `usagebudget.ResolveWorkerBackend` and the `[hera.worker_budget]` one-way "force the fallback backend" switch it implemented are retired (`fix-backend-routing-semantics`). The switch's `enabled` flag was a trap name — it meant "force the fallback unconditionally, ignoring `threshold_pct`," not "turn on threshold routing" — and had been silently forcing every hera worker spawn onto the configured fallback regardless of actual usage headroom. `backend-tier-routing`'s `[backend_routing]` tier list is now the sole budget-aware resolution mechanism, consulted by every task-creation path including hera worker/freelance spawn — there is no replacement one-way switch, and hera worker spawn no longer has a resolution step of its own at all (it passes its caller's explicit `backend`, or empty, straight through to `agent.CreateAndStart`, which resolves exactly like any other task).

**Migration**: Configure `[backend_routing]` tiers (see `backend-tier-routing`'s spec) instead of `[hera.worker_budget]`. Remove the `[hera.worker_budget]` table from `config.toml` by hand — it is silently ignored (unknown keys do not error), but has no effect.

### Requirement: The switch is a global, config.toml-only setting

**Reason**: This requirement described `[hera.worker_budget]`'s own config surface, which no longer exists — superseded by `backend-tier-routing`'s requirement of the same shape for `[backend_routing]`.

**Migration**: See `backend-tier-routing`'s "Tier list configuration source precedence" requirement.
