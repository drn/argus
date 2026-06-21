# Tasks — make-hera-plan-living

**Design doc:** `openspec/changes/make-hera-plan-living/design.md`

Three phases, each shipping as its own PR (the coordinator merges; squash-only on
drn/argus). TDD throughout: write failing tests from the deltas first, then
implement. Verify each phase with targeted `go test` per package during dev and
the exact CI linter (`$(go env GOPATH)/bin/golangci-lint run
--new-from-rev=origin/master ./...` → 0 issues). Report each phase green to the
coordinator with branch + sha; do not push or open PRs directly.

## 1. Phase A — Tests (status trust)

Write failing tests from the `hera-messaging` and `hera-coordination` deltas.

- [x] 1.1 `internal/db`: tests that `hera_role_status` accepts and round-trips `failed` (schema CHECK widened) — `UpsertHeraRoleStatus`/`HeraRoleStatusFor`.
- [x] 1.2 `internal/mcp` (hera_send): failing tests — worker→coord send with no `status` errors; `status` applied synchronously to the sender role before return; `status=done` rolls the worker task to in_review + ready_to_close; `status=failed` rolls to in_review WITHOUT ready_to_close; coordinator send needs no status.
- [x] 1.3 `internal/mcp` (hera_status): failing tests — `failed` is accepted; invalid status names all five values; worker `failed` roll (in_review, no ready_to_close).
- [x] 1.4 `internal/heragater`: failing tests — a blocker with role-status `failed` holds the dependent + pings (explicit, before session death); a planned dependent re-waits when a done blocker returns to working; held dedup clears on blocker recovery and re-pings on re-failure; exactly one "unblocked" notice on recovery; no notice for an already-materialized node whose blocker reopens.
- [x] 1.5 `internal/tui/hera` + `internal/tui/widget`: failing test that a role with status `failed` renders the red ✕ glyph at the right precedence (below ready_to_close, distinct from done).
- [x] 1.6 Confirm every Phase-A `it should X` acceptance criterion in design.md has a failing test (Prove-It).

## 2. Phase A — failed role-status (schema + store + rail glyph)

**Depends on:** Stage 1

- [x] 2.1 `internal/db/schema.go`: widen the `hera_role_status.status` CHECK to include `'failed'`; add the `HeraStatusFailed` constant in `internal/db/hera.go`.
- [x] 2.2 `internal/tui/widget/rolestatusicon.go`: add a `Failed` input + a red ✕ glyph case, placed below `ReadyToClose`/`NeedsInput` and distinct from `Done`; wire `roleStatusInputs` in `internal/tui/hera/rail.go` to set it from `role.Status == HeraStatusFailed`.
- [x] 2.3 uxlog: log failed-status transitions where other status transitions are logged.

## 3. Phase A — required synchronous send-status

**Depends on:** Stage 2

- [x] 3.1 `internal/mcp/hera.go`: add the `status` parameter to the `hera_send` tool def + handler; require it for worker/freelance senders (error naming the five values); ignore-or-accept for coordinators.
- [x] 3.2 Apply the sent status synchronously in the handler via the shared upsert-and-roll path (reuse the `hera_status` logic / `RollHeraWorkerToReview` family) BEFORE returning; soft-fail so a roll error never blocks the send.
- [x] 3.3 Add the `failed` worker roll sibling: roll to in_review WITHOUT `ready_to_close` (in_progress-gated, worker-kind-only, idempotent, soft-fail). Factor with `RollHeraWorkerToReview` so the invariants can't drift.
- [x] 3.4 `internal/mcp/hera.go` (hera_status): accept `failed`; route the worker `failed` roll through the same sibling.
- [x] 3.5 uxlog on the send-status apply (success + soft-fail).

## 4. Phase A — gater failure gating, re-arm, recovery notice

**Depends on:** Stage 2

