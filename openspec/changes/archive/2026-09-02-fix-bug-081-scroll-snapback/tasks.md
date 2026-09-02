## 1. Root-cause evidence

- [x] 1.1 Confirm the snap-back from the live `~/.argus/ux.log`: `[terminalpane] clamp scrollOffset old=N new=0 after rebuild` in per-wheel-notch bursts (2,843 occurrences), interleaved with `hera coord pane branch changed` pairs.
- [x] 1.2 Replay the implicated session's real on-disk log through the production emulator and confirm `alt=true scrollbackLen=0 totalLines=69 maxScroll=0` — the content genuinely has no linear scrollback.
- [x] 1.3 Confirm `InAltScreen()` DOES suppress scrolling once the live emulator exists, and does NOT when the pane renders through the replay path (`tp.emu == nil`) — isolating the blind spot.

## 2. Red tests

- [x] 2.1 `internal/tui/terminal/bug081_test.go`: synthetic alternate-screen recording (enter alt screen, repaint in place, no line feeds) + a line-oriented control recording; a `replayPane` helper that puts a pane in the no-live-emulator state and settles the async rebuild.
- [x] 2.2 Snap-back repro: three `ScrollUp(mouseScrollStep)` notches, each followed by the redraw that clamps — the offset must never leave the tail.
- [x] 2.3 Control case: a main-screen replay pane must still enter scroll mode and stay there.
- [x] 2.4 `internal/tui/hera/bug081_test.go`: end-to-end over a dead-handle (`!Alive()`) Hera pane — mouse wheel through `HeraPage.MouseHandler`, and PgUp through `forwardKey`. Readiness is judged by the recording appearing on screen, deliberately not by the predicate under test.

## 3. Fix

- [x] 3.1 Add `replayEmuAltScreen` to `TerminalPane`, recorded from `emu.IsAltScreen()` in `asyncReplayRebuild` alongside `replayEmuCursorVisible` / `replayEmuMaxScroll`.
- [x] 3.2 Clear it wherever the replay emulator it describes is discarded: `ResetVT` and `invalidateReplayCache`.
- [x] 3.3 `InAltScreen()`: live emulator when present (unchanged authority), else the recorded replay state under `tp.mu`.
- [x] 3.4 Add `NoScrollbackHint()` as the single source of affordance wording — "scroll within the agent" for a live session, "no scrollback recorded" for a replay pane.
- [x] 3.5 Route both keyboard scroll-up suppression sites through it: `internal/tui/app.go` (agent view status bar) and `internal/tui/hera/panes.go` (`forwardKey` PgUp → `OnInfo`).

## 4. Verify

- [x] 4.1 Re-run the red tests against the pre-fix predicate to confirm they fail on the behavior (offset entering scroll mode), not on setup.
- [x] 4.2 `make pre-pr` green.

## 5. Docs

- [x] 5.1 Gotcha bullet in `context/knowledge/gotchas/pty-terminal.md` (the guard is live-emulator-only and the replay path has no live emulator).
- [x] 5.2 Gotcha bullet in `context/knowledge/gotchas/hera-view.md` (why the Hera split view is the exposure and the classic agent view is not).
- [x] 5.3 Update `context/knowledge/index.md` coverage cells.

## 6. Archive

- [x] 6.1 Merge the delta into `openspec/specs/terminal-rendering/spec.md` and move the change folder to `openspec/changes/archive/<date>-fix-bug-081-scroll-snapback/`, committed on the change branch before merge.
