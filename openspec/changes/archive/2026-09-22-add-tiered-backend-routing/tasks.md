## 1. Config schema

- [ ] 1.1 Add `BackendRoutingConfig`/`Tier` types to `internal/config` (`backend`, `probe`, `threshold_pct`), parsed from `[backend_routing]` / `[[backend_routing.tier]]` in config.toml.
- [ ] 1.2 Validate `probe` against the registered kind set at load time (unknown kind is not a load error — surfaces as a skipped tier at resolution time, per spec).
- [ ] 1.3 Unit tests: table absent → nil/empty tier config, zero behavior change; tier naming a backend not in `cfg.Backends` loads without error (validity is a resolution-time concern, not a config-load concern).

## 2. Probe registry + Codex probe

- [ ] 2.1 Add a kind-string dispatch (`switch tier.Probe { case "claude_usage": ...; case "codex_usage": ...; case "none": ... }`) in the resolver — no generic `Prober` interface or plugin registration; each kind's implementation is plain, hardcoded, provider-specific code.
- [ ] 2.2 Have the `claude_usage` case call into `internal/usagebudget`'s existing Claude PTY-probe-and-cache read path as-is, without changing `usagebudget.ResolveWorkerBackend`'s existing behavior or the hera call sites.
- [ ] 2.3 Implement the Codex rollout-file reader: locate the most recent `~/.codex/sessions/YYYY/MM/DD/rollout-*.jsonl`, tail-scan for the last record with a `rate_limits` object, extract `used_percent` + reset info. Treat a missing/older-than-staleness-window file as "no fresh reading."
- [ ] 2.4 Implement the Codex PTY-probe fallback (headless `codex` session, minimal message, parse rendered `/status` output) — defensive parsing matching the Claude probe's fail-open contract; only invoked when 2.3 has no fresh reading.
- [ ] 2.5 In-memory cache for the Codex probe (percentage, reset info, last-probed-at), same shape/contract as the existing Claude cache.
- [ ] 2.6 Unit tests: fresh rollout file parses correctly and skips the PTY fallback; stale/missing/malformed rollout file triggers the PTY fallback; PTY fallback failure leaves cache unchanged and logs; `none` kind always reports available.

## 3. Tier-list resolver

- [ ] 3.1 Implement the ordered-list walk: first tier that's uncapped, or capped-and-under-threshold, or capped-with-unknown/stale reading, wins; empty/no-list returns empty.
- [ ] 3.2 Implement config.toml-vs-DB precedence: config.toml tier list (if non-empty) is authoritative; otherwise read the DB-persisted list.
- [ ] 3.3 Skip tiers naming a backend absent from `cfg.Backends`; all-tiers-invalid degrades to "no list configured."
- [ ] 3.4 Unit tests covering every scenario in `specs/backend-tier-routing/spec.md`.

## 4. DB storage for the Settings-UI-edited tier list

- [ ] 4.1 New table (mirroring `internal/db/backends.go`'s pattern) storing ordered tier rows.
- [ ] 4.2 Accessors: `BackendTiers() ([]Tier, error)`, `SetBackendTiers([]Tier) error` (or equivalent), used only when config.toml defines no tier list.
- [ ] 4.3 Unit tests: round-trip persistence, order preservation, empty-list state.

## 5. Wire into general backend resolution

- [ ] 5.1 `internal/agent/agent.go`'s `ResolveBackend`: insert the tiered resolver between "explicit task/project backend" and "single global default," per the modified `agent-execution` spec.
- [ ] 5.2 Unit tests: explicit task/project backend bypasses the tiered resolver entirely (assert probes are never read); no tier list configured falls through to existing default-backend behavior unchanged; tier list configured and first tier available resolves to it; all tiers exhausted falls through to the single global default.

## 6. Daemon wiring

- [ ] 6.1 Extend (or add a sibling to) the existing probe goroutine in `internal/daemon/daemon.go` to also tick the Codex probe on the same cadence.
- [ ] 6.2 Smoke test: daemon starts cleanly with no `[backend_routing]` table configured (fully inactive is a valid, common state).

## 7. TUI Settings UI

- [ ] 7.1 Add the new category (placement — new `catBackendTiers` vs. extending `catBackends` — decided at implementation time) per the `settings-view` conventions (rail label, `Label()`, `rebuildRows`, detail rendering).
- [ ] 7.2 Read-only rendering when config.toml defines the active list (mirrors `catBackends`' "(command is hardcoded)" pattern).
- [ ] 7.3 Add/remove tier actions; probe-kind cycling; threshold inline-edit (inert for `none`).
- [ ] 7.4 New reorder-up/reorder-down actions (no existing widget to reuse — new, contained code per design.md's decision).
- [ ] 7.5 SimulationScreen smoke tests per this repo's TUI testing convention: category navigation, add/remove/reorder, config.toml-read-only rendering, probe-kind cycling.

## 8. Docs

- [ ] 8.1 New `context/knowledge/gotchas/` entry (or extend `usage-budget-routing.md`) covering: the probe-registry/kind-keyed design, the Codex rollout-file-read-preferred-over-PTY-probe contract, the config.toml-wins-wholesale-over-DB precedence, and that hera's own resolver is deliberately untouched.
- [ ] 8.2 README Reference appendix: document the `[backend_routing]` config keys and the new Settings category, per this repo's "update in place for any factual change" rule.
- [ ] 8.3 Archive this change within the same PR: fold the delta specs into `openspec/specs/backend-tier-routing/` (new), `openspec/specs/agent-execution/`, `openspec/specs/config-management/`, `openspec/specs/settings-view/` (modified), move the change folder to `openspec/changes/archive/<date>-add-tiered-backend-routing/`.

## 9. Verification

- [ ] 9.1 `make pre-pr` clean (build/vet/fmt-check/lint-pr/vuln/test-cover-gate).
- [ ] 9.2 Manual/live check: configure a two-tier list (`claude_usage` then `none`/`pi`) in `config.toml`, create a task with no explicit backend, confirm it resolves per the configured tiers; then move the list to the Settings UI (remove the config.toml table) and confirm it's editable there and still resolves correctly.
- [ ] 9.3 Confirm zero behavior change for hera worker/freelance spawn and coordinator/sub-coordinator spawn paths (no test or manual check in this change should touch `usagebudget.ResolveWorkerBackend`'s existing test suite's outcomes).
