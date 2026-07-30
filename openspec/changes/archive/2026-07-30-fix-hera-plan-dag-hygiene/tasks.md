## 1. Bug A — planned node outlives its archived/nuked orchestrator

- [x] 1.1 `internal/db/hera_plan.go` `ListHeraPlannedNodes`: join `hera_orchestrators` and require `archived_at IS NULL AND nuked_at IS NULL` on the parent, in addition to the existing node-level filters.
- [x] 1.2 `internal/db/hera.go`: add an unexported `cancelStillPlannedChildRoles(orchID int64) error` helper mirroring `ListHeraPlannedNodes`'s planned-node definition (kind=worker, not archived, not cancelled, no binding ever), scoped to one orchestrator.
- [x] 1.3 Wire `cancelStillPlannedChildRoles` into `ArchiveHeraOrchestrator` and `NukeHeraOrchestrator`, logging (not propagating) a cascade failure so the primary archive/nuke call keeps its existing error contract.
- [x] 1.4 `internal/db/hera_plan_test.go`: add a test that a planned node under an archived orchestrator, and one under a nuked orchestrator, are excluded from `ListHeraPlannedNodes` — including the case where the node itself has no `cancelled_at` (proving the defensive read-path filter works independent of the cascade).
- [x] 1.5 `internal/db/hera_test.go`: extend the existing `TestArchiveHeraOrchestrator`/`TestNukeHeraOrchestrator` coverage (or add sibling tests) proving a still-planned child role gets `cancelled_at` stamped, while an already-materialized (bound) child role is left untouched.

## 2. Bug B — materializeNode escalates instead of retrying silently forever

- [x] 2.1 `internal/heragater/heragater.go`: add `materializeFailures map[int64]int` and `escalatedMaterializeFailures map[int64]bool` to `Watcher`, plus a `materializeFailureEscalationTicks` constant.
- [x] 2.2 Add `recordMaterializeFailure`/`clearMaterializeFailures` helpers; wire them into `materializeNode`'s failure/success paths.
- [x] 2.3 On crossing the threshold, send a one-shot coordinator notice (same `CoordinatorPinger` seam as `holdAndPing`) naming the node and the last error; never auto-cancel or reconfigure the node.
- [x] 2.4 Sweep both maps each `Tick()` for node ids no longer in the planned set (mirrors `rearmHeldPings`'s cleanup of `heldPings`).
- [x] 2.5 `internal/heragater/heragater_test.go`: add tests for under-threshold silence, one-time notice on crossing the threshold, no repeat notice on continued failure, count/escalation-state clearing on a later success, and sweep-on-node-removal.

## 3. Documentation

- [x] 3.1 Add a new entry to `context/knowledge/gotchas/orchestration.md` documenting: the parent-orchestrator liveness filter + cascade-cancel invariant (Bug A), the materialize-failure escalation counter/threshold (Bug B), and a short note that Bug C was data-hygiene only (no code change) with a pointer to why.
- [x] 3.2 Update `context/knowledge/index.md`'s orchestration.md row bullet count if it changes materially.

## 4. Verification

- [x] 4.1 `make pre-pr`: build/vet/fmt-check/lint-pr clean; vuln gate shows only stdlib-only advisory findings (CI runs `continue-on-error`, matching `context/knowledge/gotchas/ci-gates.md`); test-cover-gate's two `internal/agent` profile-env test failures are the documented pre-existing hera-worker-sandbox `ARGUS_TASK_ID`/`ARGUS_ARCHETYPE`/`ARGUS_MODEL` env-contamination artifact (unrelated package to this diff; confirmed via `go run ./scripts/coverfilter` against the run's own `coverage.out`: 88.7% filtered, above the 88% floor).
- [x] 4.2 Archive this change (`openspec archive fix-hera-plan-dag-hygiene`) in the same PR before merge.

## 5. Part 2 — retroactive data cleanup (not application code; run once against the live daemon DB)

- [ ] 5.1 Fresh backup: `cp ~/.argus/data.sql ~/.argus/data.sql.bak-<timestamp>` immediately before the mutation.
- [ ] 5.2 Through the daemon's own `*db.DB` connection (a small one-off Go program or an admin code path — not raw `sqlite3 UPDATE`): `CancelHeraPlannedNode(343)`, `CancelHeraPlannedNode(358)`.
- [ ] 5.3 `ArchiveHeraRole(813)`, `ArchiveHeraRole(814)`.
- [ ] 5.4 `EndHeraBinding(803, <reason>)`, `EndHeraBinding(804, <reason>)` with an end reason describing "task finished, binding never closed."
- [ ] 5.5 Leave role 184 completely untouched.
- [ ] 5.6 Verify via `sqlite3 -readonly ~/.argus/data.sql`: 343/358 no longer satisfy the planned-node query, 813/814 have non-null `archived_at`, bindings 803/804 have non-null `ended_at`, role 184 is unchanged.
- [ ] 5.7 Report role 184 back to Aaron as an open question (what project should it target) — do not resolve it.
