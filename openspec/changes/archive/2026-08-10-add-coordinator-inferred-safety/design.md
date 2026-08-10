## Context

`openspec/changes/archive/2026-08-10-add-merge-safety-review/design.md`'s Open Questions section documents a real, structural gap: a Hera worker task folded into its coordinator's branch via a plain `git merge` (never given its own standalone PR) is invisible to both existing tiers. Tier B correctly finds no PR — none ever existed. A commit-SHA-ancestry fallback was proposed and rejected there too: `drn/argus` squash-merges every PR to master, and squash produces a brand-new commit whose parents don't include any of the original branch's commits, so once a PR lands, neither the branch name nor its SHA is ever an ancestor of master again. That design explicitly deferred a decision to Aaron rather than silently guessing either way. He has now decided (2026-08-10): allow inferring a worker's safety from its coordinator's own confirmed-merged status, bounded to exactly one hop, and only ever surfaced with visible, distinguishable provenance in the popup — never silently folded into the same-looking SAFE row as a directly-confirmed verdict.

## Goals / Non-Goals

**Goals:**

- Close the folded-in-worker gap for the global Cleanup compute pass specifically, without weakening `internal/mergesafety`'s own "never guess toward safe" invariant — the inference lives entirely in the caller (`internal/api`), as one new label-only constant in `internal/mergesafety`.
- Cap the inference at exactly one hop: a worker's safety may be inferred from its own coordinator's Tier A/B verdict, never from a grandparent orchestrator's coordinator, and never by chaining through more than one coordinator-inference step.
- Make an inferred verdict visually distinguishable from a directly-confirmed one in the TUI popup — the condition Aaron's decision was contingent on.
- Classify each distinct coordinator exactly once per compute pass, regardless of how many of its workers are pending inference — this is normally a handful of orchestrators, not hundreds, so a single (non-batched) `mergesafety.Classify` call per orchestrator is the right shape, not a new batched primitive.

**Non-Goals:**

