## Why

Reliable pane delivery currently assumes that a standalone carriage return sent 50 ms after message text is accepted as Enter. Under a slow recipient, the CLI can still classify that carriage return as part of the paste batch, leaving durable Hera mail visibly drafted but never submitted while Argus incorrectly records success and emits no daemon-side diagnostic.

## What changes

- Replace the fixed-delay, fire-and-forget submit with an output-observed protocol that waits for the injected text to be consumed, verifies post-Enter PTY activity, and retries Enter with bounded backoff when no acknowledgment appears.
- Keep an unacknowledged delivery pending rather than recording it as submitted, so a later reconcile cycle can retry until its deadline.
- Emit delivery lifecycle, retry, success, and terminal-failure diagnostics through daemon-visible structured logging in addition to the existing TUI UX log.
- Document the resulting reliable-notify timing, evidence, and logging invariants.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `reliable-pane-delivery`: Require observable submit acknowledgment, bounded Enter retries, pending-state preservation when acknowledgment is absent, and daemon-visible delivery diagnostics.

## Impact

- `internal/notify`: submission protocol, narrow session interface, test fakes, and structured diagnostics.
- `internal/agent` and `internal/daemon/client`: existing monotonic PTY output counters are consumed through the notifier interface; no wire or persistence changes.
- `context/knowledge/gotchas/messaging.md`: reliable-notify operational invariants.
