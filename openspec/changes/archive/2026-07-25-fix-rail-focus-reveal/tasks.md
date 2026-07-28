## 1. Model helpers

- [x] 1.1 Add `Model.roleByID(id int64) *RoleView` — pointer into the model's backing array, scoped to Pinned/Active/Archived (mirrors `roleOrchID`'s exclusion of Freelance).
- [x] 1.2 Add `OrchView.bridgingRoleFor(bridgeTaskID string) *RoleView` — returns the non-coordinator role in `o.Roles` whose structurally-intact bridge task matches, or nil (mirrors `hasWorkerBridging`'s predicate but returns the role).

## 2. Sticky reveal

- [x] 2.1 Add `Rail.applyStickyReveal(ref int64)`: for a positive ref (role id), force that role's own `SubtreeNeedsInput` true, then walk its orchestrator's `canonicalParents()` chain up to the root, forcing each ancestor `OrchView.SubtreeNeedsInput` true and, for a worker-bridge (non-coord-spawn) hop, the parent's bridging role's `SubtreeNeedsInput` true too. For a negative ref (orchestrator header id), walk the same chain starting at that orchestrator. Zero/unresolvable ref is a no-op.
- [x] 2.2 Wire the call into `Rail.SetModel`, after resolving `prev` (including the `pendingSelRef` one-shot override) and before `buildRows`.

## 3. Tests

- [x] 3.1 Regression test: a role revealed only via the partial-fold reveal, selected, stays selected and visible after its own needs-input flag clears on the next `SetModel` (with nothing else in the model needing input).
- [x] 3.2 Regression test: the same scenario, but nested two levels deep (worker-bridge sub-coordinator), confirming the sticky chain forces the intermediate bridging row too.
- [x] 3.3 Non-regression test: once the cursor moves to a different row before the next rebuild, the previously-sticky row folds away normally (no permanent pin).
- [x] 3.4 Non-regression test: an orchestrator header selection (not a role) is sticky the same way.
- [x] 3.5 Confirm the full existing `rail_reveal_test.go` / `excursion_test.go` suites still pass unchanged (no behavior change when nothing needs to stick).

## 4. Docs

- [x] 4.1 Add a gotcha bullet to `context/knowledge/gotchas/hera-view.md` documenting the sticky-reveal mechanism and why it reuses `SubtreeNeedsInput` rather than adding parallel gates.

## 5. Archive

- [x] 5.1 Run `openspec archive fix-rail-focus-reveal` (or the manual merge-into-base-specs + move-to-archive fallback) before merge, in the same PR.
