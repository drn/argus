## Why

PTY delivery requires composer scraping and draft manipulation, while blocking budget nudges can prevent coordinators reaching idle. The live-tested bridge can submit a plugin-framed turn without touching a typed draft.

## What Changes

- Route shared-notifier Hera doorbells and task notices through live Claude bridges with idle/dialog gates, claims, acknowledgment, cancellation and bounded fallback.
- Replace the blocking budget nudge with an idle mod-submitted delivery when bridge delivery is active; preserve cadence, accounting and forced recycle.
- Add default-on `bridge.delivery` with runtime configuration overlay and an extensible Bridge section in TUI Settings.
- Keep legacy delivery and budget nudges when either bridge setting is off or authority is unavailable.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `argus-bridge`: authenticated message pull/ack and mod submission.
- `reliable-pane-delivery`: shared queue routing, claim lifecycle and fallback.
- `hera-messaging`: bridge doorbells preserve inbox acknowledgment.
- `task-messaging`: bridge notices preserve explicit acknowledgment.
- `coordinator-context-management`: non-blocking prompt budget nudge.
- `config-management`: delivery setting and configuration precedence.
- `settings-view`: Bridge toggles and persistence.
- `rest-api`: authenticated bridge delivery and budget enqueue endpoints.

## Impact

Touches notify, REST/RPC wiring, coordinator Stop hook, embedded mod, config overlays, TUI Settings, builtin Hera instructions, tool descriptions and README Reference. No supervisor restart or live-daemon mutation is authorized by this phase.
