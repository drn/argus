## Why

Ctrl+G shows an unread coordinator Board but leaves keyboard focus in the coordinator terminal. The operator must move focus before navigating the items that triggered the jump.

## What Changes

- Focus the Board after its exact-row jump and select the first unread item in displayed order.
- Follow the cursor into view without expanding or acknowledging items.
- Preserve needs-input-only landing, reattach semantics and ordinary refresh behavior.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `hera-view`: Board attention jump focus and unread cursor placement.

## Impact

TUI jump handling, regression tests and Reference documentation. No REST changes; web and macOS do not expose this TUI keyboard focus interaction.
