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

## 4. Make the roster scrollable (folded in mid-flight)

- [x] 4.1 `internal/tui/hera/details.go`: add `DetailsView.rosterScroll` /
  `rosterVisibleRows` fields; window the roster loop to `[rosterScroll,
  rosterScroll+budget)` instead of always starting at 0 (budget computed BEFORE
  the loop from the remaining row budget, independent of scroll position, so
  it's a stable clamp bound between Draw calls).
- [x] 4.2 Add `rosterMaxScroll`, `clampRosterScroll` (re-bounds every Draw
  against the live agent count + current pane height — the recompute-on-resize
  contract), and `ScrollRoster(delta)` (±1, returns false at either bound or
  before the first Draw has run).
- [x] 4.3 `SetOrch`: reset `rosterScroll` to 0 ONLY on a genuine orchestrator
  change (`orch.ID` differs, or nil↔non-nil) — NOT on a same-orchestrator
  refresh (the ~1s debounced tick re-selecting the same coordinator), so a
  mid-read scroll position never snaps back every tick.
- [x] 4.4 `internal/tui/hera/page.go`: `handleDetailsKey` tries
  `rosterScrollDelta` + `DetailsView.ScrollRoster` FIRST for j/k/Up/Down —
  the SAME keys the plan widget already binds for stage nav — falling through
  to `p.plan.InputHandler()` unchanged once the roster can't scroll further in
  that direction (or never needed to). No new focus sub-state; h/l/Enter/Space/Esc
  are untouched, always the plan widget's.

## 5. Tests (scroll)

- [x] 5.1 `internal/tui/hera/details_test.go`: `TestDetails_RosterScrolls` — a
  20-agent roster in a pane too short to show them all makes the last agent
  reachable after scrolling down and the first reachable again after scrolling
  back up, with `ScrollRoster` returning false at either bound.
  `TestDetails_ScrollRosterNoopsBeforeFirstDraw` (no Draw yet → no-op),
  `TestDetails_ScrollRosterNoopsWhenEverythingFits` (roster fits entirely →
  scroll keys never consumed), `TestDetails_SetOrchScrollReset` (same-orch
  refresh preserves scroll; a different orch resets it).
- [x] 5.2 `internal/tui/hera/dag_test.go`:
  `TestHandleDetailsKey_ScrollsRosterBeforePlan` — a 20-agent coordinator
  through the real `HeraPage.InputHandler()`: j/k scroll the roster to its
  bound while the plan widget's stage cursor never moves.

## 6. Docs (scroll)

- [x] 6.1 README Reference appendix: the plan-DAG key table's `↑`/`↓`/`j`/`k`
  row notes the roster-scroll-then-plan-nav layering; the roster description
  notes it's scrollable.
- [x] 6.2 `context/knowledge/gotchas/hera-view.md`: new section documenting
  the roster table + the clamped-scrollOffset scroll idiom + the
  scroll-then-fall-through key routing + the SetOrch same-orch-preserves
  contract.
- [x] 6.3 OpenSpec: fold the scroll requirement into the SAME "Agents roster
  renders as an aligned, scrollable table" requirement (retitled), both in
  this archived change's delta and the base `hera-view` spec — no new change
  folder, since this landed in the same PR/session before the original change
  was reviewed.
