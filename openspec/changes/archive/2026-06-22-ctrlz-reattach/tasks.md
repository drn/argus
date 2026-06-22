# Tasks

## 1. Fullscreen state on the focus machine

- [ ] 1.1 Add a `fullscreen bool` field + `Fullscreen()` accessor and `ToggleFullscreen()` to `FocusMachine` (no-op enabling while on the rail; always flips off there).
- [ ] 1.2 Clear fullscreen whenever focus lands on the rail: in `Retreat` (Coord→Rail), `ToRail`, and `rebalance` (bump-to-rail).
- [ ] 1.3 Tests in `focus_test.go`: toggle on a pane, survive Advance, clear on Retreat-to-rail / ToRail / rebalance, rail-toggle no-op.

## 2. Trap Ctrl+Z → fullscreen in the page

- [ ] 2.1 Add `case tcell.KeyCtrlZ` to `HeraPage.InputHandler` → `p.focus.ToggleFullscreen()`; always `return` (consume), placed so it fires regardless of focused region.
- [ ] 2.2 Render the fullscreen layout in `Draw`: rail + single focused pane filling the right area; collapse the hidden pane's hit-test rect; keep present-flags from the normal split; no `Sync`; full-rect coverage.
- [ ] 2.3 Tests in `page_test.go`: `Ctrl+Z` toggles `Machine().Fullscreen()`; on rail it stays off but is consumed; fullscreen Draw smoke (no panic, only focused pane rect non-zero).

## 3. Revive a stopped worker via Enter

- [ ] 3.1 Broaden the page `Enter` gate: fire `OnReattach` for a dead session (any role) OR a live worker/freelance role; a live coordinator stays navigate-only. Still advance focus.
- [ ] 3.2 `heraReattach` (App): branch dead→`startSession`; live worker→idle-gated in-place stop+resume (`reviveHeraWorker`), reusing `pendingRerenderRestart`, `sessionBlockedOnPrompt`, `runner.Stop`, statusbar. Live coordinator → no-op.
- [ ] 3.3 Update `keyset_test.go` (`Enter` on a live worker now fires reattach; add a live-coordinator-does-not-fire case) and add an App-level test for the revive branch in `heraactions_test.go`.

## 4. Docs (mandatory, same PR)

- [ ] 4.1 Help overlay: add `{"ctrl+z", "fullscreen pane"}` to the "Hera View (rail)" section in `modal/help.go`; update the Enter label to mention reviving a stopped session; assert both in `help_test.go`.
- [ ] 4.2 README Reference appendix keybinding table: add `^Z` (fullscreen) under the Hera rail keys; note the Enter revive.
- [ ] 4.3 `context/knowledge/gotchas/hera-view.md`: document the suspend footgun, the `^Z`→fullscreen trap, and the stopped-vs-dead reattach distinction.

## 5. Validate

- [ ] 5.1 `make pre-pr` passes clean (build + vet + fmt-check + lint-pr + vuln + test-cover-gate).
- [ ] 5.2 Live probe via `hera-view-probe`: confirm `^Z` fullscreens (and does NOT suspend) and that a stopped worker pane can be revived from the Hera view.
