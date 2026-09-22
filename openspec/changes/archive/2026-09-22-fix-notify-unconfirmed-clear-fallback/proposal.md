## Why

An identifiable but non-editable terminal frame can be mistaken for a stable human draft. When Ctrl+U cannot clear that frame, reliable-notify currently retries until its five-minute deadline and then drops the pane delivery without ever submitting it.

## What Changes

- Bound failed clear-confirmation retries for captured stable drafts per delivery.
- After the bounded retry count, preserve the captured content with the existing abandoned-draft annotation and submit the notice instead of waiting for deadline abandonment.
- Preserve clean capture, clear, submit, and restore delivery when Ctrl+U confirms promptly.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: Guarantee a bounded annotated fallback when stable-draft clear confirmation never arrives.

## Impact

- Updates `internal/notify/service.go`, notifier tests, and the reliable delivery messaging gotcha.
