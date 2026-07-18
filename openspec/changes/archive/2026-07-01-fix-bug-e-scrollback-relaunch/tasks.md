## 1. Reproduce the stall

- [x] 1.1 Confirm via a test that a ceiling-hit rebuild on an escape-dense log
  re-reads the same window (first byte unchanged), leaving older history
  unreachable.

## 2. Lazy on-demand extension

- [x] 2.1 Add `scrollExtendChunk` and an `extend` parameter to
  `replayRebuildReadSize`: when set (and older bytes remain), grow the read a
  chunk beyond the previous window's first byte; keep the 64MB cap.
- [x] 2.2 Thread `extend` through `asyncReplayRebuild` (and its callers) and log
  the extend read via `uxlog`.
- [x] 2.3 In `Draw`, compute the extend signal at the rebuild kick site (alive +
  dimensions match + `firstByte > 0` + `scrollOffset > replayEmuMaxScroll`) and
  pass it, so a dimension-change rebuild still reads fresh (not an extend).

## 3. Tests

- [x] 3.1 Unit: `replayRebuildReadSize` with `extend=false` does not grow the
  read (stall); with `extend=true` it moves the first byte strictly earlier and
  stays under the 64MB cap.
- [x] 3.2 Integration: through `asyncReplayRebuild`, a ceiling-hit rebuild
  without extend stalls, and repeated extend rebuilds reach the start of the
  on-disk log (first byte reaches 0).
- [x] 3.3 SimulationScreen smoke: through the real `Draw` loop, scrolling past
  the loaded ceiling of a live session extends the loaded window strictly
  further back.

## 4. Docs

- [x] 4.1 Add a gotcha bullet to `context/knowledge/gotchas/pty-terminal.md`.
