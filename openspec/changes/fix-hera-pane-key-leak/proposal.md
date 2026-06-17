## Why

BUG-001: global/rail key shortcuts leak into a focused Hera content pane. The
Hera tab lives in `modeTaskList`, and `handleGlobalKey` intercepts `q` (quit
argus), `1`/`2`/`3` (tab switch), `?` (help), `Ctrl+C` (quit), and `Ctrl+L`
(screen Sync) for that mode BEFORE the event reaches `HeraPage`. So when an
operator focuses a coordinator/worker pane and types — pressing `q` quits ALL of
argus instead of sending `q` to the agent's PTY; `Ctrl+C` quits instead of
interrupting the agent; `1`/`2`/`3`/`?` switch tabs / open help instead of
typing. A focused pane is a live terminal; these keystrokes must reach it.

The agent view (`modeAgent`) already solves this by surrendering the same keys to
its PTY. The Hera tab's focused panes need the equivalent gate.

## What Changes

- Add `App.heraPaneFocused()` — true when the Hera tab is active AND focus is in
  a content pane (coordinator or agent/details), not the rail.
- Gate the leaking globals in `handleGlobalKey` on `!heraPaneFocused()`: the
  rune shortcuts (`q`/`1`/`2`/`3`/`?`) fall through via a single guard at the top
  of the rune switch; `Ctrl+C` and `Ctrl+L` fall through via per-case guards. The
  fall-through reaches `HeraPage.InputHandler`, which forwards the key to the
  focused pane's PTY.
- The rail keeps every global (it is not a content pane → `heraPaneFocused()` is
  false), so quit/tab-switch/help still work while the rail holds focus. Escape a
  pane with `Ctrl+Q` (already routed to the page) to use the globals again.

## Capabilities

### Modified Capabilities

- `hera-view`: Global key shortcuts are focus-gated on the Hera tab — a focused
  content pane receives them as PTY input instead of triggering argus actions.

## Impact

- **Modified code:** `internal/tui/app.go` (`heraPaneFocused` helper + three
  guards in `handleGlobalKey`). No new keys, no rebinds — purely a routing fix,
  so no help-overlay/README key-table change is required.
- **Docs:** `context/knowledge/gotchas/hera-view.md` (the leak + the gate).
- **No new dependencies, no schema change, no daemon RPC.** Specs stay LOCAL
  DOCS only; gate stays `make pre-pr`.
