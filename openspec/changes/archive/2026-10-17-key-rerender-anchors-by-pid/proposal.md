## Why

An external REST resume or Hera revive can replace the process without passing through TUI startSession. An unresolved width anchor from the old process then causes an unnecessary repair of the new process.

## What changes

- Scope unresolved width anchors and attach decisions to the live session PID.
- Discard old-process anchors, cached attach decisions and pending repair status when a replacement process binds.
- Ignore async decisions superseded by another process binding, while preserving same-process anchor protection.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `terminal-rendering`: process-scoped unresolved anchors and external restart handling.

## Impact

Local and remote TUI bind decisions, state cleanup, regression tests and gotchas. No daemon or REST contract changes.
