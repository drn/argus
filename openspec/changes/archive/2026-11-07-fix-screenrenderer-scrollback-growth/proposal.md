## Why

The TUI reached 3.9 GB RSS after 4 days, ~1 GB of it in x/vt `Scrollback.Push`. A live heap profile of a freshly launched TUI shows the same signature growing within the first half hour: ~31 MB under `agent.ScreenRenderer.render` (the needs-input / content-idle scan emulator).

`ScreenRenderer.render` "resets" its single shared emulator with RIS (`ESC c`) before every render, but RIS does not clear x/vt's scrollback. Every render of a tail taller than the screen pushes the scrolled-off lines into the emulator's scrollback, which was never capped below x/vt's 10,000-line default. The renderer is shared across all sessions and ticks, so scrollback grows monotonically to the cap (measured ~26 KB/line at 200 cols, so ~260 MB per renderer), and every push beyond the cap pays an O(cap) `slices.Delete` shift on the tview goroutine. The renderer only ever reads the visible screen (`emu.String()`), so none of that scrollback is used.

## What Changes

- `ScreenRenderer` (agent) creates its emulator with a scrollback cap of 1 line and calls `ClearScrollback()` after each RIS, so retained scrollback is bounded regardless of tail size or render count.
- The daemon-side usage-budget and Codex probe emulators (same RIS-only pattern) get the same treatment.
- uxlog lines for renderer emulator creation and reset counts.
- No supervisor ProtocolVersion bump, no wire changes, no new `screen.Sync()`.

## Capabilities

### Modified Capabilities

- `terminal-rendering`: screen-reconstruction emulators used for text scraping retain bounded scrollback.

## Impact

`internal/agent/needsinput.go`, `internal/usagebudget/usagebudget.go`, `internal/backendtier/codexprobe.go`, gotchas in `context/knowledge/gotchas/pty-terminal.md`.

## Non-goals

The replay emulator's 50K-line cap (by design, up to 2 slots) is measured but left alone here; a cap proposal follows if the heap time series shows it dominating.
