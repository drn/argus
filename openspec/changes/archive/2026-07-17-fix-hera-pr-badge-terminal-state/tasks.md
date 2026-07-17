## 1. Shared PR-actionable predicate

- [x] 1.1 Add `model.PRState.IsActionable()` in `internal/model/prstate.go` —
  true for `PRAwaitingReview` / `PRChangesRequested` / `PRApproved`, false for
  every other state. Single source of truth for "does this PR state deserve a
  live badge."
- [x] 1.2 `theme.PRGlyph` delegates its `ok` flag to `IsActionable()` instead
  of re-enumerating the three states inline (behavior unchanged for the task
  list; now backed by the shared predicate).

## 2. Fix the Hera rail and details indicators

- [x] 2.1 `internal/tui/hera/rail.go` `rolePR()` parses `prMeta[taskID]["state"]`
  via `model.ParsePRState` and returns `IsActionable()` instead of checking
  `url != ""`.
- [x] 2.2 `internal/tui/hera/details.go` `roleMark()` same fix — the `PR` mark
  is appended only when the parsed state is actionable.

## 3. Tests

- [x] 3.1 `internal/model/prstate_test.go` — table test for `IsActionable()`
  across all `PRState` values.
- [x] 3.2 `internal/tui/hera/rail_test.go` — update
  `TestRail_PRIndicatorOnManagedRow` to set a `state`, and add
  `TestRail_RolePR_PRStateTable` covering merged-closed/draft/unknown/empty →
  false and the three actionable states → true.
- [x] 3.3 `internal/tui/hera/details_test.go` — update
  `TestDetails_NilOrchAndMarks` (the old assertion encoded the bug: `{"url":
  "u"}` with no `state` produced `"ready PR"`) and add
  `TestDetails_RoleMark_PRStateTable` mirroring the rail table.

## 4. Docs

- [x] 4.1 `context/knowledge/gotchas/hera-view.md` — rewrite both PR-indicator
  bullets (roster + rail) to document the state-gated behavior and the
  url-retained-after-merge poller detail that caused the bug.
- [x] 4.2 README — checked; the only README PR-badge mention is task-list
  screenshot alt-text, already accurate. No change needed.
