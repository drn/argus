## Why

Reliable notify appended its "unsubmitted input" annotation to Hera messages when the operator had typed nothing. Claude Code's ghost suggestion (for example `push it`) is drawn faint from its first cell with the terminal's own cursor parked on it, but a window-title update containing `✳` (E2 9C B3) leaked its tail onto the composer row. x/ansi ends an OSC at the 0x9C byte, so the remainder printed as non-faint text and the faint-only check read the composer as a typed draft. The recipient then asked the operator what they had meant to say.

## What Changes

- Strip OSC sequences before the composer-detection emulator renders the output tail, via a shared `internal/oscfilter` package that the terminal pane now also uses.
- Reword the fixed fallback annotation so it is conditional: if the composer held unsent text, do not act on it and mention it afterward; if it was empty, ignore the line. A residual false positive then degrades to a no-op.

## Capabilities

### New Capabilities

- None.

### Modified Capabilities

- `reliable-pane-delivery`: The fallback annotation wording is conditional, and a ghost suggestion is treated as an empty composer even when a window-title update accompanies it.

## Impact

- `internal/agent` composer rendering, `internal/oscfilter` (moved from `internal/tui/terminal`), `internal/notify` annotation constant.
- Daemon-side only; no supervisor surface or protocol change.
