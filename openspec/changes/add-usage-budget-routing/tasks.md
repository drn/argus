## 1. Config schema

- [ ] 1.1 Add `WorkerBudgetConfig` (`enabled bool`, `threshold_pct int`, `fallback_backend string`) to `internal/config`, nested under the existing `HeraConfig` as `[hera.worker_budget]`.
- [ ] 1.2 Default `fallback_backend` to `"codex"` when the table is present but the field is empty; `threshold_pct = 0` means "threshold mode disabled."
- [ ] 1.3 Unit tests: table absent → feature inactive; `fallback_backend` naming a backend not in `cfg.Backends` → feature inactive (fail open, no error).

## 2. Usage probe + cache (`internal/usagebudget`, new package)

- [ ] 2.1 Implement a lightweight one-shot subprocess probe that runs a headless Claude Code session with `/usage` and captures its rendered output (reuse the technique proven manually today: PTY-backed subprocess, no full `agent.CreateAndStart` task/worktree).
- [ ] 2.2 Parse the "Current week (all models)" percentage and its reset timestamp from the captured output. Parse defensively — any unexpected shape is a parse failure, not a panic.
- [ ] 2.3 In-memory cache holding `{percentage, resetAt, lastProbedAt}`, guarded for concurrent access (probe goroutine writes, resolver reads).
- [ ] 2.4 `Probe(ctx) error` performs one probe-and-cache-update cycle; probe or parse failure leaves the previous cached value in place and logs via uxlog — never returns a value the caller must handle as fatal.
- [ ] 2.5 Unit tests: successful parse updates the cache; subprocess failure leaves cache unchanged + logs; unparseable output leaves cache unchanged + logs; cache read before any probe returns "unknown."

## 3. Daemon wiring

- [ ] 3.1 Start a background goroutine at daemon startup (alongside `runPRPoller`, `internal/daemon/daemon.go`) that calls `usagebudget.Probe` on a configurable interval (default ~30 min).
- [ ] 3.2 Wire the interval from `[hera.worker_budget]` config (or a sane hardcoded default if not separately configurable — confirm during implementation whether cadence itself needs to be user-configurable or can stay a constant for v1).
- [ ] 3.3 Smoke test: goroutine starts without panicking when the config table is absent (feature fully inactive is a valid, common state).

## 4. Budget-aware backend resolution

- [ ] 4.1 Implement `usagebudget.ResolveWorkerBackend(explicit string, cfg config.Config) string`: returns `explicit` unchanged if non-empty; otherwise returns the configured fallback backend when the manual switch is enabled OR the cached percentage is at/above `threshold_pct` (and not stale/unknown); otherwise returns `""`.
- [ ] 4.2 Wire into `internal/mcp/hera.go: toolHeraSpawnWorker` — replace `Backend: p.Backend` with the resolver's result.
- [ ] 4.3 Wire into `internal/heragater/heragater.go: materializeNode`'s worker-kind path (the branch that currently hardcodes `""` for backend in the `w.materialize(...)` call) — leave `materializeSubCoord` untouched.
- [ ] 4.4 Unit tests: explicit backend bypasses the resolver entirely (assert the cache is never read); manual switch on with no explicit backend returns fallback; threshold crossed returns fallback; threshold not crossed (or cache unknown) returns empty; coordinator/subcoord spawn paths never call the resolver (assert via the existing materializeSubCoord test seam).

## 5. Docs

- [ ] 5.1 Add a `context/knowledge/gotchas/` entry (misc.md or a new file) covering: the `/usage`-is-reachable-headlessly discovery, the fail-open probe contract, and the config-only (no UI) v1 scope.
- [ ] 5.2 Archive this change within the same PR per repo convention: merge the delta specs into `openspec/specs/usage-budget-routing/` (new) and `openspec/specs/hera-coordination/` (modified), move the change folder to `openspec/changes/archive/<date>-add-usage-budget-routing/`.

## 6. Verification

- [ ] 6.1 `make pre-pr` clean (build/vet/fmt-check/lint-pr/vuln/test-cover-gate).
- [ ] 6.2 `openspec validate --all --strict` clean.
- [ ] 6.3 Manual/live check: with the manual switch enabled in a local `~/.argus/config.toml`, spawn a worker via `hera_spawn_worker` with no explicit `backend` and confirm it lands on the configured fallback backend.
