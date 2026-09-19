## Why

Verified Enter retries repair the final submit race, but they do not decide safely what to do with content already sitting in the recipient's composer. A live incident left multiple Hera notices concatenated in an unsubmitted input line; reliable delivery must distinguish active human typing from stale or previously injected content so it can recover instead of either overwriting a person or waiting forever.

## What changes

- Snapshot the rendered recipient composer before delivery and use its content—not session idleness or pane focus—as the primary submit-safety signal.
- Submit immediately when the composer is empty or contains only stale Argus/Hera notices.
- Defer while non-notice content changes between snapshots, because change is evidence of active typing.
- Treat unchanged non-notice content as abandoned after a stability window, preserve it, and append an explicit do-not-act annotation before the new notice.
- Retain the existing idle/focus gates only as a conservative fallback when no composer can be identified from the rendered terminal.
- Add daemon-visible logs and messaging gotchas for every content decision.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `reliable-pane-delivery`: Make rendered composer content the primary delivery gate and define stale-input recovery.

## Impact

- `internal/agent`: expose a bounded rendered-composer snapshot from the existing VT screen renderer.
- `internal/notify`: maintain per-delivery composer observations and construct annotated recovery input.
- `context/knowledge/gotchas/messaging.md`: document the content-aware delivery invariant.
