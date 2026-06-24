## Why

Text injected into a session's PTY via the native task bus or the hera plugin goes
unsubmitted when the recipient isn't idle at the moment of injection. Two independent
PTY writers (the native bus and the hera plugin) can race and double-submit. The
result is a live-reproduced bug: an idle worker sits with unsubmitted pointer text in
its input forever. This change gives argus an owned, reliable inject-and-submit
service so every automated delivery is guaranteed exactly once.

## What Changes

- Add a daemon-owned **reliable notify service** (`internal/notify/`): `ReliableNotify(taskID, text, deliveryID, opts)` injects text into the task PTY and submits it with a single CR exactly once, as soon as the session is idle AND no human is focused on that pane. Pending deliveries retry on the idle-watcher 5-second tick. Dedup by deliveryID prevents double-inject. A deadline backstop and caller-cancel terminate retries. A per-task serialization lock ensures only one CR is ever auto-submitted at a time.
- Add a **daemon-level focus tracker**: a lightweight `FocusTracker` keyed by task ID, updated by the TUI when it enters/leaves agent view and by plugin-view input forwarders. The reconciler checks `IsFocused(taskID)` before any auto-submit so no CR lands in a pane a human is currently typing in.
- Add **REST endpoints** for plugin callers: `POST /api/tasks/{id}/notify` (inject+submit) and `DELETE /api/tasks/{id}/notify/{delivery_id}` (cancel). The hera plugin replaces its idle-gated doorbell loop with a single `POST /notify` call; it cancels via `DELETE` when the recipient acks the hera message.
- **Migrate the native task bus**: `daemon/nudge.go` + `mcp/messaging.go` replace the bare `"\n"` fire-and-forget with `ReliableNotify`, using the message ID as delivery_id. `task_message_ack` cancels the delivery on read. One service, one writer – eliminates the collision.

## Capabilities

### New Capabilities

- `reliable-pane-delivery`: The reliable inject-and-submit service, reconciler, single-writer serialization, dedup, deadline, cancel, and idle+focus gate.
- `daemon-focus-tracking`: A daemon-level registry of which task pane a human is currently focused on, aggregating TUI and plugin-view focus signals.

### Modified Capabilities

- `rest-api`: Add the `/notify` inject endpoint and the `/notify/{delivery_id}` cancel endpoint, both plugin-scoped-token callable.
- `task-messaging`: Migrate send from bare nudge to `ReliableNotify`; cancel delivery on ack.

## Impact

- **New code:** `internal/notify/` (service, focus tracker, types), wire-up in `internal/daemon/daemon.go`, `internal/api/server.go`, `internal/tui/app.go`.
- **Modified code:** `internal/daemon/nudge.go` (Nudge calls ReliableNotify), `internal/mcp/messaging.go` (send calls Notify; ack calls cancel), `internal/api/routes.go` + `internal/api/handlers.go` (two new routes), `internal/api/push.go` (idleWatcher tick calls reconciler).
- **Dependencies:** none new.
- **hera contract:** hera replaces its doorbell loop with `POST /api/tasks/{id}/notify` + `DELETE /api/tasks/{id}/notify/{delivery_id}` (see design.md § hera contract).
