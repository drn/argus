## 1. Loosen the rail surfacing gates

- [x] 1.1 `buildRoleView` (internal/tui/hera/model.go): change the worker gate to `taskInProgress || rv.Live` so a live role of any kind surfaces needs-input when it is in the content-aware set; rewrite the gate comment to explain BUG-023 is now guarded by liveness + content-awareness.
- [x] 1.2 `needsInputForHeraRail` (internal/tui/app.go): admit any hera-bound task (worker OR coordinator) regardless of status (`heraManaged` union), not just coordinators; update the call site to pass `mergeManagedFromMeta(heraWorkers, heraCoordinators)`.

## 2. Fix the status-icon precedence

- [x] 2.1 `RoleStatusIcon` (internal/tui/widget/rolestatusicon.go): rank `NeedsInput` above `ready_to_close` so an actively-blocked worker shows `(?)` instead of the review glyph; update the doc comment.

## 3. Tests (TDD)

- [x] 3.1 Repro at the BuildModel seam: a live worker rolled to `in_review` (binding still live) and in the needs-input set surfaces `NeedsInput=true` and rolls up to the coordinator.
- [x] 3.2 Regression at the BuildModel seam: an EXITED worker (binding ended) does NOT surface `(?)` even though its task lingers in the set (BUG-023 via liveness).
- [x] 3.3 Admission unit test: `needsInputForHeraRail` keeps in_progress AND any hera-bound task (worker/coordinator) regardless of status; drops plain finished + unknown tasks.
- [x] 3.4 Precedence tests (widget + rail): needs-input wins over `ready_to_close`; `ready_to_close` still shows the review glyph when not blocked.
- [x] 3.5 End-to-end render test: a live `in_review` worker with `ready_to_close` stamped + a blocked session log surfaces the needs-input glyph on the rail (proves all three layers).
- [x] 3.6 Reframe the existing in_review→suppressed worker tests to the new exit-based BUG-023 contract.

## 4. Docs & gates

- [x] 4.1 Document the invariant in context/knowledge/gotchas/events.md (and/or hera-view.md).
- [x] 4.2 `make pre-pr` green; `openspec validate --all --strict` passes.
- [x] 4.3 Archive this change within the branch (base specs updated atomically).
