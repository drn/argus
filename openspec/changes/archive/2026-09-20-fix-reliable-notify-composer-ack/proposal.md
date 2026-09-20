## Why

Reliable pane delivery can falsely record an Enter as accepted when a busy recipient produces unrelated PTY output. The next queued notice can then be appended to the unsubmitted stale draft, creating concatenated, undelivered coordinator messages.

## What Changes

- Confirm submission from the rendered composer state after each standalone CR, rather than from output-byte advancement alone.
- Confirm that a stale-notice Ctrl+U operation actually cleared the composer before directly injecting the next notice.
- Preserve unverified stale drafts through the existing annotated-append path instead of concatenating messages.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: Require composer-state confirmation for submission and verified stale-draft clearing.

## Impact

`internal/notify/service.go`, notifier regression tests, and messaging delivery invariants.
