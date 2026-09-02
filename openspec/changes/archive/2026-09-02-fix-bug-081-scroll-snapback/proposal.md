## Why

**BUG-081 — in the Hera split view, scrolling up a live-looking agent pane "scrolls up a line and then jumps back to the bottom, repeatedly"; the pane cannot be scrolled at all until the TUI is restarted.** Reported against the Coordinator/Agent dual-pane terminals; the classic fullscreen single-agent view is unaffected.

Ground truth from the live `~/.argus/ux.log`: 2,843 occurrences of `[terminalpane] clamp scrollOffset old=N new=0 after rebuild`, arriving in bursts of one clamp per mouse-wheel notch (`old=3`, then `6`, `9`, `12` as the user out-paces the rebuild), each interleaved with a `hera coord pane branch changed` pair — i.e. one scroll-mode entry plus one replay rebuild per notch, every notch resolving to `new=0`. Replaying the implicated session's real on-disk log through the production emulator confirms why: `alt=true scrollbackLen=0 lastRow=68 totalLines=69 maxScroll=0`. The recording is a full-screen agent's in-place repaint (one `ESC[?1049h` at byte 13, no `ESC[?1049l`, zero line feeds, 1,212 `ESC[H`), so it carries no linear scrollback whatsoever.

Argus already knows not to scroll such content — BUG-031 suppresses scroll-mode entry and BUG-026 forwards the wheel to the agent instead. Both hang off one predicate, `TerminalPane.InAltScreen()`, which read **only the live emulator `tp.emu`**. That emulator is created **exclusively by `renderLive`**, and `Draw` runs `renderLive` only for a pane at the live tail whose session is `Alive()`. A pane rendering through the replay path therefore never has one, and the predicate silently answered "main screen" for exactly the panes most likely to be showing a full-screen agent's zero-scrollback recording. Scroll mode was entered against content whose `maxScroll` is 0, and the very next paint clamped the offset straight back to 0 — the twitch-and-snap, on every notch, forever.

Reproduced against the real log: after settling, `InAltScreen=false`, `replayEmuMaxScroll=0`, and each `ScrollUp(3)` yields `scrollOffset=3` → redraw → `scrollOffset=0`, three notches running.

**Why the Hera split view and not the classic agent view:** the classic view is only ever entered on a task the user explicitly attached to, which starts/re-dials a live session. Hera panes rebind on every rail hop and keep whatever handle the runner returns — including a BUG-013 dead handle (stream torn down by a StreamLost relay or a daemon bounce while the agent process is still running). That pane looks live to the operator but is `!Alive()` to `Draw`, so it renders through the replay path for its whole lifetime. It also explains "unless I restart the TUI": a restart re-dials a live stream, which restores the live emulator and with it the guard.

**This is NOT the garbling family (BUG-078 / #937 / #964 / #965 / #966).** Those are width/offset reconstruction defects that corrupt *what* is painted. This one paints correctly and mis-answers *whether the content is scrollable*. It shares no code with the ring/log merge or width-drift paths, and it predates this week's work — the blind spot has existed since the alternate-screen guard was introduced.

## What Changes

- **The alternate-screen determination now describes the content the pane is actually rendering, not just the live emulator.** `asyncReplayRebuild` records the built replay emulator's alternate-screen state alongside the cursor-visibility and max-scroll it already records; `InAltScreen()` keeps the live emulator as the authority whenever one exists and falls back to that recorded state when it does not. Both existing guards (BUG-031 keyboard suppression, BUG-026 wheel forwarding) therefore work on replay-path panes without either of them changing.
- **The recorded flag never outlives the emulator it describes.** It is written with `replayEmu` in `asyncReplayRebuild` and cleared with it in `ResetVT` and `invalidateReplayCache`, so it can never latch: once a recording is main-screen (or the agent leaves the alternate screen), scrollback resumes.
- **The keyboard no-scrollback affordance now tells the truth for a replay pane.** A new `TerminalPane.NoScrollbackHint()` is the single source of wording for both scroll-up call sites: a LIVE full-screen agent still gets "scroll within the agent" (it can be scrolled within — the wheel is forwarded to it), while a pane replaying such an agent's recording gets "Fullscreen agent output — no scrollback recorded", because there is no running agent to defer to.
- **No keybinding, schema, RPC, or wire-contract change.** No change to what is painted, to the replay read/extend heuristics, or to the anchor-lock and clamp arithmetic.

## Capabilities

### Modified Capabilities

- `terminal-rendering`: the "Mouse wheel forwarded to full-screen agents" and "Keyboard scroll suppressed for full-screen agents" requirements now define the alternate-screen determination over the content being rendered — including a replay-path pane with no live emulator — instead of assuming "finished/replay ⇒ not on the alternate screen". Both requirements gain the replay-pane scenarios, and the keyboard requirement specifies the two affordance wordings.

## Impact

- **Modified code:**
  - `internal/tui/terminal/terminalpane.go` — new `replayEmuAltScreen` field recorded by `asyncReplayRebuild` and cleared by `ResetVT` / `invalidateReplayCache`; `InAltScreen()` falls back to it; new `NoScrollbackHint()`.
  - `internal/tui/app.go` — the agent view's scroll-up suppression uses `NoScrollbackHint()` for its status-bar affordance.
  - `internal/tui/hera/panes.go` — `forwardKey`'s PgUp branch uses `NoScrollbackHint()` for its `OnInfo` affordance.
- **New tests:** `internal/tui/terminal/bug081_test.go` (predicate, snap-back repro, accelerated scroll, main-screen control case, live-emulator precedence, reset clearing, hint wording) and `internal/tui/hera/bug081_test.go` (end-to-end wheel and PgUp over a dead-handle Hera pane).
- **Named follow-up, deliberately NOT in this change:** a Hera pane bound to a task with **no session handle at all** never reaches the replay path, so it cannot hit this bug — but only because `bindPane`'s `SetTaskID` → `ResetVT` → `SetSession(nil)` order has `ResetVT` null out the `replayData` that `SetTaskID` just loaded, leaving `HasContent()` false and the pane showing "Session not running - press Enter to start" instead of replaying its recorded output. That is a separate display defect (missing replay, not mis-scrolled replay) with its own visible behavior change, and it is not fixed here.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
