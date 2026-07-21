## Why

`add-worker-context-indicator` reused `coordinator_context_budget` (default `200000`) as the
percentage denominator for the rail's worker/freelance context-pressure indicator. That number is
not a context window size — it's the point at which the coord-hook Stop hook starts nudging a
*coordinator* to recycle, deliberately set well below a coordinator's real ceiling so it has room
to wrap up gracefully. A worker runs with a much larger real context window (assume 1M tokens); by
that budget's math, a worker crossing 40% would mean it had already burned through 80,000 tokens —
a small fraction of its actual capacity, and a false alarm. The intended trigger is 40% of the
worker's real window: 400,000 tokens.

## What Changes

- New `HeraConfig.WorkerContextWindow` (`config.toml` key `hera.worker_context_window`), default
  `1000000`, separate from `coordinator_context_budget`.
- `resolveHeraTier` picks the denominator by role kind: `CoordinatorContextBudget` for a
  coordinator (unchanged), `WorkerContextWindow` for a worker or freelance role.
- No change to the indicator's tier thresholds (40/65/90) or rendering — only which number those
  percentages are computed against.

## Capabilities

### Modified Capabilities

- `hera-view`: the context-pressure indicator requirement's denominator description corrected from
  "the project's configured `coordinator_context_budget`" to "`coordinator_context_budget` for a
  coordinator role, `worker_context_window` for a worker or freelance role."
- `config-management`: `HeraConfig` gains `worker_context_window` (default `1000000`).

## Impact

- **Modified code:** `internal/config/config.go` (`HeraConfig.WorkerContextWindow`),
  `internal/tui/hera_tiering.go` (`resolveHeraTier`'s denominator selection).
- **No schema change, no breaking change.** A project with no `worker_context_window` override
  gets the 1000000 default; the indicator's visual behavior is unchanged in shape, only recalibrated
  in scale for worker/freelance rows.
