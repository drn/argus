## Why

`add-merge-safety-classifier` and `add-nuke-merge-warning` stop the problem from recreating itself going forward, but they don't address the 737-task historical backlog a live audit already found (tasks stuck at `status=in_review, archived=1` with their Hera binding already ended, permanently invisible to `PruneCompleted`). A prior investigation manually classified that backlog with the same tiered evidence this workstream's classifier now encodes in tested code — this change gives the operator a permanent, repeatable, in-product way to run that same classification and act on it, instead of a one-time manual/LLM-driven pass.

## What Changes

- A new Cleanup popup, opened from the Hera view, lists every task matching the stuck-task predicate (`archived=1`, `status=in_review`, no live Hera binding) across ALL projects, classified by the merge-safety classifier into two sections: **Safe** (confirmed merged) and **Needs review** (not confirmed), mirroring the grouped/sectioned scrollable-list pattern already used by the task switcher.
- Two bulk actions: mark only the Safe section's tasks complete, or mark every listed task (Safe and Needs Review) complete. **Needs Review tasks are never touched automatically** — marking them complete is only ever an explicit, informed bulk choice the operator makes after seeing why each one was held back.
- Classification runs daemon-side (on demand, not a new standing poll loop) via a new REST endpoint, reusing the classifier's per-repo-batched Tier B lookups; results are cached so re-opening the popup doesn't always re-pay the classification cost.
- **Open decision needing sign-off (see design.md):** does "mark complete" in this popup's bulk actions mean ONLY flipping `status → complete` (making the tasks reachable by the existing, already-audited `Ctrl+R` prune-completed flow as a deliberate separate step), or does it extend all the way to actually deleting the task + any remaining worktree in one action? This proposal recommends the former; see design.md for the full tradeoff.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: adds the Cleanup popup as a new, globally-scoped (not per-coordinator) view reachable from the Hera page, built on the classifier from `add-merge-safety-classifier`.
- `rest-api`: adds endpoints to trigger/read the on-demand cleanup-candidate classification and to apply the bulk status-flip action.

## Impact

- Depends on `add-merge-safety-classifier` (required) and benefits from, but does not require, `add-nuke-merge-warning` landing first.
- New daemon-side computation (reuses `internal/mergesafety`'s batch entry point), a new `task_meta` namespace (or equivalent) caching each candidate's last-computed verdict so repeat opens are cheap and the shared GitHub GraphQL budget isn't re-spent on every popup open.
- New TUI popup (`internal/tui`), modeled on the existing grouped-list precedent (`TaskSwitcherModal`'s sectioned/header-row rendering).
- New REST endpoints under `/api/maintenance/...`. TUI-only for this stage — per this repo's Frontend Parity rule, the web PWA and macOS app gap is an explicit, named Non-Goal here (mirroring the existing standing "Hera mutations are TUI-only" gap), not silence.
- No change to `PruneCompleted` itself, and (pending the open decision above) likely no new deletion code path at all — reusing the existing, already-hardened prune flow rather than parallel-implementing bulk deletion safety.
