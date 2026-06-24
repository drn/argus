## 1. Horizontal viewport

- [x] 1.1 Add a horizontal scroll offset to the plan-view widget; reset it on `SetData`.
- [x] 1.2 Expose the cursor's selected-box geometry (relative x + width) from the stage layout, including a fanned-group member.
- [x] 1.3 Compute an ensure-visible horizontal offset so the selected box (border included) is fully within the diagram region; scroll only when the widest stage overflows the pane.
- [x] 1.4 Apply the offset to every stage block's paint origin (left-aligned in scroll mode), keeping painting clipped to the region.
- [x] 1.5 Draw dim `‹` / `›` edge indicators on a stage's row when its content is hidden past the left / right pane edge.

## 2. Tests

- [x] 2.1 Selected node is fully painted for various cursor positions (first, middle, last) in a wide stage.
- [x] 2.2 Edge indicators appear when content is off-screen and are absent when everything fits.
- [x] 2.3 Scrolling tracks `←→` cursor movement (left node visible after scrolling back).

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `dag-rendering.md` (horizontal viewport ensure-visibles the cursor; edge indicators; clipped paint; reuses dagview x-positions).
