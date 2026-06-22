# Tasks

## 1. Connector disconnect detection (red → green)

- [x] 1.1 Test: unexpected pump exit (server closes the socket) fires `onClose` exactly once
- [x] 1.2 Test: explicit `Close()` does NOT fire `onClose` (no phantom reconnect on teardown)
- [x] 1.3 Test: `onClose` is safe when nil / fires at most once across both pumps exiting
- [x] 1.4 Implement: `explicitClose` flag + `sync.Once`-guarded `fireClose(err)`; call from read+write pumps on non-explicit exit; `SetOnClose` setter; add `SetOnClose` to the `pluginConnector` interface + `fakePluginConnector`

## 2. Reconnect state + redial loop (red → green)

- [x] 2.1 Test: on disconnect while active, the mount enters `reconnecting`, the overlay page is shown, and a redial loop starts
- [x] 2.2 Test: backoff schedule is the capped sequence (250ms→500ms→1s→2s, steady 2s), computed from attempt count
- [x] 2.3 Test: successful redial wires a fresh connector to the same sinks, resets `connReady/focusSent/lastSent`, clears `reconnecting`, dismisses the overlay
- [x] 2.4 Test: after a successful resume the reconciler sends resize (real inner rect) then focus exactly once (reuses post-layout handshake)
- [x] 2.5 Test: disconnect after deactivate is ignored (mount no longer active)
- [x] 2.6 Implement: reconnect fields on `pluginViewMount`; `onDisconnect` handler (QueueUpdateDraw-guarded); redial goroutine with cancel stored on the mount; fresh-connector rewire + state reset; overlay show/hide

## 3. Reconnect overlay (red → green)

- [x] 3.1 Test: overlay renders the plugin title and the "Reconnecting…" message; flips to "Still trying…" after the ~2 min grace (driven by `nowFn`)
- [x] 3.2 Implement: overlay modal (reuse modal styling) on a dedicated `tview.Pages` page; no `screen.Sync()`

## 4. Keys while reconnecting (red → green)

- [x] 4.1 Smoke test: Esc while reconnecting exits to the task list (cancels loop, removes overlay); Esc while live still forwards to the plugin
- [x] 4.2 Smoke test: double-Ctrl+Q while reconnecting still force-exits
- [x] 4.3 Implement: `modePluginView` branch handles Esc-to-exit when `reconnecting`; `deactivatePluginView` cancels the loop + removes the overlay + resets reconnect fields

## 5. Wrap-up

- [x] 5.1 `openspec validate --all --strict` clean
- [x] 5.2 Gotcha documented in `context/knowledge/gotchas/keybindings.md` (reconnect: onClose suppression on explicit close, Esc-exit only while reconnecting, laidOut survives reconnect) + `pty-terminal.md` if relevant
- [x] 5.3 `make pre-pr` clean (build → vet → fmt-check → lint-pr → vuln → test-cover-gate)
