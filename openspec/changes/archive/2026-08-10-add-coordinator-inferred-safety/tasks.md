## 1. mergesafety: label-only tier constant

- [x] 1.1 Add `TierCoordinatorInferred = "coordinator-inferred"` to `internal/mergesafety/classify.go`, alongside `TierLocalAncestor`/`TierMergedPR`. No new classification logic in this package.

## 2. db: resolve a coordinator's bound task by orchestrator name

- [x] 2.1 Add `DB.CoordinatorTaskForOrchestrator(name string) (*model.Task, bool, error)` in `internal/db/tasks.go`, mirroring `StuckTaskCandidates`' correlated-subquery style; `ok=false`+nil error when unresolvable.
- [x] 2.2 Tests in `internal/db/db_test.go`: happy path resolves the bound task; most-recent-binding-wins when a coordinator role has been rebound; unresolvable orchestrator name returns `ok=false`, no error; a non-coordinator (worker) role binding is never returned.

## 3. api: coordinator-inferred pass in runCleanupCompute

- [x] 3.1 Add `Server.classifyCoordinatorFn` test-seam field (mirrors `cleanupComputeFn`), defaulting to a real Tier A/B `mergesafety.Classify` call over the coordinator task's resolved `Params`.
- [x] 3.2 In `runCleanupCompute`, after the existing `ClassifyBatchFunc` pass: group not-safe, orchestrator-bearing candidates by orchestrator name; for each group, resolve the coordinator task once, classify it once via the seam, and on a safe verdict override every candidate in the group to `Safe=true`/`TierCoordinatorInferred`/a reason naming the coordinator task + its own tier/reason; otherwise leave the group's candidates unchanged. Cache overrides via the existing `SetMetaBatch` path.
- [x] 3.3 Tests in `internal/api/cleanup_candidates_test.go`: coordinator confirmed safe rescues its workers; coordinator not-safe leaves candidates unchanged; unresolvable coordinator task leaves candidates unchanged (fail-closed, no error); two candidates sharing one orchestrator classify the coordinator exactly once (assert via the seam's call count); a not-safe coordinator whose own orchestrator has a grandparent does NOT trigger a second-hop lookup (assert the seam is never called for the grandparent).

## 4. tui: thread Tier through the popup and annotate coordinator-inferred rows

- [x] 4.1 Add `Tier string` to `mergeSafetyCandidate` (`internal/tui/mergesafetypopup.go`) and a local `mergeSafetyTierCoordinatorInferred` constant mirroring the wire value (no `internal/mergesafety` import).
- [x] 4.2 Add `Tier string `json:"tier,omitempty"`` to the client-side `cleanupCandidateJSON` mirror in `internal/tui/mergesafety.go` and thread it through `cleanupCandidatesToRows`.
- [x] 4.3 In `MergeSafetyPopup.drawRows`, render a distinct trailing annotation (e.g. "  (safe via coordinator)") on a SAFE row whose `Tier` is the coordinator-inferred constant; every other SAFE row's rendering unchanged.
- [x] 4.4 Tests: `mergesafetypopup_test.go` covers the new annotation renders only for coordinator-inferred SAFE rows, and a regression check that every other SAFE row's rendering is unchanged.

## 5. docs, gate, archive

- [x] 5.1 Add a bullet to `context/knowledge/gotchas/misc.md` naming the new tier, its one-hop cap, and why (squash-merge severs branch-name and SHA ancestry for folded-in Hera workers).
- [x] 5.2 `make pre-pr` clean (excluding the pre-existing ARGUS_* env-leak false-fail on 2 unrelated `internal/agent` tests).
- [x] 5.3 Archive this change into base specs in-branch (`merge-safety` gets the new ADDED requirement; `hera-view`'s "Merge-safety review popup" requirement gets the MODIFIED update) before the final commit.
