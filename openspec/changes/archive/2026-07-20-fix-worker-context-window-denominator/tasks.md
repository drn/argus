## 1. Config field

- [x] 1.1 Write failing tests for `HeraConfig.WorkerContextWindow`'s default (`1000000`) and
      config.toml override, mirroring `CoordinatorContextBudget`'s existing tests exactly
- [x] 1.2 Add `WorkerContextWindow int` (`toml:"worker_context_window"`) to `HeraConfig`, wired into
      `DefaultConfig()` at `1000000`
- [x] 1.3 Make the 1.1 tests green

## 2. Denominator selection

**Depends on:** Stage 1

- [x] 2.1 Write failing tests: a worker/freelance role's `ContextPercent` computed against
      `WorkerContextWindow`, a coordinator's still against `CoordinatorContextBudget`, and a
      same-`ContextSize`-different-kind test making the split concrete
- [x] 2.2 Update `resolveHeraTier` to pick the denominator by `rv.Kind`
- [x] 2.3 Make the 2.1 tests green (update the two now-stale `add-worker-context-indicator`
      tests that assumed a single shared denominator)

## 3. Docs and archive

**Depends on:** Stage 2

- [x] 3.1 Correct the `hera-view` base spec's context-pressure indicator requirement text
- [x] 3.2 Add the `config-management` delta documenting `worker_context_window`
- [x] 3.3 Update the `README.md` Projects Tab description + `[hera]` config table
- [x] 3.4 Update the `context/knowledge/gotchas/hera-view.md` bullet
- [x] 3.5 Run `make pre-pr` clean
- [x] 3.6 `openspec archive fix-worker-context-window-denominator`, on the same branch, before merge
