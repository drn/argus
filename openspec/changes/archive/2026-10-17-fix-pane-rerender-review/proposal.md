## Why

Measured delays come from fresh coordinator sessions spawned at 80 columns and then bound to approximately 98-column panes. Review also found that the attempted persisted-width fix adds no decision value and can erase unresolved anchors.

## What changes

- Start fresh headless/Hera tasks at recent TUI-reported pane dimensions, preserving explicit dimensions and the no-report default.
- Remove persisted per-task width state entirely and preserve unresolved in-memory anchors until a successful new start.
- Emit stop diagnostics through daemon/supervisor logging and distinguish pending repair from active re-rendering.
- Keep session switches and Hera revive on ordinary non-escalating stops; explicitly opt width repairs into bounded stops.
- Strengthen the regressions identified by review and document the corrected latency evidence.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `terminal-rendering`: repair-state preservation, recent launch geometry, background publication, and repair-specific stop scope.

## Impact

TUI layout publication, local DB layout metadata, fresh task creation, terminal status, session stop/RPC routing, tests, and operational logs. Web/macOS automatically benefit from shared spawn sizing; publishing their own viewport preferences and showing repair status remain the named follow-up. No REST contract changes.
