## 1. Reproduce the corruption

- [x] 1.1 Confirm via a SimulationScreen test (and against the real session log)
  that wide-authored scrollback rendered in a narrower pane corrupts (stranded
  right-edge column) while rendering at the authored width is clean.

## 2. Emulate at the authored width, clip to the pane

- [x] 2.1 Add `replayEmuDims` (live PTY size → `.size` sidecar → pane floor;
  never narrower than the pane).
- [x] 2.2 In `Draw`'s scroll path, build/cache/paint the replay emulator at the
  resolved authored dims while clipping to the pane inner rect; the fallback
  paint uses each emulator's own build dims.
- [x] 2.3 `uxlog` when the authored size exceeds the pane (per rebuild kick).

## 3. Tests

- [x] 3.1 Unit: `replayEmuDims` returns the authored width when wider than the
  pane, the pane when already wide enough, the sidecar for dead sessions, and
  the pane floor when nothing is known.
- [x] 3.2 SimulationScreen: a live session authored wide, viewed narrow, with a
  CHA-positioned right-edge marker — scrolling up produces no stranded
  right-edge column and the replay emulator is built at the authored width.

## 4. Docs

- [x] 4.1 Add a gotcha bullet to `context/knowledge/gotchas/pty-terminal.md`.
