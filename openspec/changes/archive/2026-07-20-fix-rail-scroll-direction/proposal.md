## Why

The just-shipped mouse-wheel scroll for the Tasks list and Hera rail (`add-rail-mouse-scroll`) mapped `MouseScrollUp`→`CursorUp`/`MouseScrollDown`→`CursorDown`, mirroring `gitpanel.FilePanel`'s pane-scroll convention. Live dogfood testing on a trackpad showed this reads backwards: both widgets move the CURSOR itself (not an independent viewport), so the intuitive mapping is the cursor moving in the same direction as the fingers, not the direction a content pane would move — the opposite of `FilePanel`'s convention. `add-rail-mouse-scroll` is already archived (base specs merged), so this is a follow-up correction rather than an amendment to that closed change.

## What Changes

- Swap the wheel-to-cursor mapping in both `taskview.TaskListView.MouseHandler` and `hera.Rail.MouseHandler`: `MouseScrollUp` now calls `CursorDown`, `MouseScrollDown` now calls `CursorUp`.
- No other behavior changes — click-to-focus and the out-of-rect no-op are unaffected.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `task-list-view`: corrects the "Mouse wheel scrolls the task list" requirement's direction mapping.
- `hera-view`: corrects the "Mouse wheel scrolls the rail" requirement's direction mapping.

## Impact

- `internal/tui/taskview/tasklist.go`, `internal/tui/hera/rail.go`: swap the two `case` bodies in each `MouseHandler`.
- `internal/tui/taskview/tasklist_test.go`, `internal/tui/hera/rail_test.go`: updated scroll-direction assertions.
- `context/knowledge/gotchas/tasklist-ui.md`, `context/knowledge/gotchas/hera-view.md`: gotcha bullets documenting the inverted (cursor-drag, not pane-drag) convention and why it deliberately diverges from `FilePanel`.
