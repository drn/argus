## Why

Claude Code renders an example prompt in dim text when its composer is empty, but the terminal renderer previously discarded style information and classified that placeholder as stable user input. Reliable delivery then emitted a misleading abandoned-input annotation even though there was nothing to preserve.

## What Changes

- Treat an identifiable composer containing only dim-styled placeholder text as empty.
- Preserve real non-dim composer input as user content.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: Define dim Claude Code placeholder text as empty composer state.

## Impact

`internal/agent/needsinput.go`, its renderer tests, and reliable pane-delivery safety decisions.
