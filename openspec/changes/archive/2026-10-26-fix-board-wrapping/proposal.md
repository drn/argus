## Why

The coordinator Board cuts subjects off, splits ordinary words, and exposes bold markdown markers. Expanded items need readable wrapping without losing selection, unread state or scrolling.

## What Changes

- Share the inbox's word wrapper with the Board, preserving inbox sanitization.
- Wrap expanded subjects with a hanging indent; ellipsize collapsed subjects while retaining unread markers.
- Render bold spans and hanging bullet indentation in bodies.
- Preserve cursor visibility with multi-line headers.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `coordinator-board`: Human board interaction rendering and cursor visibility.

## Impact

TUI Board, inbox and shared widget helper only. No wire-contract or dependency changes.
