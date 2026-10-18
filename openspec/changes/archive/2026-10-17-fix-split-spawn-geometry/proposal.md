## Why

Fresh Hera tasks can inherit a full-width Tasks or fullscreen pane report and then render wide-authored history in a narrow split pane. Tab changes and another viewer's heartbeat can recreate this corruption.

## What changes

- Always publish conservative split geometry for Hera roles from the screen dimensions, independent of tab and fullscreen state.
- Reject Active-only geometry for Hera roles and retain the existing default when their split size is absent.
- Clear stale repair indicators on redundant attach and improve repair-specific logs.
- Close the reported mutation-test gaps and describe every omitted-size task creation path.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `terminal-rendering`: split launch sizing, all omitted-size callers, and repair-state cleanup.

## Impact

Local TUI layout publication, local DB preferences, stop diagnostics, regression tests and gotchas. No REST contract changes.
