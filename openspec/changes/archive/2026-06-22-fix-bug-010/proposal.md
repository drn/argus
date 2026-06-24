## Why

**BUG-010 — plan-DAG nodes overflow off the right edge and are unreachable.**
The plan-view widget (`internal/tui/planview`) lays each stage's sibling nodes
left-to-right and paints them with NO horizontal viewport. When a stage (or the
no-plan live-roles row) has more sibling nodes than fit the pane width, the extra
nodes run off the right edge — clipped, invisible, and unreachable. `←→` moves
the cursor "within" a stage, but the view never scrolls to follow, so a selected
node past the right edge is selected-but-unseen.

The widget already scrolls VERTICALLY to keep the cursor's stage block in view
(`scrollOffsetFor`); the horizontal axis had no equivalent.

## What Changes

- **The plan diagram gains a horizontal viewport that follows the cursor.** When
  the widest stage block overflows the pane width, the diagram scrolls
  horizontally so the SELECTED node's box (border included) is fully visible, on
  every cursor change (`←→` within a stage, `↑↓` between stages, group fan-out,
  drill-in, and the refresh re-anchor). When everything fits, no horizontal
  scroll is applied and each stage stays centered (unchanged behaviour).
- **Off-screen content is signalled with edge indicators.** When a stage's
  content is hidden past the left or right pane edge, a dim `‹` / `›` marker is
  drawn at that edge on the stage's row, consistent with the existing single-line
  glyph vocabulary.
- **Painting stays clipped** to the diagram region (the existing `clipRect`), so
  a node never spills past the pane border; the ensure-visible logic keeps the
  selected node whole.

## Capabilities

### Modified Capabilities

- `hera-view`: the plan diagram now scrolls HORIZONTALLY (not just vertically) to
  keep the selected node fully visible, and draws edge indicators when sibling
  nodes are off-screen — previously over-wide stages ran off the right edge,
  unreachable.

## Impact

- **Modified code:**
  - `internal/tui/planview/planview.go` — a horizontal scroll offset on the
    widget; ensure-visible math reusing the dagview-derived box positions;
    left/right edge indicators; clipped, offset-aware stage painting.
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  read-only view/navigation — no edit callbacks added.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
