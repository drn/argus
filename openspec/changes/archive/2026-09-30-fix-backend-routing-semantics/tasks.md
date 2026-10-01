## 1. Retire `[hera.worker_budget]`

- [x] 1.1 Delete `WorkerBudgetConfig`, `DefaultWorkerBudgetFallbackBackend`, `HeraConfig.WorkerBudget`, and the `applyFileDefaults` config.toml-default-fallback logic (`internal/config/config.go`, `internal/config/file.go`)
- [x] 1.2 Delete `usagebudget.ResolveWorkerBackend`; keep `Probe`/`CachedClaudePct`/`Reading`
- [x] 1.3 Add `usagebudget.CachedReading()` / `backendtier.CachedReading()` full-Reading accessors (for the BootInfo relay)
- [x] 1.4 Delete the `resolveHeraWorkerBackend` wrapper in `internal/mcp/hera.go`; pass `backend` straight through
- [x] 1.5 Delete `resolveWorkerBackend`/`SetConfigResolver`/`ConfigResolver`/`w.config` in `internal/heragater/heragater.go`; pass `""` straight through at the one call site
- [x] 1.6 Remove the `SetConfigResolver` wiring call in `internal/daemon/daemon.go`
- [x] 1.7 Update/delete tests referencing the retired config/resolver in `internal/config`, `internal/usagebudget`, `internal/mcp`, `internal/heragater` (incl. the subcoord fixture's `configCalled` assertion)

## 2. Unify backend-resolution precedence (project.Backend + tier list reachable at creation)

- [x] 2.1 Extract `resolveDefaultBackendName(explicit, project string, cfg config.Config) string` in `internal/agent/agent.go`
- [x] 2.2 `agent.ResolveBackend` calls the shared function
- [x] 2.3 `agent.CreateAndStart`'s task-creation stamp calls the shared function instead of blindly stamping `cfg.Defaults.Backend`
- [x] 2.4 Add `internal/agent/create_test.go` coverage: project-backend-applies, explicit-overrides-project, tier-list-applies, default-floor-unchanged

## 3. Hera coordinator/sub-coordinator Claude-capable carve-out

- [x] 3.1 Add `resolveCoordinatorBackend(explicit, project string, cfg config.Config) string` in `internal/agent/hera_spawn.go`, using `IsClaudeBackend`
- [x] 3.2 Wire into `SpawnHeraCoordinator` (covers the rail `n` key automatically, since enforcement is centralized)
- [x] 3.3 Wire into `MaterializeHeraSubCoordinator`
- [x] 3.4 Add unit tests for `resolveCoordinatorBackend` (explicit-wins, explicit-non-Claude-still-wins, forces-claude-when-default-is-not, tier-list-never-applies, project-pin-honored, no-claude-backend-configured) and end-to-end tests for both spawn functions

## 4. Costed Codex PTY-fallback opt-in

- [x] 4.1 Add `config.BackendRoutingConfig.CodexPTYFallbackEnabled` (config.toml-only, default false)
- [x] 4.2 Add `backendtier.SetCodexPTYFallbackEnabled`/gate `Probe`'s PTY-fallback branch on it
- [x] 4.3 Wire `Daemon.probeCodexOnce` to reflect `cfg.BackendRouting.CodexPTYFallbackEnabled` live every tick
- [x] 4.4 Update/add `internal/backendtier/codexprobe_test.go` coverage (disabled-by-default leaves cache stale + logs, explicit-enable still works, existing PTY-path tests opt in explicitly)
- [x] 4.5 Add `internal/config/file_test.go` coverage for the new config key coexisting with the tier list
- [x] 4.6 Add `internal/daemon/codexprobe_test.go` wiring smoke test

## 5. Surface usage-probe readings in BootInfo + TUI status bar

- [x] 5.1 Add `ClaudeUsagePct`/`ClaudeUsageKnown`/`CodexUsagePct`/`CodexUsageKnown` to `daemon.BootInfoResp` (additive, no protocol bump)
- [x] 5.2 Populate them in `RPCService.BootInfo` from `usagebudget.CachedClaudePct()`/`backendtier.CachedCodexPct()`
- [x] 5.3 Add `internal/daemon/bootinfo_relay_test.go` coverage (socket-free `rpcFor` construction, not the flaky full-Serve harness)
- [x] 5.4 Add `StatusBar.SetUsage`/`UsageSummary` + render the `cla X% · cdx Y%` segment, `—` for unknown/stale
- [x] 5.5 Add `App.reevaluateUsage`/`claimUsageCheck`/`usageRecheckInterval`, wired alongside the existing skew recheck on the daemon-health-check tick
- [x] 5.6 Add TUI tests: widget-level render tests (unknown-default, known-renders-percentages, stale-renders-unknown-not-percentage) and app-level wiring tests (interval floor, gate behavior, no-provider-is-inert, feeds-status-bar)

## 6. Supervisor spawn-surface bookkeeping

- [x] 6.1 Bump `SupervisorSpawnSurface` 13→14 in `internal/daemon/surface.go` with a history line
- [x] 6.2 Re-record `SpawnSurfaceDigest` in `internal/daemon/surface_test.go`'s companion constant

## 7. Documentation

- [x] 7.1 Update `context/knowledge/gotchas/usage-budget-routing.md`: retirement, the uxlog daemon-silence finding (its own entry), the Codex rollout-path dead-overlay finding, the opt-in fallback, the TUI status-bar relay mechanism
- [x] 7.2 Write proposal.md / design.md / delta specs (`usage-budget-routing` REMOVED, `backend-tier-routing` MODIFIED+ADDED, `agent-execution` MODIFIED, `hera-coordination` REMOVED+ADDED, `tui-shell` ADDED)

## 8. Verification and ship

- [x] 8.1 `make pre-pr` clean
- [x] 8.2 `openspec archive fix-backend-routing-semantics` (or manual merge-and-move) before opening the PR
- [ ] 8.3 Open PR via `mcp__argus__iris_gh_pr_create`
- [ ] 8.4 Watch CI to green (fix-and-repush loop if red); do not report done over a red run
