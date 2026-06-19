# Tasks

## 1. Authoritative needs-input source into the model

- [x] 1.1 Add `RoleView.NeedsInput` (own, from the authoritative set) and `RoleView.SubtreeNeedsInput` (rollup) to `internal/tui/hera/model.go`.
- [x] 1.2 Add `RoleView.needsInputOwn()` (`NeedsInput || blocked status`) and `RoleView.ShowsNeedsInput()` (`SubtreeNeedsInput || needsInputOwn`).
- [x] 1.3 `BuildModel(r HeraReader, needsInput map[string]bool)`: stamp `rv.NeedsInput = needsInput[taskID]` for live roles in `buildRoleView`.

## 2. Rollup in BuildModel

- [x] 2.1 Add `Model.rollupNeedsInput()` post-pass: per-orch subtree rollup via `orchSubtreeNeedsInput` (walks `BridgeSubtree`, cycle-safe), then stamp each role's `SubtreeNeedsInput` (coordinator → orch subtree; bridging worker → own || child subtree; leaf/freelance → own).
- [x] 2.2 Call `rollupNeedsInput` at the end of `BuildModel`.

## 3. Rail projection + precedence

- [x] 3.1 `statusIcon` needs-input branch reads `role.ShowsNeedsInput()` (rollup OR own), ranked below `ready_to_close`, above `done`/active/idle/live. Document the precedence in the function comment.

## 4. Thread the set from the App

- [x] 4.1 `HeraPage.SetNeedsInput(ids []string)` stores a `needsInput map[string]bool`; `doRefresh` passes it to `BuildModel`.
- [x] 4.2 App tick pushes `a.needsInputIDs` via `a.heraPage.SetNeedsInput` next to the existing `a.tasklist.SetNeedsInput`.

## 5. Tests

- [x] 5.1 `model_test.go`: a descendant worker in needs-input makes its parent coord AND the root coord report needs-input; clears when resolved.
- [x] 5.2 `model_test.go`: multi-level (≥2) bridged subtree propagates across bridges; no false-positive when no descendant needs input; cycle-safe.
- [x] 5.3 `rail_test.go`: `statusIcon` glyph for own/rollup needs-input; precedence vs ready_to_close/done.
- [x] 5.4 `page_test.go`: `SetNeedsInput` threads through `doRefresh` to the rendered glyph.

## 6. Docs + validate

- [x] 6.1 `context/knowledge/gotchas/hera-view.md`: document the needs-input subtree rollup, cross-bridge traversal, and precedence.
- [x] 6.2 `openspec validate hera-needsinput-rollup --strict` passes.
- [x] 6.3 `make pre-pr` passes clean.
