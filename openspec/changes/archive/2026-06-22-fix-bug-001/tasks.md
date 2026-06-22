# Tasks

## 1. Pane self-revives on Enter

- [x] 1.1 `terminal.TerminalPane`: add an `OnReattach func()` callback field (near `OnClick`/`OnBranchChange`).
- [x] 1.2 `terminal.TerminalPane`: add an `InputHandler()` method that fires `OnReattach` ONLY when `Enter` is pressed AND no live session is attached (`session == nil || !Alive()`); nil-guard the callback; fall through for every other key and for `Enter` while alive.

## 2. Route the focused-pane dead-session Enter to the revive

- [x] 2.1 `HeraPage.forwardKey`: in the dead/nil-session branch (after the re-resolve fails), route the event to the pane's `InputHandler` instead of dropping silently — so `Enter` fires `OnReattach`; any other key is still dropped.
- [x] 2.2 `HeraPage.bindPane`: after `SetSession(sess)`, wire `tp.OnReattach` to revive THAT pane's bound task via a new `reattachPane(tp)` helper (agent pane → selected worker; coord pane → the orchestrator's coordinator it shows), routing through the page's `OnReattach` → `App.heraReattach`.

## 3. Tests

- [x] 3.1 `terminalpane_test.go`: Enter-when-not-alive fires `OnReattach`; Enter-when-alive does NOT; a non-Enter key does NOT; nil `OnReattach` is inert (no panic).
- [x] 3.2 `internal/tui/hera` SimulationScreen smoke: Enter in a focused dead-session pane fires the revive path.

## 4. Docs + validate

- [x] 4.1 `context/knowledge/gotchas/hera-view.md`: document that the pane self-revives on `Enter` (only-when-not-alive, nil-guarded so the task page stays inert).
- [x] 4.2 `go test ./internal/tui/terminal/... ./internal/tui/hera/...` passes; `make fmt` clean.
