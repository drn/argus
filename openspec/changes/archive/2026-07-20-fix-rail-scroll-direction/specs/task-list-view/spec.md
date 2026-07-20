## MODIFIED Requirements

### Requirement: Mouse wheel scrolls the task list

The task list SHALL respond to mouse wheel scroll events within its rect by moving the cursor: scroll-up moves the cursor DOWN one row, scroll-down moves it UP one row, skipping project/section headers and separators identically to keyboard navigation. This mapping is deliberately inverted relative to a plain content-pane scroll — the cursor is what the gesture drags, so it moves in the same direction as the operator's fingers (trackpad "natural" scrolling) rather than the direction a viewport would pan. A left-click within the task list's rect SHALL focus it. Mouse events outside the task list's rect SHALL NOT be consumed.

#### Scenario: Scroll up moves the cursor to the next task

- **WHEN** the operator scrolls the mouse wheel up while the pointer is over the task list
- **THEN** the cursor moves to the next task row, the same target `CursorDown` would select

#### Scenario: Scroll down moves the cursor to the previous task

- **WHEN** the operator scrolls the mouse wheel down while the pointer is over the task list
- **THEN** the cursor moves to the previous task row, the same target `CursorUp` would select

#### Scenario: A click focuses the task list

- **WHEN** the operator left-clicks within the task list's rect
- **THEN** the task list receives focus

#### Scenario: Mouse events outside the rect are ignored

- **WHEN** a mouse event's position falls outside the task list's current rect
- **THEN** the event is not consumed and the cursor does not move
