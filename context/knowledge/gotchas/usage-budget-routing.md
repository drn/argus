# Usage budget routing gotchas

Budget-aware Hera worker routing is advisory steering, not enforcement.

- **Claude `/usage` is reachable from a headless PTY-backed Claude Code subprocess.** The daemon probe relies on the rendered "Current week (all models)" line and reset timestamp from that client-side screen, without creating an Argus task or spending model tokens.

- **The probe and parser fail open by design.** Probe failures, parse drift, missing cache, stale cache, invalid fallback backend, or unavailable config all leave the worker backend empty so existing project/default backend precedence applies.

- **The v1 switch is config.toml-only.** `[hera.worker_budget]` intentionally has no TUI, web, or macOS settings surface yet; this is the named Frontend Parity non-goal for `add-usage-budget-routing`, not an overlooked UI gap.

## Tiered backend routing (`internal/backendtier`, `add-tiered-backend-routing`)

Generalizes usage-aware routing to **general (non-hera) task-backend resolution** (`agent.ResolveBackend`) via an ordered, user-configurable `[backend_routing]` tier list — a separate feature from `[hera.worker_budget]` above, not a replacement for it.

- **`usagebudget.ResolveWorkerBackend` and its two hera call sites (`internal/mcp/hera.go`, `internal/heragater/heragater.go`) are deliberately untouched.** `backendtier.ResolveBackend` is a new, parallel resolver consulted only by `agent.ResolveBackend`'s general precedence chain (`task.Backend > project.Backend > backendtier.ResolveBackend(cfg) > cfg.Defaults.Backend`); hera worker/freelance spawn keeps its own bespoke Claude-vs-single-fallback resolver unchanged. Reusing `usagebudget.CachedClaudePct()` as a read-only cache accessor is fine — wrapping or changing `ResolveWorkerBackend`'s own behavior is not in scope for this feature.

- **Probe kinds are hardcoded per-provider and dispatched by a kind string (`claude_usage`/`codex_usage`/`none`), not a plugin interface.** `backendtier.ResolveBackend`'s tier-list walk `switch`es on `tier.Probe`; adding a new kind (e.g. a hypothetical Gemini probe) means a new `case` plus one new hardcoded probe function, never a generic `Prober` registration mechanism. An unrecognized probe kind is skipped (never available), not an error.

- **The Codex probe prefers a free rollout-file read over a costed PTY probe, in that order, every tick.** `backendtier.Probe` first tail-scans the most recently modified `~/.codex/sessions/**/rollout-*.jsonl` for the last record carrying a `rate_limits` object (`worstCodexWindow` takes the more-exhausted of Codex's independent 5h/weekly windows, not just the weekly one) and only falls back to spawning a headless `codex` PTY session + `/status` scrape when no rollout file newer than `CacheMaxAge` (1 hour, mirroring `usagebudget`'s own staleness window) has a usable reading. The PTY fallback genuinely spends Codex quota to check Codex quota — it's a real, accepted trade-off (see design.md), not a bug, and its `/status` text parser is defensive-by-necessity since the upstream format was unstable at design time (openai/codex#15281).

- **config.toml wins wholesale over the DB — no per-tier merge.** `db.Config()` loads `cfg.BackendRouting.Tiers` from the `backend_tiers` DB table *before* `cfgLoader.Apply` runs, so a non-empty `[[backend_routing.tier]]` table in config.toml fully replaces the slice during TOML decode (same mechanism `Backends`/`Projects` already rely on for "config.toml wins"). There is no world where config.toml supplies tiers 1–2 and the DB supplies tier 3; whichever source is present governs the entire list. `db.BackendTiersFromConfigToml()` (via `FileLoader.DefinesBackendRoutingTiers()`) is the TUI's read-only signal — it reflects only the *last* `Config()` call's finding, so it's meaningless before one has run, and is always `false` for an in-memory/remote store.
