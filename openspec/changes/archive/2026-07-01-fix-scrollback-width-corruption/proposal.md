## Why

**The actual user-reported (screenshot-confirmed) bug: LIVE-session scroll-mode
render corruption.** With no quit/relaunch, pressing Up to scroll back a little
in a pane whose content has long lines / markdown tables shows corrupted
scrollback: text fragments into ~one-word-per-line, a vertical column of single
characters strands down the far-right edge (two widths overlaid), and the live
status footer + PTY input prompt are re-shown inside the scrolled-back content.
Only panes with long lines corrupt; short-output panes are fine.

Reproduced with the real session log: rendered at the session's authored PTY
width (100, per the `.size` sidecar) it is CLEAN (`sbLen=0` — the agent redraws
in place); rendered at a narrower pane width (e.g. 60) it shows the EXACT
screenshot corruption (`sbLen=2215` of garbage). Root cause: the scroll-mode
replay emulator is built at the current PANE width, but the committed scrollback
bytes are authored at the session's PTY width. Re-emulating wide-authored
content in a narrower emulator clamps absolute cursor positioning (CSI nG / CUP)
at the right edge and turns the agent's in-place redraws into fake garbage
scrollback — the fragmented reflow, the stranded right column, and the re-shown
footer/input. This is the same class of defect the dead-session PREVIEW already
fixes via the `.size` sidecar (emulate wide, clip to pane); the live-session
agent-view scroll path never adopted it.

## What Changes

- **The scroll-mode replay emulator is built at the width the scrollback bytes
  were AUTHORED for — the session's live PTY size, then the `.size` sidecar —
  never narrower than the pane, and `paintEmu` clips to the pane** (`renderCols
  = min(emuCols, w)`). In the common case where the pane already matches the
  session PTY size this is a no-op; it only widens the emulator when the session
  was authored wider than the current pane, turning corruption into a clean,
  horizontally-clipped render.
- At the authored width the agent's in-place redraws no longer spill into
  scrollback, so the fake garbage scrollback — and with it the fragmented
  reflow, the stranded right-edge column, and the re-shown live footer / PTY
  input prompt — disappears.
- Mirrors `previewEmuSize` / the dead-session preview fix. `uxlog` records when
  the authored size exceeds the pane (per rebuild kick, not per frame).

## Capabilities

### Modified Capabilities

- `terminal-rendering`: scrollback browsing now renders the replay at the
  content's authored PTY width and clips to the pane, instead of re-emulating
  wide-authored scrollback in a narrower pane (which clamped cursor positioning
  and corrupted the view).

## Impact

- **Modified code:**
  - `internal/tui/terminal/terminalpane.go` — new `replayEmuDims` helper
    (live PTY size → `.size` sidecar → pane floor); `Draw`'s scroll path builds
    the replay emulator, checks cache validity, and paints at the resolved
    authored dims (`replayCols`/`replayRows`) while clipping to the pane inner
    rect; the fallback paint uses each emulator's own build dims.
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure
  read-only replay rendering; the live follow-tail path and dead-session replay
  cache validity (log-size based, no sidecar) are unchanged.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