- [x] 4.1 `internal/heragater/heragater.go` `blockerOutcome`: return `blockerFailed` directly when `HeraRoleStatusFor(blocker).Status == failed`, BEFORE the session-death inference path.
- [x] 4.2 Re-arm: each tick, for every `(node, blocker)` key in `heldPings`, clear it when the blocker's outcome is no longer `blockerFailed` (recovered, or edge/role gone).
- [x] 4.3 Recovery notice: when a key clears because the blocker RECOVERED, emit exactly one "unblocked: node X's blocker Y recovered" message to the coordinator (FROM the held node's role, reusing the `ping` seam).
- [x] 4.4 Confirm no notice path exists for an already-materialized node whose blocker reopens (assert via test).
- [x] 4.5 uxlog on re-arm clears and recovery notices.
- [x] 4.6 Full Phase-A verification: `go test ./internal/db/... ./internal/mcp/... ./internal/heragater/... ./internal/tui/...` green; linter 0 issues. Report Phase A green to coordinator.

## 5. Phase B — Tests (mutation verbs)

**Depends on:** Stage 4

Write failing tests from the `hera-coordination` (mutation-verb + cancelled) and
`hera-view` (cancelled rendering) deltas.

- [ ] 5.1 `internal/db`: failing tests for `RemoveHeraBlock(blocked, blocker)` (removes one edge; missing edge is a no-op), `SetHeraRolePrompt`/`UpdateHeraPlannedNode` (edits prompt/project), and the `cancelled_at` column (`CancelHeraPlannedNode`; `ListHeraPlannedNodes` excludes cancelled; `ListHeraBlocks`/render still surface it).
- [ ] 5.2 `internal/heragater`: failing tests — a cancelled node never materializes; a dependent whose only unsatisfied blocker is cancelled becomes eligible.
- [ ] 5.3 `internal/mcp` (hera_plan.go): failing tests — `hera_plan_node_update` edits a planned node, rejects a materialized node; `hera_unblock` drops an edge idempotently; `hera_plan_node_cancel` cancels a planned node, rejects a materialized one; all three reject non-coordinator callers.
- [ ] 5.4 `internal/tui/planview` + `internal/tui/hera`: failing SimulationScreen/projection test — a cancelled node renders a distinct grey ✕, kept visible, distinct from failed.
- [ ] 5.5 Confirm every Phase-B `it should X` criterion has a failing test.

## 6. Phase B — store additions (edge-remove, prompt-edit, cancelled marker)

**Depends on:** Stage 5

- [ ] 6.1 `internal/db/schema.go`: add `cancelled_at TEXT` to `hera_roles` + `idx_hera_roles_cancelled`.
- [ ] 6.2 `internal/db/hera_plan.go`: `RemoveHeraBlock(blocked, blocker int64) error` (DELETE on `hera_blocks`, idempotent).
- [ ] 6.3 `internal/db/hera_plan.go` / `hera.go`: `SetHeraRolePrompt`/`UpdateHeraPlannedNode` (prompt + project edit, planned-only path).
- [ ] 6.4 `internal/db/hera_plan.go`: `CancelHeraPlannedNode` (stamp `cancelled_at`); extend `ListHeraPlannedNodes` with `AND cancelled_at IS NULL`.
- [ ] 6.5 `internal/heragater`: `blockerOutcome` treats a cancelled blocker as non-gating (dependent proceeds).
- [ ] 6.6 uxlog on each store mutation.

## 7. Phase B — MCP mutation verbs

**Depends on:** Stage 6

- [ ] 7.1 `internal/mcp/hera_plan.go`: `hera_plan_node_update` handler + tool def (coordinator-guarded; reject if materialized).
- [ ] 7.2 `internal/mcp/hera_plan.go`: `hera_unblock` handler + tool def (coordinator-guarded; idempotent).
- [ ] 7.3 `internal/mcp/hera_plan.go`: `hera_plan_node_cancel` handler + tool def (coordinator-guarded; reject if materialized).
- [ ] 7.4 Register the three verbs in `heraToolDefs` / the CallTool dispatcher.
- [ ] 7.5 uxlog on each verb.

## 8. Phase B — planview cancelled rendering

**Depends on:** Stage 6

- [ ] 8.1 `internal/tui/hera` projection (`heraPlanNodes`): map a `cancelled_at` role to a new `planview` cancelled state.
- [ ] 8.2 `internal/tui/planview`: render the cancelled state as a distinct grey ✕ (rune-slice iteration; full-rect; no Sync — follow dag-rendering.md).
- [ ] 8.3 README Reference appendix: add the three MCP verbs to the hera tool table.
- [ ] 8.4 Full Phase-B verification: targeted `go test` green; linter 0 issues. Report Phase B green to coordinator.

## 9. Phase C — authority + docs

**Depends on:** Stage 8

- [ ] 9.1 `.claude/skills/hera/SKILL.md`: state the plan-DAG is authoritative for a bound coordinator; document the required `hera_send` status; document `hera_plan_node_update` / `hera_unblock` / `hera_plan_node_cancel`; add a "keep the DAG reconciled" standing order; reframe the create-only language.
- [ ] 9.2 `context/knowledge/gotchas/orchestration.md`: status-on-send synchronous apply; `failed` status + explicit gating; re-arm + recovery notice; cancelled-node semantics.
- [ ] 9.3 `context/knowledge/gotchas/dag-rendering.md`: cancelled node grey ✕ rendering rules.
- [ ] 9.4 `context/knowledge/gotchas/hera-view.md`: failed glyph precedence; cancelled node projection.
- [ ] 9.5 `context/knowledge/gotchas/messaging.md`: required worker→coord send-status + synchronous apply + soft-fail.
- [ ] 9.6 README Reference: add the `hera_send` `status` parameter to the tool table.
- [ ] 9.7 Report Phase C green to coordinator.
