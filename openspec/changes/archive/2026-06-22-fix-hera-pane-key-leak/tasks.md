# Tasks

## 1. Focus-gated global key routing

- [ ] 1.1 Add `App.heraPaneFocused()` — true when `ActiveTab()==TabHera`, the page is local (not remote), and the focus machine is on a content pane (not `FocusRail`).
- [ ] 1.2 Guard the rune shortcuts (`q`/`1`/`2`/`3`/`?`) with a single early `break` at the top of `handleGlobalKey`'s rune switch when `heraPaneFocused()`.
- [ ] 1.3 Guard `Ctrl+C` (break before `tapp.Stop`) and `Ctrl+L` (`&& !heraPaneFocused()`) so they fall through to the page when a pane is focused.

## 2. Tests

- [ ] 2.1 `heraactions_test.go`: `heraPaneFocused()` is false on the Tasks tab, false on the Hera tab with the rail focused, true with a content pane focused; and `handleGlobalKey` returns the event (fall-through, not nil) for `q`/`1`/`2`/`3`/`?`/`Ctrl+C`/`Ctrl+L` while a pane is focused, but consumes `?` (opens help) on the rail.

## 3. Docs + validate

- [ ] 3.1 `context/knowledge/gotchas/hera-view.md`: document the leak + the focus gate.
- [ ] 3.2 `make pre-pr` passes clean.
