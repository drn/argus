## Why

An expanded coordinator board item can exceed the TUI details pane height, leaving its body unreadable with the mouse wheel. Aaron requested wheel scrolling over that board and approved this small behavioral change directly.

## What changes

- Route wheel events over the visible board to its line offset, three lines per notch, clamped to the rendered content.
- Preserve focus, item selection, Enter expansion and existing PgUp/PgDn keyboard scrolling.
- Test real mouse delivery through the SimulationScreen event loop and boundary math.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `coordinator-board`: Scroll a tall expanded body by wheel without moving keyboard focus.

## Impact

TUI board layout and Hera page mouse routing only; no REST or persistence changes.

## Non-goals

- The separate `i` InboxModal is unchanged.
- Web and macOS board presentation remains the existing named follow-ups `web-coordinator-board-read-only` and `macos-coordinator-board-read-only`; neither client currently renders coordinator board items, so neither shares this board overflow bug.
