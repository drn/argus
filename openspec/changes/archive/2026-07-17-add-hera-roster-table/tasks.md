## 1. Render the roster as an aligned table

- [x] 1.1 `internal/tui/hera/details.go`: add `rosterCols` + `computeRosterColumns`
  (widest-cell sizing capped at per-column maxima, shrinking model → archetype →
  name → status when the pane is narrower than the ideal total), `rosterColStarts`,
  `rosterTruncate` (rune-safe), `archetypeDisplay`/`modelDisplay` (`—` when empty),
  `rosterStatusText` (mirrors `widget.RoleStatusIcon`'s precedence so the icon and
  the label never disagree; folds in the PR mark), `drawRosterHeader`, and
  `drawRosterRow`.
- [x] 1.2 Replace the bullet-row roster draw in `DetailsView.Draw` with the header +
  per-row table draw; replace `roleMark` with `hasPR` (the PR-only predicate the
  new status-text composer calls).
- [x] 1.3 `ContentHeight()`: add the column-header row to the roster's row budget
  whenever the roster is non-empty, keeping it in exact lockstep with `Draw`.

## 2. Tests

- [x] 2.1 Unit tests: `rosterTruncate` (ASCII + rune-safe multibyte truncation,
  zero/negative width, exact fit), `archetypeDisplay`/`modelDisplay` (empty → `—`),
  `rosterStatusText` (precedence matches `widget.RoleStatusIcon`, including
  needs-input outranking ready_to_close, and the PR suffix composing with any
  status), `computeRosterColumns` (content-sized within caps, non-negative at any
  width, fits within `avail` once `avail` covers the fixed gutter/gap overhead).
- [x] 2.2 Render (SimulationScreen) test: a coordinator's roster shows the
  `STATUS`/`NAME`/`ARCHETYPE`/`MODEL` header and each worker's resolved
  archetype/model, with an unresolved worker rendering `—` rather than a blank
  cell.
- [x] 2.3 Render (SimulationScreen) test: an extreme-narrow pane draws without
  panicking (columns shrink toward zero rather than corrupting the layout).
- [x] 2.4 Update the existing `ContentHeight`/`roleMark` tests for the new row
  budget (+1 header row when the roster is non-empty) and the new
  `hasPR`/`rosterStatusText` helpers.

## 3. Docs

- [x] 3.1 README Reference appendix: describe the roster as a table
  (status/name/archetype/model) instead of bullet marks.
- [x] 3.2 OpenSpec: `MODIFIED` the `hera-view` "PR indicator in the roster" and
  add an `ADDED` "Agents roster renders as an aligned table" requirement;
  archive into the base spec in this same PR.
