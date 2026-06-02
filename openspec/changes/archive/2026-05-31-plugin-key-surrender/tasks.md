**Design doc:** `openspec/changes/plugin-key-surrender/design.md`

## 1. Tests (write first, prove the gap)

- [x] 1.1 Encoder table tests: port every existing `terminalpane_test`/`streampane_test`/`app_test` (tcellKeyToBytes) case into the new `internal/tui/keyenc` package and assert byte-identical output (no-regression pin)
- [x] 1.2 Encoder table tests for the new modified-key cases: `Ctrl+Right`→`\x1b[1;5C`, `Shift+Right`→`\x1b[1;2C`, `Alt+Right`→`\x1b[1;3C`, `Ctrl+Shift+Right`→`\x1b[1;6C`, `Ctrl+Alt+Right`→`\x1b[1;7C`, and plain arrows/Home/End/PgUp/PgDn
- [x] 1.3 Routing/surrender tests (SimulationScreen smoke + handler unit): in plugin-view mode, Esc / Ctrl+C / `?` / a tab-switch number / a focus-rail arrow all reach the pane and trigger no argus action
- [x] 1.4 Failsafe tests: single Ctrl+Q is forwarded; two Ctrl+Q within the window force-return (deactivate); two Ctrl+Q outside the window do not
- [x] 1.5 Connector tests: a plugin→argus text frame is delivered to the control callback; `release`/`hotkeys`/`help` dispatch; unknown type and malformed JSON are ignored without disrupting the binary stream
- [x] 1.6 Bottom-bar tests: bar-flagged subset renders; reserved exit hint always present and never displaced; live update on re-push; fallback affordance with no dictionary; argus hints return after release
- [x] 1.7 Help-overlay tests: `help` frame renders the full dictionary; overlay lists only plugin hotkeys; `?` is not reserved by argus
- [x] 1.8 Confirm every `it should X` acceptance criterion in `design.md` has a corresponding failing test (Prove-It Pattern) and run `make test` to see them red

## 2. Shared key encoder

**Depends on:** Stage 1

- [x] 2.1 Create `internal/tui/keyenc` with one exported function mapping `*tcell.EventKey` → `[]byte` using the xterm `CSI 1;<mod><final>` form (`<mod> = 1 + Shift(1) + Alt(2) + Ctrl(4)`), covering runes (Alt→ESC-prefixed), Enter/Shift+Enter, Tab/Backtab, Backspace/Delete, Escape, arrows + Home/End/PgUp/PgDn (modified and unmodified), and Ctrl+letter C0 bytes
- [x] 2.2 Route `terminalpane.eventBytes` and `streampane.eventBytes` through `keyenc`; delete the duplicate allowlists
- [x] 2.3 Route `app.go:tcellKeyToBytes` (agent PTY) through `keyenc`; verify the agent view still types correctly and existing app tests stay green
- [x] 2.4 Run `make test` — Stage 1.1/1.2 now green; assert no agent-view regression

## 3. Connector control frames + release

**Depends on:** Stage 1

- [x] 3.1 Extend `internal/tui/views/connector.go` to deliver plugin→argus **text** frames to a new control callback (replacing the current drop), parsing JSON defensively; keep binary ANSI frames on the existing fast path
- [x] 3.2 Add a typed control-envelope decode for `release` / `hotkeys` / `help`; ignore unknown/malformed
- [x] 3.3 Wire `release` in `plugin_views.go` to `deactivatePluginView` via `QueueUpdateDraw` (callback runs on the read-pump goroutine — must not touch tview directly)
- [x] 3.4 Add `uxlog` calls for control-frame receipt, dispatch, and ignored/malformed frames
- [x] 3.5 Run `make test` — Stage 1.5 + release path green

## 4. Full surrender + double-Ctrl+Q failsafe

**Depends on:** Stage 1

- [x] 4.1 Rewrite the `modePluginView` branch in `app.go:handleGlobalKey` to forward every key (stop intercepting Esc); argus reserves nothing for its own nav
- [x] 4.2 Add `lastCtrlQ time.Time` to `App`, reset on `activatePluginView`; in plugin-view mode, forward a single Ctrl+Q but intercept the second within the window and call `deactivatePluginView`
- [x] 4.3 Add `uxlog` for surrender entry/exit and failsafe firing
- [x] 4.4 Run `make test` — Stage 1.3/1.4 green

## 5. Hotkey dictionary + context-sensitive bottom bar

**Depends on:** Stage 3

- [x] 5.1 Store the latest hotkey dictionary on the active `pluginViewMount`; set on `hotkeys` frame, clear on deactivate (no bleed into the next plugin)
- [x] 5.2 Add a plugin-view state setter to `widget/statusbar.go` (active flag, plugin title, dictionary); `Draw` branches to render the `bar:true` subset
- [x] 5.3 Always render the reserved "return to argus" exit hint last so plugin items cannot displace it; cap item count + total width and truncate
- [x] 5.4 Render the "<plugin> has the keyboard" fallback affordance when no dictionary is present; push plugin-mode on activate, clear on deactivate so argus hints return
- [x] 5.5 Re-render the bar on each `hotkeys` re-push (via `QueueUpdateDraw`)
- [x] 5.6 Run `make test` — Stage 1.6 green

## 6. Plugin-triggered help overlay

**Depends on:** Stage 5

- [x] 6.1 On a `help` control frame, render the full stored dictionary in argus's existing help modal, styled like argus help, showing only the plugin's hotkeys
- [x] 6.2 Confirm argus does NOT reserve `?` (it is forwarded to the plugin); the overlay is dismissible without capturing the keyboard beyond dismissal
- [x] 6.3 Run `make test` — Stage 1.7 green

## 7. Docs, gotchas, and final verification

**Depends on:** Stage 2, Stage 4, Stage 6

- [x] 7.1 Update `docs/plugins.md`: full surrender, the `release`/`hotkeys`/`help` envelopes, the double-Ctrl+Q failsafe, and the iTerm2 Cmd→Ctrl+Alt remap round-trip
- [x] 7.2 Add non-obvious gotchas to `context/knowledge/gotchas/` (encoder consolidation byte-identical invariant, control-frame-on-read-pump threading via QueueUpdateDraw, failsafe window, mod-7 round-trip) and update the index bullet counts
- [x] 7.3 Run `make fmt-check`, `make vet`, `make test`, and `make test-cover` (≥95% on touched packages); fix gaps
- [x] 7.4 Run `openspec validate plugin-key-surrender --strict`
