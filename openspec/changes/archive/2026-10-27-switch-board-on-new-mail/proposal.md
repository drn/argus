## Why

A coordinator that receives board mail while the operator is sitting on it keeps showing its plan; the mailbox marker changes but the details region does not, so new mail on the coordinator in view is easy to miss.

## What Changes

- On refresh, when the selected coordinator gains an unread board item that was not unread in the previous snapshot (a new item or a re-unread revision), select the board.
- Keyboard focus, the board cursor and the expanded item are unchanged; only the lower panel switches.
- Refreshes with no new unread item keep preserving the chosen lower panel, including an explicit toggle to the plan.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `hera-view`: Board selection on new mail for the selected coordinator.

## Impact

TUI Hera details region and regression tests. No REST changes; web and macOS have no board auto-landing.
