## Why

Tier A (local-ancestor) and Tier B (merged-PR lookup) can structurally never classify a Hera-descended worker task whose branch was folded into its coordinator's branch via a plain `git merge` and never got its own standalone PR: Tier B correctly finds no PR (none ever existed), and a commit-SHA-ancestry fallback doesn't help either, since `drn/argus` squash-merges every PR — a squash produces a brand-new commit whose parents don't include the original branch's commits, so neither the branch name nor its SHA is ever an ancestor of master again. This was found live and documented as an explicit open question in `openspec/changes/archive/2026-08-10-add-merge-safety-review/design.md`, pending a decision from Aaron on whether inferring safety from a coordinator's own confirmed-merged status is acceptable. He has now made that call (2026-08-10): allow it, bounded to a single hop, and never silently — the cleanup popup must visibly distinguish an inferred verdict from a directly-confirmed one.

## What Changes

- A new classification tier, `mergesafety.TierCoordinatorInferred`, usable only by the global Cleanup compute pass (`internal/api`'s `runCleanupCompute`) — the `internal/mergesafety` package itself gains only the constant, no new classification logic (it stays decoupled from Hera/DB concepts).
- After the existing Tier A/B pass, every candidate that classified not-safe AND belongs to a Hera orchestrator is grouped by orchestrator name. For each distinct orchestrator, its coordinator role's bound task is resolved once (new `db.CoordinatorTaskForOrchestrator`) and classified via the existing Tier A/B `mergesafety.Classify` — never recursively through this same coordinator-inference logic (a deliberate one-hop cap; no chain of inference through a grandparent orchestrator).
- If the coordinator's own verdict is safe, every one of that orchestrator's not-safe candidates is overridden to safe, tier `coordinator-inferred`, with a reason naming the coordinator task and its own tier/reason. If the coordinator's verdict is not-safe, or its task can't be resolved at all (e.g. pruned before this feature existed), every candidate under that orchestrator is left exactly as its own Tier A/B verdict said — fail closed, no error.
- The single-role nuke path (Tier A only, must never make a network call) explicitly does NOT get this tier — Tier C can require a Tier B network call to resolve the coordinator's own verdict, which that interactive path must never wait on.
- The merge-safety review popup (`internal/tui`) gains a `Tier` field on its candidate rows and renders a distinct trailing annotation on a SAFE row specifically when its tier is coordinator-inferred (e.g. "safe via coordinator") — every other SAFE row's rendering is unchanged. This is the visibility condition Aaron's decision was contingent on: the weaker-confidence inference must never be indistinguishable from a directly-confirmed verdict.
- No schema change and no new wire field: the JSON `tier` field already exists on the cleanup-candidates wire contract and already round-trips through `task_meta` — this adds only a new possible value.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `merge-safety`: adds the bounded, one-hop coordinator-inferred fallback tier to the global Cleanup compute pass.
- `hera-view`: the merge-safety review popup gains a visible annotation distinguishing a coordinator-inferred SAFE row from a directly-confirmed one — the condition Aaron's decision was contingent on.

## Impact

- `internal/mergesafety/classify.go`: one new exported constant, `TierCoordinatorInferred`. No behavioral change to `Classify`/`ClassifyBatch`/`ClassifyBatchFunc`.
- `internal/db/tasks.go`: new `CoordinatorTaskForOrchestrator` method.
- `internal/api/cleanup_candidates.go`: `runCleanupCompute` gains the coordinator-inference pass; `Server` gains a small test-seam function field for it (mirrors the existing `cleanupComputeFn` pattern).
- `internal/tui/mergesafetypopup.go` + `internal/tui/mergesafety.go`: `Tier` threaded through the candidate shape and the wire-mirroring struct; one new render branch for the coordinator-inferred annotation.
- `context/knowledge/gotchas/misc.md`: one bullet documenting the new tier and its one-hop cap.
