## Why

When reliable notification cannot confirm that Ctrl+U cleared a composer, its fallback payload is written at the cursor. The current directional annotation can identify the injected notice as abandoned user text and the user's draft as the instruction, reversing its safety guidance.

## What Changes

- Put the bracketed Argus/Hera notice before the fallback annotation.
- Replace directional annotation text with directionless wording that identifies unsubmitted user input, excludes it from action, directs the agent to handle only the bracketed notice, and asks the user to continue afterward.
- Recognize the reordered annotated payload as stale notifier content on retry.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: Preserve unconfirmed drafts with an order-safe annotated fallback and recognize the reordered payload on retry.

## Impact

- `internal/notify` payload construction and stale-payload classification.
- Reliable-notify tests and messaging gotchas.
