## Why

Stable, non-placeholder composer drafts are common on busy Hera coordinators, so preserving them by prepending an instruction to ignore them makes nearly every delivered notice noisy and ambiguous.

## What Changes

- Clear a stable non-notice composer draft only after the existing stability window, submit the notice without an annotation, then restore the captured draft as unsent input after acknowledged submission.
- Keep faint-only Claude Code placeholders on the empty-composer path: they are never captured or restored.
- Skip restoration rather than overwriting any composer state that changed during submission.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: Recover stable composer drafts without exposing them to the receiving agent as notice preamble.

## Impact

- Updates `internal/notify` delivery sequencing and its tests.
- Updates the reliable pane delivery specification and messaging gotcha documentation.
