## Why

Restoring a captured stable draft writes that same content straight back into the composer, which the next delivery to that task then observes as a fresh stable draft and captures, clears, and restores again — a self-perpetuating loop independent of clear confirmation, confirmed live via six restore events on one idle task within 47 minutes.

## What Changes

- Remember, per task, the exact content last written back by a restore.
- When a later delivery observes that same content as a stable draft, clear and submit without capturing or restoring it again.
- Consume that memory one-shot on the next stable-draft observation for the task, matched or not, so a human retyping the same words later is unaffected.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: Break the restore-then-recapture loop with a one-shot, content-matched circuit breaker.

## Impact

- Updates notifier delivery state (`Notifier.lastRestoredDraft`), regression coverage, and the messaging invariant.
