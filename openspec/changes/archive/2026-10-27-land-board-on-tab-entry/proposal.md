## Why

The rail cursor survives a tab switch, so returning to the Hera tab (e.g. Cmd+→ from the agent view) onto the coordinator already selected kept the panel chosen on the earlier visit. A coordinator that received board mail while the tab was away showed its plan until the cursor left and came back.

## What Changes

- Treat Hera-tab entry as coordinator selection time for board landing: unread items open the board.
- Entry only opens the board; it keeps the board cursor and expanded item and never forces the plan.
- Ordinary same-selection refreshes keep preserving the chosen lower panel.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `hera-view`: Board landing on Hera-tab entry.

## Impact

TUI Hera tab entry and regression tests. No REST changes; web and macOS have no board auto-landing on tab entry.
