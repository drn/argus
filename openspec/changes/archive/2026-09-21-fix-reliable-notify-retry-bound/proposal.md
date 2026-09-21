## Why

Reliable pane delivery can repeatedly resubmit one Hera doorbell when a visual terminal wrap prevents the rendered composer from matching the single-line injected text. A missing fallback branch then leaves the delivery pending and can cause repeated real agent turns until a daemon restart.

## What Changes

- Match injected composer text after normalizing visual-wrap whitespace, so wrapped notices remain verifiable.
- Use the existing output-advance fallback when a known composer cannot confirm the injected draft.
- Bound total unconfirmed Enter attempts across reconcile ticks and abandon the delivery with an error-level diagnostic when the ceiling is reached.
- Recognize the notifier's own annotated-preservation payload as a stale notice so retries cannot repeatedly append the annotation and grow the draft.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: Verify wrapped composer drafts, safely fall back when verification is unavailable, and bound failed delivery retries.

## Impact

`internal/notify/service.go`, its regression tests, the reliable pane-delivery specification, and messaging delivery invariants.
