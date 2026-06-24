# Hera View

## MODIFIED Requirements

### Requirement: Plan diagram has a footer hint and scrolls to the cursor (area 6)

The plan diagram SHALL render a dim footer hint bar on its bottom row (`↑↓ stage · ←→ within · Enter fan · Esc back`). Because boxes are taller than single-line chips, when the stacked block exceeds the diagram region the view SHALL scroll vertically so the cursor's stage box is fully visible; when the block fits, no scroll is applied.

The diagram SHALL also maintain a HORIZONTAL viewport that follows the cursor. When the widest stage block overflows the diagram width, the view SHALL scroll horizontally so the SELECTED node's box (its border included) is fully within the visible x-range — on every cursor change (`←→` within a stage, `↑↓` between stages, group fan-out, sub-coordinator drill-in, and the refresh re-anchor). When every stage fits the width, no horizontal scroll is applied and each stage stays centered. The selected node's box positions are reused from the dagview-derived stage layout (no relayout-to-fit, no wrapping, no shrinking). When a stage's sibling content is hidden past the left or right pane edge, a dim `‹` / `›` edge indicator SHALL be drawn at that edge on the stage's row. All painting SHALL be clipped to the diagram region (no `screen.Sync`); the ensure-visible logic keeps the selected node whole.

#### Scenario: Footer hint renders

- **WHEN** the plan diagram is drawn
- **THEN** a dim nav-legend footer row is present

#### Scenario: The diagram scrolls to keep the selected node visible

- **WHEN** the plan has more stages than fit the region and the cursor is on the last stage
- **THEN** the last stage's box is rendered within the region and the first stage has scrolled out of view

#### Scenario: A wide stage scrolls horizontally to the selected node

- **WHEN** a stage has more sibling nodes than fit the pane width and the cursor is moved with `←→` onto a node past the right edge
- **THEN** the selected node's box is rendered fully within the diagram region (scrolled into view), and a node off the opposite edge is not painted

#### Scenario: Edge indicators reflect off-screen content

- **WHEN** a stage's sibling nodes extend past the right pane edge
- **THEN** a dim `›` indicator is drawn at the right edge of that stage's row; a `‹` indicator is drawn at the left edge once the view has scrolled right; and when every stage fits the width no edge indicator is drawn

#### Scenario: Scrolling back reveals the left node

- **WHEN** the cursor is scrolled to a right-edge node in a wide stage and then moved back left with `←` to the first node
- **THEN** the first node's box is rendered fully within the diagram region
