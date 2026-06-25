## Why

**BUG-011 — a fanned plan-DAG group overflows the pane in one horizontal row.**
When a parallel group is fanned out (Enter on a collapsed group), the plan-view
widget (`internal/tui/planview`) lays every member box in a SINGLE horizontal
row inside the enclosure. With many members the row runs off the right edge —
e.g. `2a 2b 2c 2d land-44-iso… land-46-mem… …` — leaving only a `›` indicator
to hint at the hidden members. The view is unusable for a wide group: the
operator must horizontally scroll one member at a time to see them all.

The horizontal viewport (BUG-010) keeps the SELECTED member visible by scrolling,
but a single overflowing row is still the wrong shape for a group of more than a
handful of members.

## What Changes

- **A fanned group's member boxes wrap onto MULTIPLE ROWS to fit the pane
  width.** The enclosure packs member boxes left-to-right, starting a new row
  whenever the next box would exceed the available inner width, so the whole
  group fits the diagram width instead of overflowing in one row. A member box
  wider than the pane on its own still occupies a row of its own (and the BUG-010
  horizontal viewport still scrolls to it).
- **`←→` navigation walks the members in order across the wrapped grid.** The
  member cursor index is unchanged; stepping right off the end of one row lands
  on the first member of the next row (and left off the start lands on the prior
  row's last member), so every member is reachable.
- **The enclosure grows TALLER for the wrapped rows, and the downstream
  inter-stage edge re-anchors below it.** The connector that hangs under the
  group's center is placed using the block's (now taller) height, so it stays
  anchored beneath the wrapped block; each feeding member keeps its `↘`.
- **BUG-010's horizontal viewport + ensure-visible is preserved.** Wrapping
  removes the overflow in the common case (no scroll, no `‹`/`›` indicators), but
  the X-viewport still handles a lone over-wide member box.

## Capabilities

### Modified Capabilities

- `hera-view`: a fanned group's member boxes now WRAP onto multiple rows to fit
  the pane width — previously they were laid out in a single horizontal row that
  overflowed the right edge for a group with many members.

## Impact

- **Modified code:**
  - `internal/tui/planview/planview.go` — `layoutFannedGroup` packs members into
    rows bounded by the available width (threaded through `buildStageBlock`);
    multi-row enclosure height; member draw + selected-member geometry across
    rows.
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  read-only view/navigation — no edit callbacks added.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
