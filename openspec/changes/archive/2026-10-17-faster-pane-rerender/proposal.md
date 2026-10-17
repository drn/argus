## Why

Reopening the TUI on Projects can leave an agent pane half drawn for roughly twenty seconds while a repeated width repair waits and restarts Claude. Preserve width reconciliation across viewer launches and make necessary repairs visible and bounded.

## What changes

- Persist local TUI width reconciliation and unresolved anchors without clobbering outstanding drift.
- Escalate transcript-guarded rerender stops after a three-second transcript flush grace period.
- Show a small in-pane re-rendering indicator during deferred repair and restart.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `terminal-rendering`: durable width repair state, bounded agent-only stop, and repair status.

## Impact

Local task metadata, TUI kick decisions, terminal pane painting, and session stop escalation. Builds on #1053's transcript availability guard.
