## MODIFIED Requirements

### Requirement: Mouse wheel scrolls the rail

The rail SHALL respond to mouse wheel scroll events within its rect by moving the cursor: scroll-up moves the cursor to the NEXT selectable row, scroll-down moves it to the PREVIOUS selectable row, including kanban-group boundary crossing and cursor persistence, identically to keyboard navigation. This mapping is deliberately inverted relative to a plain content-pane scroll — the cursor is what the gesture drags, so it moves in the same direction as the operator's fingers (trackpad "natural" scrolling) rather than the direction a viewport would pan. A left-click within the rail's rect SHALL focus it. Mouse events outside the rail's rect SHALL NOT be consumed.

#### Scenario: Scroll up moves the cursor to the next selectable row

- **WHEN** the operator scrolls the mouse wheel up while the pointer is over the rail
- **THEN** the cursor moves to the next selectable row, the same target `CursorDown` would select

#### Scenario: Scroll down moves the cursor to the previous selectable row

- **WHEN** the operator scrolls the mouse wheel down while the pointer is over the rail
- **THEN** the cursor moves to the previous selectable row, the same target `CursorUp` would select

#### Scenario: A click focuses the rail

- **WHEN** the operator left-clicks within the rail's rect
- **THEN** the rail receives focus

#### Scenario: Mouse events outside the rect are ignored

- **WHEN** a mouse event's position falls outside the rail's current rect
- **THEN** the event is not consumed and the cursor does not move
