## 1. Rail: ancestry-only heading detection + first-match auto-select

- [x] 1.1 Add `railRow.ancestryOnly` — true while filtering AND the row's own
  name (or, for an orchestrator header, its folded-in coordinator's name) does
  NOT match the query. Computed in `appendOrch` (via `orchMatchesOwnQuery`),
  `appendWorkerRow`, and `appendPinnedRole`.
- [x] 1.2 `railRow.selectable()` returns `false` when `ancestryOnly` — this is
  the single choke point that makes arrow nav (`step`), `clampCursor`, and
  first-match auto-select all skip ancestry-only headings for free.
- [x] 1.3 `Rail.jumpToFirstMatch()` pins the cursor onto the first selectable
  row that is a real content match (`rrOrch`/`rrRole`/`rrPinnedBreadcrumb`/
  `rrFreelanceRole`, never a structural fold header), writing the cursor
  directly (not via `setCursor`) so it stays non-persisting (BUG-002).
- [x] 1.4 `rebuildAfterFilter` calls `jumpToFirstMatch` when the query is
  non-empty; falls back to the existing ref-based `restoreCursor` once cleared.
- [x] 1.5 Render ancestry-only headings dimmed: `drawOrchRow`, `drawRoleRow`,
  `drawBreadcrumbNameRow` fold `ancestryOnly` into their existing `dim` check.

## 2. Rail + page: one-Enter select-and-clear

- [x] 2.1 `Rail.ClearFilter()` — the shared full-reset (input mode off, query
  reset, rebuild) used by both Esc and (at the bare-Rail level) Enter.
- [x] 2.2 `Rail.handleFilterKey`'s `KeyEnter` case calls `ClearFilter()` (was:
  `filterInput = false` only, leaving the query applied — the old "lock" step).
- [x] 2.3 `HeraPage.handleRailMutation`: while filtering, only `Enter` is
  special-cased — it calls `Rail.ClearFilter()` FIRST (re-pinning the cursor by
  stable identity onto the now-unfiltered tree) and then falls through to the
  existing reattach/focus-advance logic unchanged. Every other key still
  returns `false` (filter input, unchanged).

## 3. Tests

- [x] 3.1 Rewrite `TestRail_FilterEscClearsEnterAccepts` (two tests: Esc-clears,
  Enter-selects-and-clears at the bare-Rail level) and
  `TestPage_FilterArrowNavigateThenEnterSelects` (one Enter, not two) — both
  encoded the OLD two-Enter behavior.
- [x] 3.2 New coverage: first-match auto-select while typing, ancestry-only
  header dimmed + non-selectable + skipped by arrow nav, a coordinator whose
  own/coordinator name matches stays selectable, one-Enter jump-and-clear at
  the `HeraPage` level (reattach fires + filter clears in one keystroke),
  Enter-while-filtering still does not persist rail state (BUG-002).

## 4. Docs

- [x] 4.1 `context/knowledge/gotchas/hera-view.md`: rewrite the `/` filter
  bullet describing the old "Enter commits... a second Enter then acts on the
  row" convention.
- [x] 4.2 `context/knowledge/gotchas/keybindings.md`: update the Hera rail
  keyset bullet's Enter description to note the filter-select-and-clear case.
