## Why

Claude sessions already report bridge state and receive messages. Operators need to see their Hera role, unread mail and budget in that session, and inspect it without spending a model turn.

## What changes

- Add a composable cached band above the prompt, arrival toasts and local `/hera-inbox` and `/hera-status` commands.
- Add live `bridge.band` and `bridge.toasts` switches, default on, through TUI and remote settings.
- Expose an authenticated read-only task bridge UI snapshot without acknowledging mail.
- Band redesign (follow-up after merge): drop context, name the newest unread sender, roll up a coordinator's working and blocked workers (new snapshot `workers` field), style with theme keys, keep the last snapshot flagged offline when the daemon is unreachable, and drop the blank row above the prompt box.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `argus-bridge`: session UI, cached refresh and local commands.
- `config-management`: live UI flags with standard overlay.
- `settings-view`: Bridge band and toast toggles.
- `rest-api`: read-only UI snapshot and settings round-trip.

## Impact

Embedded Claude mod, daemon REST, config, TUI Settings, API client/store, tests and Reference documentation. Web and macOS settings controls remain a named follow-up. The snapshot's `workers` roll-up exists only for the mod's band: the TUI rail, web Hera tab and macOS Hera tab already render worker statuses from the Hera roster, so none of them reads it (deliberate non-goal, not a parity gap).
