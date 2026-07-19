**Design doc:** `openspec/changes/fix-hera-view-status-spec-drift/design.md`

This is a spec-text-only change (no code changes) — there is no implementation
stage. Stage 1 verifies the delta's accuracy against the cited code; Stage 2
archives it.

## 1. Verification

- [x] 1.1 Re-read the delta's "Status-icon precedence on role rows" requirement against `internal/tui/widget/rolestatusicon.go:46,64-93` and confirm the precedence order (`NeedsInput > Active > ReadyToClose > Failed > Done > Idle > Live > default`) and every scenario match the code exactly.
- [x] 1.2 Re-read the delta's "Needs-input (?) propagates up..." requirement against `internal/tui/hera/model.go:1020-1057` and confirm the worker/coordinator/freelance liveness-based description (no worker-only `in_progress` carve-out) and the corrected precedence sentence match the code. Two inaccuracies found and corrected: (a) the admission-feed sentence said "a hera coordinator role" but the code (`internal/tui/app.go` `needsInputForHeraRail`/`mergeManagedFromMeta`) admits coordinator OR worker; (b) the requirement's blanket claim included "freelance" but `readHeraRoles`/`readManagedTasks` never populate a freelance map, so the requirement was rescoped to worker/coordinator only (design.md D2 corrected to match).
- [x] 1.3 Re-read the delta's "Needs-input (?) CLEARS..." requirement against `internal/tui/hera/model.go:1024-1029,1054` and confirm the content-aware `needsInputIDs` + liveness-ends-on-session-exit mechanism is described accurately, with no remaining `task.Status == in_progress` gate claimed anywhere in the requirement.
- [x] 1.4 Re-read the delta's "Active agents animate a spinner glyph" requirement against `internal/tui/hera/model.go:135-170` (`RoleView.IsActive`) and confirm the `Live && SessionRunning && !SessionIdle` definition, the BUG-C in_review-still-spins scenario, and the BUG-F ready_to_close/failed/done-does-not-outrank-active correction all match the code.
- [x] 1.5 Confirm `openspec validate fix-hera-view-status-spec-drift --strict` passes.

## 2. Archive

**Depends on:** Stage 1

- [x] 2.1 Run `openspec archive fix-hera-view-status-spec-drift` (merges the delta into `openspec/specs/hera-view/spec.md`, moves the change folder to `openspec/changes/archive/<date>-fix-hera-view-status-spec-drift/`), committed on this branch.
- [x] 2.2 Run `openspec validate --all --strict` and confirm it passes against the merged base spec. (57/57 passed)
