## Context

This design was revised after Aaron rejected an earlier two-step draft (classify → flip status to `complete` → separately run `Ctrl+R` later). His direction: one popup does the whole job in one sitting — NOT-SAFE and SAFE sections, `Clean safe` (default) / `Clean all` / `Cancel`, both Clean actions act immediately. This also merges what were two separate change proposals (a nuke-time warning, and a standalone cleanup UI) into one, since both now converge on the same shape: classify → show a list → let the operator pick a scope → act immediately.

Two existing pieces this builds directly on:

- `internal/agent/prune.go`'s `PrunePrepare`/`PrunePlan.Run` + `internal/db/tasks.go`'s `DB.PruneCompleted()` — the already-hardened deletion path (live-Hera-binding exclusion so a still-bound task's row is never deleted out from under its Hera role; the BUG-062-informed bounded-concurrency worktree/branch removal). Today this is hardwired to "every task currently `status=complete`" — there is no way to hand it an explicit candidate list.
- `heraOpenDelete`/`heraNukeRole` (`internal/tui/heraactions.go`) — the existing single-role nuke confirm-then-act flow, today a plain y/N `ConfirmModal`.

## Goals / Non-Goals

**Goals:**

- One reusable popup component (NOT-SAFE / SAFE sections, Clean safe / Clean all / Cancel) driving two entry points: single-role nuke (n=1, Tier A only) and a new global Cleanup action (n=many, Tier A+B).
- Both entry points' Clean actions delete immediately — no intermediate "flip to complete and wait" state anywhere in this design.
- Zero duplication of deletion safety logic: the global Cleanup's delete step and the existing `Ctrl+R` flow share the exact same guarded primitive.
- Never a hard block: NOT-SAFE tasks are always cleanable via `Clean all`, an explicit, informed override.

**Non-Goals:**

- No change to cascade nuke (`Ctrl+D` on a coordinator header) or clear-archived (`C`) beyond the count augmentation already scoped for them — see Decision below for why a partial-cascade version of this popup is explicitly out of scope here.
- No Tier B (network) calls from the single-role nuke path — same rationale as before: an interactive, common action must never wait on `gh`/GitHub.
- No web PWA / macOS parity for the new global Cleanup surface in this stage (named Non-Goal, per Frontend Parity).
- No per-row toggle-then-confirm inside a section (deselecting individual tasks). Two bulk scopes (safe / all) match Aaron's exact framing; per-row selection remains a plausible, larger future enhancement.

## Decisions

**Decision: generalize `PruneCompleted` to an explicit-ID-list primitive; keep the existing all-complete sweep as a thin wrapper over it.**

`DB.PruneCompleted()`'s SQL hardcodes `status='complete'`; the global Cleanup's candidates are `status='in_review'` (the stuck-task predicate), so the existing function can't be called as-is. Alternatives considered:

- *Flip each selected task's status to `complete` first, then call the existing `PruneCompleted()` unconditionally.* Rejected: `PruneCompleted()` would then also sweep up any OTHER already-complete task in the system that happens to have no live Hera binding — collateral deletion of tasks the operator never looked at or chose to clean, entirely outside what this popup showed them. Silently expanding the blast radius past what was reviewed is exactly the failure mode this whole workstream exists to prevent.
- *Add a new, separate delete-with-guards function just for this popup.* Rejected — this is precisely "fork a second deletion path," which both Aaron's direction and this repo's own established caution (multiple prior BUG-0xx fixes hardening the ONE existing prune path) argue against.
- *Add `DB.PruneTasks(ids []string) (pruned []*model.Task, skipped int, err error)`* — re-verifies the live-Hera-binding guard per ID at delete time (never trusting a caller's earlier snapshot) and deletes exactly (a subset of) the given IDs, nothing else. `DB.PruneCompleted()` becomes: look up all `status='complete'` IDs, then call `PruneTasks` with them — identical behavior, zero test changes needed for the existing `Ctrl+R` flow. `internal/agent/prune.go`'s `PruneOptions` gains an optional `TaskIDs []string` field: when set, `PrunePrepare` sources its task list from `database.PruneTasks(opts.TaskIDs)` instead of `database.PruneCompleted()`; the slow worktree/branch-removal phase (`PrunePlan.Run`) is completely unchanged either way — it already just iterates whatever `toClean` list `PrunePrepare` computed.
- Note what does NOT change: `PruneTasks` re-verifies only the live-Hera-binding guard (the one invariant that's genuinely unsafe to skip — a dangling Hera pointer). It does not also re-check `status`/`archived`, since those predicates differ by caller (`complete` for `Ctrl+R`, the stuck-task predicate for the popup) and are each caller's own responsibility to have already selected correctly — mirroring the popup's own snapshot-then-reverify pattern below.

**Decision: the global Cleanup action re-verifies the stuck-task predicate per task immediately before calling `PruneTasks`, using the last-computed (cached) classification snapshot rather than a fresh live reclassification.**

"What the operator saw is what they acted on." The classification (Tier A/B, potentially seconds of network latency) runs once, on demand, and is cached; Clean actions consume that cached snapshot. A task that stopped matching the stuck-task predicate between classification and the Clean click (e.g. someone else already touched it) is silently skipped, not erred on — matching `PruneTasks`'s own skip-not-error contract for a task whose live-binding guard now fails.

**Decision: cascade nuke and clear-archived do NOT get the popup — they keep an aggregate count, unchanged mechanics.**

A cascade or clear-archived subtree is nuked/cleared as a single all-or-nothing unit today: the whole subtree's roles, orchestrator, and tasks. Offering `Clean safe` there would mean partially nuking a subtree — leaving some roles/tasks nuked and others (the NOT-SAFE ones) alive under a coordinator whose other children are gone — a materially different, more complex feature (does the orchestrator survive with fewer children? does a partially-nuked coordinator's own role status change?) that Aaron's direction never asked for; he specifically called out "the single-task nuke confirm," not cascade. Extending the aggregate-count treatment (confirmed/not-confirmed counts folded into the existing message, Tier A, never blocking) to those two entry points, unchanged from the original scoping, closes the actual gap (visibility before a bulk nuke) without inventing partial-subtree semantics nobody asked for. If Aaron wants that later, it's a clean, separate follow-up.

**Decision: single-role nuke's popup, at n=1, still exposes all three actions (not a specialized 2-button variant).**

Keeping one popup component (not two: "the real one" plus "a single-task-shaped one") means less code and one thing to get right. At n=1: if the task is SAFE, both `Clean safe` and `Clean all` behave identically (the safe set already IS the whole set); if NOT-SAFE, `Clean safe` is a no-op (empty scope, nothing happens — the button stays present but visibly acts on zero items) and `Clean all` is what actually proceeds, mirroring exactly the "warn but always allow" contract the original nuke-warning direction asked for, just phrased through the shared two-button vocabulary instead of a bespoke y/N.

**Decision: single-role popup opens only after Tier A finishes (compute-first), not immediately with an async update.**

Unchanged reasoning from the earlier draft: Tier A is local-only and fast; computing before opening avoids needing a message/list-update capability on an already-open modal, and mirrors the codebase's existing `fetchGitStatus` goroutine → `QueueUpdateDraw` idiom.

**Decision: the global Cleanup action's classification is daemon-side, on-demand, cached — same as the earlier draft.**

No change from the original reasoning: `task_meta`-cached results (surviving a restart), triggered by opening the popup or an explicit refresh, never a standing poll loop, SAFE cached as terminal, NOT-SAFE re-checked on the next compute.

## Risks / Trade-offs

- **[Risk]** `Clean all` on the global backlog is a genuinely destructive, no-undo (worktree/branch/DB row gone) action reachable from a palette command. → **Mitigation**: the popup shows exactly what's about to be deleted (both sections, with reasons) before either Clean action is available — this is the whole point of the popup existing — and `Clean all` is never the default-selected action (`Clean safe` is), so an operator moving fast and hitting the default doesn't accidentally delete unconfirmed work.
- **[Risk]** Generalizing `PruneCompleted`/`PrunePrepare` touches code with a real incident history (BUG-062 bulk cascade-nuke freeze). → **Mitigation**: the refactor is additive (a new explicit-ID-list entry point) and preserves the existing all-complete path as a thin wrapper with zero behavior change — the existing test suite for `Ctrl+R` continues to prove the concurrency/guard behavior is intact; new tests cover only the new explicit-list entry point.
- **[Risk]** A task selected for Clean-all/Clean-safe could have its worktree/branch removal partially fail (e.g. a locked file) after its DB row is already scheduled for deletion. → **Mitigation**: this is pre-existing `PrunePlan.Run` behavior (best-effort worktree removal, logged not fatal) — unchanged by this design, not a new risk introduced here.

## Migration Plan

Additive: new popup, new REST endpoints, a backward-compatible refactor of `PruneCompleted`/`PrunePrepare` (existing callers unaffected). Depends on `add-merge-safety-classifier`. No rollback concerns beyond reverting the commit.

## Open Questions

None outstanding — Aaron's redirect resolved the prior open decision (immediate delete, not two-step), and the secondary opens from the earlier draft (palette-only reachability, Safe-as-terminal caching) stand as originally recommended, per the coordinator's confirmation.
