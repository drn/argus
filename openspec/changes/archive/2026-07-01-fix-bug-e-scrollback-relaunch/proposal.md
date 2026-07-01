## Why

**BUG-E — after relaunching argus (a TUI restart that reattaches to a
still-running agent session), the agent pane's scrollback is truncated: you
cannot scroll back to session history that existed BEFORE the relaunch.** Prior
fixes (raising the read/scrollback caps) never fully closed it.

The daemon/supervisor owns the agent PTY and survives a TUI relaunch, and the
on-disk session log (`~/.argus/sessions/<task>.log`) holds the FULL history (it
is truncated only on `StartSession`, never on reattach). The replay path already
reads the log on demand — but the read size is derived from a
`(scrollOffset + viewport) * cols * 3` bytes-per-visible-line heuristic, floored
at 8MB. For escape-dense agent output (Claude's in-place spinner/stream
repaints), 8MB of log yields far FEWER net scrollback lines than that heuristic
assumes. Concretely, an 8MB tail can produce only ~4K net lines, while the
heuristic would need `scrollOffset > ~23K` to grow the read past the 8MB floor.

Because `scrollOffset` is clamped back to `replayEmuMaxScroll` after every
rebuild (`consumeReplayRebuildPendingLocked`), pressing Up at the loaded ceiling
re-asks for the SAME 8MB window → same `maxScroll` → a **feedback-loop
deadlock**: the user physically cannot scroll further even though tens of MB of
older log are on disk and the 64MB / 50K-line caps are nowhere near hit. Raising
the 8MB floor only moves the stall to a proportionally higher line count; it
never breaks the loop.

## What Changes

- **When the user scrolls PAST the currently-loaded scrollback window on an
  alive session and older log bytes remain below it, the replay rebuild reads a
  fixed byte CHUNK further back than the previous window's first byte, instead
  of trusting the line heuristic.** This guarantees each ceiling-hit rebuild
  reaches strictly older history, independent of output density, until the whole
  log is loaded (first byte reaches 0) or the bounded 64MB single-read cap is
  reached.
- The extension is **lazy / on-demand**: the pane still loads only a bounded
  window per rebuild (never eagerly replays the entire, possibly multi-MB, log
  into the emulator on reattach). The monotonic-`firstByteOffset` invariant,
  the `scrollOffset` clamp, ESC-boundary alignment, and the no-`Sync` rendering
  contract are all preserved.
- **Bounds are unchanged and deliberate:** the single-read cap stays 64MB and
  the replay emulator's scrollback stays 50K lines. These are the honest,
  memory-bounded ceilings; the stall (not the bounds) was the bug, and for the
  vast majority of real sessions the extension now reaches the full on-disk
  history.

## Capabilities

### Modified Capabilities

- `terminal-rendering`: scrollback browsing now EXTENDS the loaded window by
  reading further back from the on-disk session log when the user scrolls past
  the currently-loaded window — previously an escape-dense session stalled at
  the initially-loaded (density-underestimated) window and older history was
  unreachable after a reattach.

## Impact

- **Modified code:**
  - `internal/tui/terminal/terminalpane.go` — `replayRebuildReadSize` gains an
    `extend` parameter (grows the read a `scrollExtendChunk` beyond the previous
    window's first byte); `asyncReplayRebuild` threads it through; `Draw`
    computes the extend signal (alive + dimension-match + `firstByte > 0` +
    `scrollOffset > replayEmuMaxScroll`) at the rebuild kick site; `uxlog` on the
    extend read.
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  read-only replay/scroll behaviour; dead-session replay is unchanged.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
