## 1. Render a divider between the Pinned and Active sections

- [x] 1.1 `buildRows` records whether the Pinned section rendered, and the row
  index where the Active section begins; after appending the Active rows it
  inserts a single `rrRule` row at that index when the Pinned section rendered AND
  the Active section produced ≥1 row (BUG-027). Mirrors the existing
  Freelance / Archive `rrRule` mechanism — same rune (`─`) and style
  (`theme.StyleBorder`) at draw time, no new row kind.

## 2. Tests

- [x] 2.1 A model with a pinned orchestrator AND an active orchestrator renders
  exactly one `rrRule` row positioned between the last Pinned row and the first
  Active row.
- [x] 2.2 A model with no pins (only active orchestrators) renders NO Pinned→Active
  divider.
- [x] 2.3 A model with only a pinned orchestrator and no active entries renders NO
  trailing divider.
- [x] 2.4 The divider is non-selectable: `j`/`k` cursor nav steps from the last
  Pinned selectable row to the first Active selectable row, never landing on the
  rule.

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/hera-view.md` (Pinned→Active
  divider rendered only when both sections present; reuses `rrRule`).
