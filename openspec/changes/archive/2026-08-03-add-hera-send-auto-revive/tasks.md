**Design doc:** `openspec/changes/add-hera-send-auto-revive/design.md`

## 1. Tests (write failing first)

- [x] 1.1 `internal/mcp/hera_test.go`: dead explicit-`to` recipient is restarted before send succeeds (assert `fakeHeraReviver` called with the recipient's task id + kind, and the response contains `- **revive**:` plus `restarted_dead`).
- [x] 1.2 Busy/blocked/live-coordinator recipient: revive attempt made (reviver called), send still succeeds, response reports the skip outcome.
- [x] 1.3 Recipient with no live binding (planned role, `db.ErrHeraNotFound` from `HeraLiveBindingByRole`): reviver NOT called, no `- **revive**:` line, send still succeeds.
- [x] 1.4 `s.heraRevive == nil` (reviver not wired): no error, no `- **revive**:` line, send still succeeds exactly as today.
- [x] 1.5 Worker/freelance default-route send (no explicit `to`): reviver NOT called even when the target coordinator has a live binding.
- [x] 1.6 Coordinator self-send (`to` resolves to caller's own role): reviver NOT called; existing self-send error path is unchanged.
- [x] 1.7 A revive call error (fakeHeraReviver returns an error): send still succeeds, no `- **revive**:` line.
- [x] 1.8 Confirm every scenario in `specs/hera-messaging/spec.md`'s new requirement has a corresponding failing test before moving to implementation.

## 2. Implement auto-revive in `toolHeraSend`

**Depends on:** Stage 1

- [x] 2.1 `internal/mcp/hera.go`, `toolHeraSend`: after recipient resolution, before `s.heraSvc.Send`, add the gated auto-revive attempt per design.md D1/D2 — `caller.role.Kind == db.HeraKindCoordinator && p.To != "" && toRole.ID != caller.role.ID`.
- [x] 2.2 Resolve `s.heraStore.HeraLiveBindingByRole(toRole.ID)`; fold `db.ErrHeraNotFound` into a silent/Info-level skip; any other error → Warn log and skip.
- [x] 2.3 When a binding is found and `s.heraRevive != nil`, call `s.heraRevive(HeraReviveInput{TaskID: binding.ArgusTaskID, IsCoordinator: toRole.Kind == db.HeraKindCoordinator})` exactly as `toolHeraRevive` does; on error, Warn log and proceed to send.
- [x] 2.4 On a successful revive attempt, capture the outcome string for the response and emit `slog.Info("[hera] revive", ...)` matching `toolHeraRevive`'s existing fields.
- [x] 2.5 In the response builder, append `- **revive**: <heraReviveOutcomeMessage(outcome, toRole.Name)>` immediately when a revive attempt succeeded; omit entirely otherwise.
- [x] 2.6 Run `go test ./internal/mcp/...`; confirm Stage 1 passes.

## 3. Verify and land

**Depends on:** Stage 2

- [x] 3.1 Run `make pre-pr`; fix any failures (build, vet, fmt-check, lint-pr, vuln, test-cover-gate).
- [x] 3.2 `context/knowledge/gotchas/messaging.md`: add a bullet noting hera_send's auto-revive-on-send reuses `hera.ReviveRole` verbatim with no new gating logic, and is soft-fail so a revive failure never blocks message delivery.
- [x] 3.3 `context/knowledge/index.md`: update the `gotchas/messaging.md` coverage-bullet cell to reflect the new bullet.
- [x] 3.4 `openspec archive add-hera-send-auto-revive` (or the manual merge-and-move fallback): merge the `hera-messaging` delta spec into `openspec/specs/hera-messaging/spec.md`, move the change folder to `openspec/changes/archive/<date>-add-hera-send-auto-revive/`, commit on the same branch before merge.
- [x] 3.5 Open the PR via `mcp__argus__iris_gh_pr_create`, base `master`.
