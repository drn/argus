## ADDED Requirements

### Requirement: Left arrow moves rail cursor to parent coordinator when rail is focused

The Hera view SHALL intercept `KeyLeft` in `HeraPage.InputHandler` ONLY when `focus.State() == FocusRail`. When intercepted, the handler SHALL call `rail.CursorToParent()` and return, consuming the event.

When a content pane is focused (`FocusCoord` or `FocusAgent`), `KeyLeft` SHALL NOT be intercepted — it SHALL pass through to `forwardKey` and reach the pane's PTY unchanged.

#### `Rail.CursorToParent()` algorithm

Starting from the current cursor row, walk backwards through the flattened row list. Stop at the first row that satisfies ALL of:

1. `row.depth < currentRow.depth` (strictly smaller depth)
2. `row.kind == rrOrch` OR (`row.kind == rrRole` AND `row.collOrchID > 0`)

Call `setCursor(i)` on the matching row. If no such row exists (cursor is at root, or no qualifying ancestor), the method is a no-op.

The `FocusMachine` state SHALL remain unchanged in all cases.

The binding SHALL appear in the Hera rail section of the help overlay (`?`) and in the README Reference keybinding table.

#### Scenario: Left from worker moves cursor to parent orchestrator header

- **GIVEN** the Hera rail is focused and the cursor is on a worker row (depth > 0)
- **WHEN** the user presses `←`
- **THEN** the cursor moves to the nearest ancestor `rrOrch` or bridging `rrRole` row with smaller depth
- **THEN** the `FocusMachine` state remains `FocusRail`

#### Scenario: Left from root row is a no-op

- **GIVEN** the Hera rail is focused and the cursor is on a row with depth 0 and no qualifying ancestor above it
- **WHEN** the user presses `←`
- **THEN** the cursor does not move
- **THEN** the `FocusMachine` state remains `FocusRail`

#### Scenario: Left from pane-focused state passes through to PTY

- **GIVEN** the Hera tab is active and a content pane (coordinator or agent) is focused
- **WHEN** the user presses `←`
- **THEN** the rail cursor does NOT move
- **THEN** the `FocusMachine` state does NOT change
- **THEN** the key is forwarded to the pane's PTY via `forwardKey`
