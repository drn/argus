## Why

BUG-001: when a Hera worker (or coordinator) session is suspended/dead, its
focused pane shows the overlay `Session not running - press Enter to start`, but
pressing `Enter` while that pane holds focus does NOTHING. The only way to revive
the session is to focus the rail, select the worker entry, and press `Enter` (the
rail's reattach path). The overlay's promise is unmet.

Root cause: `internal/tui/terminal/terminalpane.go` has no `InputHandler` method,
so it inherits tview's `Box` default which silently discards keystrokes. The rail
path works because `HeraPage.handleRailMutation` receives `Enter` and fires
`OnReattach`. A FOCUSED pane never fires any revive callback — its keys are routed
by `HeraPage.forwardKey`, which (when the session is nil / `!Alive()`) re-resolves
and then DROPS the keystroke (the BUG-013 path). So `Enter` is dropped.

## What Changes

- `terminal.TerminalPane` gains an `OnReattach func()` callback and an
  `InputHandler()` method. The handler fires `OnReattach` ONLY when `Enter` is
  pressed AND no live session is attached (the state behind the "Session not
  running" overlay); for every other case — non-`Enter` keys, or `Enter` while
  the session IS alive — it falls through so live PTY input is unaffected. The
  callback is nil-guarded, so a consumer that does not wire it (the task page)
  keeps the pane inert exactly as today.
- `HeraPage.forwardKey` no longer ends a dead-session keystroke with a silent
  drop: it routes the event to the pane's own `InputHandler`, so `Enter` on a
  dead/nil pane revives the worker via `OnReattach` (which routes through the
  page's already-wired `OnReattach` → `App.heraReattach`, the SAME revive path the
  rail uses), while any other key is dropped as before.
- `HeraPage.bindPane` wires each fed pane's `OnReattach` to revive THAT pane's
  bound task: the agent pane targets the selected worker; the coordinator pane
  targets the orchestrator's coordinator it is showing.

## Capabilities

### Modified Capabilities

- `hera-view`: `Enter` in a focused pane showing the "Session not running"
  overlay revives that pane's session (extends the rail-only reattach to the
  focused pane), with live PTY input unaffected.

## Impact

- **Modified code:** `internal/tui/terminal/terminalpane.go` (`OnReattach` field +
  `InputHandler`), `internal/tui/hera/panes.go` (`forwardKey` dead-branch routes to
  the pane `InputHandler`; `bindPane` wires `OnReattach`; `reattachPane` per-pane
  selection target).
- **Tests:** `internal/tui/terminal/terminalpane_test.go` (Enter-when-dead fires
  `OnReattach`; Enter-when-alive / non-Enter do not), `internal/tui/hera`
  SimulationScreen smoke (Enter in a focused dead-session pane fires the revive).
- **Docs:** `context/knowledge/gotchas/hera-view.md` (pane self-revives on Enter
  invariant).
- **No new key** (Enter-to-revive is already the rail's documented behavior; this
  extends it to the pane), so the help overlay / README key tables do NOT change.
  **No schema change, no daemon RPC, no `screen.Sync()`** — input routing only.
  Specs stay LOCAL DOCS only; gate stays `make pre-pr`.
