## ADDED Requirements

### Requirement: Scroll expanded board content

The TUI coordinator board SHALL scroll its rendered headers and bodies by three lines per mouse-wheel notch over its visible rectangle, even when another pane has keyboard focus. The offset SHALL clamp between zero and the final content viewport, reserving borders and the hint row. Wheel scrolling SHALL preserve keyboard focus, selection and expansion, and SHALL NOT affect the board when the pointer is over the roster or another pane. PgUp/PgDn SHALL continue to scroll a focused board. Selection navigation and Enter toggling SHALL bring the selected header into view.

#### Scenario: Read a tall expanded item

- **WHEN** Aaron expands a body taller than the board viewport and wheels down over the board
- **THEN** previously hidden body lines become visible, including the final line at the bottom clamp
- **AND** wheeling up returns to the first header without overscrolling

#### Scenario: Wheel without board focus

- **WHEN** the coordinator terminal or rail has keyboard focus and Aaron wheels over the board
- **THEN** board content scrolls and keyboard focus remains on its existing pane

#### Scenario: Pointer outside board

- **WHEN** Aaron wheels over the roster above the board
- **THEN** the board offset remains unchanged

#### Scenario: Keyboard and selection after scrolling

- **WHEN** Aaron uses PgUp/PgDn in the focused board and then navigates to another item or toggles expansion
- **THEN** page scrolling reads the body and subsequent selection or expansion reveals the selected header
