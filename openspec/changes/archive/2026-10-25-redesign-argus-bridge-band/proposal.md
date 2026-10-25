## Why

The first band repeated what the status line and the TUI rail already show (context percent, role name, kind, orchestrator), hid who had written, gave a coordinator no view of its workers, and vanished whenever the daemon blipped. It also left a blank row between itself and the prompt box.

## What changes

- Drop context percent, role name, role kind and orchestrator from the band.
- Name the newest unread sender, and roll up a coordinator's working and blocked workers from a new snapshot `workers` field.
- Style every state with theme keys; the `argus:` label alone keeps the statusline's raw blue.
- Keep the last snapshot on screen flagged `⚠ bridge offline` when the daemon is unreachable.
- Omit the engine's empty beneath component so no blank row appears above the prompt box.
- `/hera-status` shows the band line and says when its values are the last snapshot.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `argus-bridge`: band content, styling, offline behavior and `/hera-status` output.
- `rest-api`: coordinator `workers` roll-up on the bridge UI snapshot.

## Impact

Embedded Claude mod (`hooks/ui.tsx`, tests, types), `internal/api/bridge_ui.go`, the supervisor spawn surface digest, the bridge gotchas and the README Reference. The snapshot's `workers` roll-up exists only for the mod's band: the TUI rail, web Hera tab and macOS Hera tab already render worker statuses from the Hera roster, so none of them reads it (deliberate non-goal, not a parity gap).
