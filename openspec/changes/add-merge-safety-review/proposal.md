## Why

`add-merge-safety-classifier` builds the tiered evidence classifier; this change is where it becomes visible and actionable. Per Aaron's direction, a single review popup should do the whole job in one sitting — classify, show what's confirmed-safe vs. not, and let the operator act immediately — rather than a two-step "warn, then separately clean up later" flow. This also covers the 737-task historical backlog directly: the same popup, given the full backlog as its candidate set, replaces the one-time manual/LLM sweep with a permanent, repeatable, in-product action.

## What Changes

- A new **merge-safety review popup**: two sections, **NOT-SAFE** first, then **SAFE**, each task showing its classification reason. Three actions: **Clean safe** (default-selected), **Clean all**, **Cancel**. Both clean actions act immediately — no separate later step.
  - **Clean safe**: cleans up only the tasks in the SAFE section.
  - **Clean all**: explicit override — having seen the NOT-SAFE list, the operator deliberately cleans up everything shown.
  - **Cancel**: no-op.
- **Single-role nuke** (`Ctrl+D` on a role) now opens this popup with a candidate set of exactly the one task being nuked, classified via Tier A only (local, no network — nuke stays an interactive, non-network path). Choosing Clean (safe or all, equivalent at n=1) runs the existing nuke mechanics (`heraReclaimAndArchiveTask` — stop session, reclaim worktree+branch, mark role/orchestrator nuked). This replaces today's plain y/N confirm for this one entry point.
- **New global Cleanup action** (reachable via the Ctrl+K command palette, not scoped to any coordinator) opens the same popup with the full stuck-task backlog (`archived=1`, `status=in_review`, no live Hera binding`) across every project as its candidate set, classified via Tier A **and** Tier B (network allowed — this is a deliberate, occasional maintenance action, not an interactive hot path). Choosing Clean immediately deletes the chosen scope's task rows and worktrees/branches, reusing the exact guards `PruneCompleted` already has (live-Hera-binding exclusion) rather than a second deletion path.
- Cascade nuke (`Ctrl+D` on a coordinator/orchestrator header) and clear-archived (`C`) are **explicitly out of scope for the popup** — they keep today's aggregate count-based confirm, extended only with a confirmed/not-confirmed count (Tier A, still never blocking), exactly as originally scoped. Converting them to a full per-task list with partial (some-of-the-subtree) cleanup would be a materially different, riskier feature (which roles/orchestrator structure survives a partial cascade) that nothing in this direction actually asked for — named explicitly as a Non-Goal, not a silent gap.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: adds the review popup, its single-role-nuke entry point, and the new global Cleanup entry point.
- `rest-api`: adds endpoints to trigger/read the on-demand backlog classification and to immediately clean a chosen scope.
- `worktree-management`: generalizes "Pruning completed tasks" so its core delete-with-guards logic is callable against an explicit task-ID list, not only the implicit "all status=complete" set — reused by both the existing `Ctrl+R` flow (unchanged behavior) and this change's Clean actions.

## Impact

- Depends on `add-merge-safety-classifier` landing first.
- `internal/tui/heraactions.go`: single-role nuke branch of `heraOpenDelete` changes from a synchronous confirm build to an async classify-then-open-popup dispatch. Cascade/clear-archived paths gain only the count augmentation, unchanged mechanics.
- `internal/db/tasks.go` / `internal/agent/prune.go`: `PruneCompleted` is re-expressed as a thin wrapper over a new explicit-ID-list primitive; existing behavior and tests for the `Ctrl+R` flow are unaffected.
- New daemon-side on-demand classification + `task_meta` caching for the global backlog (as in the earlier draft of this work), new `/api/maintenance/...` endpoints, new TUI popup widget modeled on the task switcher's grouped rendering.
- TUI-only for this stage; web PWA/macOS parity is an explicit named Non-Goal (mirrors the existing standing "Hera mutations are TUI-only" gap).
