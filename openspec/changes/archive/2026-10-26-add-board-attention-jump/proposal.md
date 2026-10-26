## Why

Unread coordinator boards represent human attention, but Ctrl+G currently skips them unless an agent also needs input. Aaron approved including these coordinators in the existing attention cycle.

## What changes

- Include coordinators with unread boards in Ctrl+G and the Ctrl+J popup's next-attention scan when `ui.coordinator_board` is enabled.
- Preserve rail order, one stop per coordinator, ancestor reveal, board auto-landing and identity-based excursion restoration.
- Update keymap help and the README Reference description.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `hera-view`: Extend attention jumps and excursion membership to unread coordinator boards.

## Impact

TUI rail, jump switcher and global jump dispatch only. No REST contract or daemon behavior changes. Web and macOS have no equivalent global TUI attention cycle; their board behavior remains covered by coordinator-board. This is a separate PR from #1075, with no merge or deployment.
