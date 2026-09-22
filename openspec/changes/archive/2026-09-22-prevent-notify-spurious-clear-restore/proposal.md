## Why

After an unconfirmed Ctrl+U clear, a later transient empty composer observation can incorrectly revive the clean restore path and write misclassified static pane content back into a live input.

## What Changes

- Permanently taint a captured stable-draft delivery after its first unconfirmed clear.
- Route every later apparent clear for that delivery through annotated preservation without restoring the captured text.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: Prevent a later apparent clear from restoring a suspect captured draft.

## Impact

- Updates notifier delivery state, regression coverage, and the messaging invariant.