- No change to `internal/mergesafety`'s classification algorithm itself, and no new package there — `Classify`/`ClassifyBatch`/`ClassifyBatchFunc` are untouched. That package stays decoupled from Hera/DB concepts; `TierCoordinatorInferred` is a label only.
- No multi-hop inference. A not-safe coordinator whose own orchestrator has a grandparent coordinator does NOT get rescued by walking further up the tree. If Aaron wants that later, it's a clean, separate follow-up requiring its own explicit sign-off (the same reasoning that produced this decision applies again at each additional hop).
- No Tier C for the single-role nuke path (`internal/tui/mergesafety.go`'s `classifyNukeCandidate`, Tier A only) or for cascade nuke / clear-archived. Resolving a coordinator's own verdict can require a Tier B network call; those interactive/bulk-but-still-synchronous paths must never wait on `gh`/GitHub, exactly the reasoning that keeps them Tier-A-only today. Tier C only ever runs inside the global Cleanup action's async, already-batched, already-network-tolerant compute pass.
- No schema change, no new wire field. `cleanupCandidateJSON`'s `tier` field already exists on both the server and TUI-side mirror of the wire contract and already round-trips through `task_meta` — this only adds a new possible value.

## Decisions

**Decision: the coordinator-inference pass lives entirely in `internal/api/cleanup_candidates.go`'s `runCleanupCompute`, as a second pass after the existing Tier A/B `ClassifyBatchFunc` call — not inside `internal/mergesafety`.**

Alternatives considered: teaching `mergesafety.Classify` itself a "Tier C" that looks up a coordinator task. Rejected — that package has no notion of Hera orchestrators, roles, or bindings by design (it only knows repo dir / repo slug / branch / default ref), and giving it one would couple a deliberately-generic git/GitHub classifier to this codebase's own orchestration schema. Keeping the inference in the caller, and adding only a label constant to `mergesafety`, preserves that boundary.

**Decision: group not-safe, orchestrator-bearing candidates by orchestrator name; resolve and classify each orchestrator's coordinator task exactly once; apply the result to every candidate in that group.**

This is the natural shape given `StuckTaskCandidate.Orchestrator` (5a-cleanup-tree-view) already names each candidate's most-recent orchestrator. Grouping first means a coordinator with N stuck workers pays for exactly one `CoordinatorTaskForOrchestrator` DB lookup and one `mergesafety.Classify` call, not N of each — classifying it N times would be wasted work at best and, if that classification includes a Tier B network call, an N-times-larger unnecessary GraphQL cost at worst.

**Decision: resolve the coordinator task via a new `db.CoordinatorTaskForOrchestrator(name string) (*model.Task, bool, error)`, mirroring `StuckTaskCandidates`' own correlated-subquery style (`hera_orchestrators` by name → `hera_roles` `kind='coordinator'` → `hera_bindings`, most recent by `started_at`).**

Alternatives considered: reusing `StuckTaskCandidates`' own per-task orchestrator-resolution subquery in reverse (task → orchestrator) by scanning ALL tasks for one whose resolved orchestrator name matches. Rejected — that requires no new query but does an unbounded full-table correlated-subquery scan per orchestrator group instead of one indexed join; a direct orchestrator-name → coordinator-task query is simpler and cheaper. `ok=false` (never an error) is returned when no coordinator role/binding resolves for that name — e.g. a coordinator role pruned before this feature existed, or the name doesn't resolve at all — so the caller's fail-closed path is a plain early-continue, not error handling.

**Decision: the coordinator's own classification is Tier A/B only, via the existing `mergesafety.Classify` (single-candidate form) — never anything that itself performs coordinator inference.**

This is what makes the one-hop cap structural rather than merely a documented intention: `Classify` has no knowledge of orchestrators and cannot recurse into this feature's own grouping logic even in principle. There is no depth parameter to thread through and get wrong — the call graph itself cannot go past one hop.

**Decision: a small `Server.classifyCoordinatorFn` test-seam field, mirroring the existing `cleanupComputeFn` pattern, wraps the coordinator's own classification call.**

`internal/mergesafety`'s Tier B network seam (`fetchMergeCandidates`) is a package-private var — `internal/api` tests cannot reach it to assert call counts without either spinning up real `git`/`gh` processes or reaching into another package's internals. This codebase already has an established idiom for exactly this situation (`cleanupComputeFn`, added for `runCleanupCompute` itself): a `Server`-level function field, nil in production (resolving to the real implementation), overridable in tests. Applying the same idiom to the coordinator-classification call lets tests assert "classified exactly once per orchestrator" and "never a second hop" via a call-count/call-argument recorder, without coupling the test to `mergesafety`'s internals or spawning subprocesses.

**Decision: cache the coordinator-inferred override exactly like every other verdict — same `task_meta` keys, same terminal-safe caching rule.**

A coordinator-inferred `Safe=true` verdict is cached under the same `cleanupMetaSafe`/`cleanupMetaTier`/`cleanupMetaReason` keys as any other verdict, and — per the existing rule — a cached `safe=true` is terminal and never reclassified. This means a given orchestrator's coordinator is reclassified at most once per not-yet-rescued worker per compute pass; once rescued, that worker drops out of future passes entirely, same as any other confirmed-safe task.

**Decision: the TUI popup's `Tier` field is threaded through as a plain string, compared against a local constant — not an import of `internal/mergesafety`.**

`internal/tui/mergesafetypopup.go` already documents that it deliberately doesn't import `internal/mergesafety` (the popup is a pure display/choice widget; callers convert their own `Verdict` into the popup's own shape). A local `const mergeSafetyTierCoordinatorInferred = "coordinator-inferred"` mirrors the wire value verbatim, exactly like `Safe`/`Reason` already do, rather than breaking that boundary for one string comparison.

## Risks / Trade-offs

- **[Risk]** A coordinator whose own worktree/branch was already cleaned up classifies not-safe via "no repo resolvable", which would (correctly) block inference — but an operator might expect this to still work since the *coordinator's own PR* did in fact merge. → **Mitigation**: this is exactly the fail-closed behavior Aaron asked for: if the coordinator's own verdict can't be positively confirmed by the existing tiers, the worker is not rescued, no different from any other unconfirmable case. The coordinator's task_meta cache from an earlier pass (if it was ever classified safe while its repo was still resolvable) would already be terminal-cached and reused instantly here.
- **[Risk]** Silent behavior change if the popup annotation is missed or later regresses (e.g. a future refactor drops the `Tier` field). → **Mitigation**: `mergesafetypopup_test.go` gets a regression test asserting the annotation renders ONLY for `coordinator-inferred` SAFE rows and that every other SAFE row's rendering is byte-for-byte unchanged from before this change.
- **[Risk]** Grouping logic looks similar to (but must not reuse) `StuckTaskCandidates`' per-task orchestrator resolution, inviting a future maintainer to "simplify" by merging them and accidentally reintroducing an N-classifications-per-orchestrator regression. → **Mitigation**: the grouping code and its doc comment are explicit about the exactly-once contract, and the "classified exactly once" test pins it.

## Migration Plan

Additive only: one new constant, one new DB method, one new pass appended after existing classification, one new struct field threaded through an existing wire type that already carries the field, one new render branch. No schema change, no data migration, no rollback concerns beyond reverting the commit. A coordinator-inferred verdict cached under the new tier value simply reverts to being treated as an ordinary (if oddly-labeled) cached safe verdict if this change were ever reverted — never re-derived, never deleted, harmless either way.
