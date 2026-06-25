## 1. Wrap fanned-group members onto multiple rows

- [x] 1.1 Thread the diagram's available inner width through `buildStageBlock`
  into `layoutFannedGroup`.
- [x] 1.2 In `layoutFannedGroup`, pack member boxes left-to-right into rows
  bounded by the available inner width (a member always occupies at least its own
  row); compute the enclosure's multi-row width/height.
- [x] 1.3 Draw each member at its (row, column) position; keep the vertical role
  label, `▲` affordance, and per-member `↘` feed markers.
- [x] 1.4 Compute the selected member's (relX, width) across the wrapped grid so
  the BUG-010 horizontal viewport still ensure-visibles a lone over-wide member.

## 2. Tests

- [x] 2.1 Render test: a fanned group with many members at a narrow width wraps
  onto multiple rows, every member box is fully visible (no overflow / no
  `‹`/`›`), and the cursor reaches every member via `←→`.
- [x] 2.2 Render test: the downstream inter-stage connector still draws, anchored
  below the now-taller wrapped group block.

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/dag-rendering.md`.
