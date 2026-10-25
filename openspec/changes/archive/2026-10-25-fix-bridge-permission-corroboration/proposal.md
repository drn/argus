## Why

Hook-approved Bash calls can run beyond the mod's grace period and appear as pending permissions. Dropping all bridge authority for that uncertain field causes false source transitions and inconsistent idle verdicts.

## What changes

- Retain live bridge busy/idle authority while corroborating only uncertain permission needs-input with the scraper.
- Apply one rule across watcher notifications, REST, daemon-connected TUI and shadow comparison.
- Retain the mod heuristic: the installed 2.1.310 engine rejects user mods on the first-party `anthropic` telemetry stream, confirmed by validation and a live probe before implementation.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `argus-bridge`: resolve uncertain permission input per field without changing the source.
- `idle-detection`: preserve live bridge busy state and derive quiescence from corroborated input.

## Impact

Shared bridge verdict helpers, API watcher and task snapshots, TUI override consumers, and shadow diagnostics. No settings or wire shape changes. All three frontends consume the same daemon rule. Direction approved through /doitright.
