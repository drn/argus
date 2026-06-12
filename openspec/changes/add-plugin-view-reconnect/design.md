# Design: plugin-view reconnect

## Context

- The plugin-view WebSocket client is `internal/tui/views/connector.go`. `Dial` starts a read pump (plugin→argus: binary ANSI to `onBytes`, text control frames to `onControl`) and a write pump (argus→plugin keystrokes from `inBytes`). Both pumps return silently on EOF/error today; the only signal to the App is the `Dial` return value on the *initial* dial.
- App-side lifecycle lives in `internal/tui/plugin_views.go`. `activatePluginView` builds a connector via `pluginConnFactory`, dials in a goroutine, and flips `connReady` once dialed. `deactivatePluginView` sends blur, closes, and resets state.
- The resize/focus handshake is already robust: `reconcilePluginViewSize` (run from `afterDraw` on every draw) sends a resize envelope whenever the computed viewport drifts from `lastSentCols/Rows`, gated on `connReady && laidOut`, then sends focus once (`focusSent`). This is exactly the "fresh connection" handshake a reconnect needs — reset the four flags and the machinery re-runs.
- Key routing: `app.go handleGlobalKey` `modePluginView` branch forwards everything to the plugin except the double-Ctrl+Q failsafe and the single key that dismisses a plugin help overlay.

## Goals / Non-Goals

- **Goal:** detect an unexpected WS drop, show an app-level "Reconnecting…" overlay, retry with backoff, resume seamlessly with a fresh resize/focus handshake.
- **Goal:** QQ and (during reconnect) Esc exit to the task list.
- **Non-goal:** any approval/permission handshake (explicitly dropped by Aaron).
- **Non-goal:** protocol changes, daemon-side changes, or buffering keystrokes across the gap (keys typed while disconnected are dropped — the plugin re-renders a full frame on resume anyway).

## Decisions

### 1. Disconnect detection: connector `onClose` callback

Add a `onClose func(error)` field to `Connector`, set via `NewConnector` (new trailing param) — but to avoid churning every call site, wire it through the existing factory signature by adding a setter `SetOnClose`. The pumps call a guarded `fireClose(err)` exactly once (a `sync.Once`) when they exit. `Close()` sets an `explicitClose` flag (under the existing mutex) before closing, and `fireClose` is suppressed when that flag is set or `closeCh` is already closed — so deactivate/failsafe never trigger a phantom reconnect. `onClose` runs on the pump goroutine, so the App handler must hop to the tview goroutine via `QueueUpdateDraw`, exactly like `onControl`.

Rationale for a setter over a constructor param: the `pluginConnectorFactory` type and its stub (`fakePluginConnector`) are referenced across tests; a setter on the concrete `*Connector` keeps the factory signature stable and lets the App register the handler right after construction. The `pluginConnector` interface gains `SetOnClose(func(error))`.

### 2. Reconnect state on `pluginViewMount`

Add: `reconnecting bool`, `reconnectAttempt int`, `reconnectCancel context.CancelFunc` (or a `chan struct{}` stop signal), and `reconnectStarted time.Time` (for the ~2 min "still trying" flip; uses `a.nowFn` so tests stay deterministic). All owned by the tview goroutine, set/reset in activate/deactivate and the QueueUpdateDraw closures — no locking, matching the existing reconciliation fields.

### 3. The reconnect overlay

App-level overlay rendered as a `tview.Pages` page on top of the frozen pane (mirrors `pluginHelpPage`). A small centered `modal` widget (reuse the error-modal/help-modal styling) showing the plugin title and the attempt/elapsed state:

- `< 2 min`: "Reconnecting to {Title}…"
- `>= 2 min`: "Still trying to reach {Title}… Press Esc or Ctrl+Q twice to exit."

The frozen pane stays underneath; the overlay does not clear it. Drawn via the standard draw cycle (no `screen.Sync()` — honoring the UX-rendering rules).

### 4. The backoff redial loop

On unexpected disconnect (handler runs on tview goroutine):
1. If this mount is no longer `a.activePlugin`, ignore (raced with deactivate).
2. Mark `m.reconnecting = true`, record `reconnectStarted`, show the overlay, refresh the bottom bar to a reconnect hint.
3. Spawn a goroutine driven by a context whose cancel is stored in `m.reconnectCancel`. Loop: build a fresh connector (same `onBytes`/`onControl`/`onClose`, same persistent `bytesIn`/`keysOut`), `Dial` with a per-attempt timeout. On failure: bump attempt, sleep `backoff(attempt)` (250ms→500ms→1s→2s, steady 2s), refresh overlay text via `QueueUpdateDraw`, repeat until the context is cancelled. On success: hop to the tview goroutine, re-check `a.activePlugin == m && m.reconnecting`, then:
   - assign `m.conn = newConn`,
   - reset `connReady=false, laidOut(keep — pane is still laid out), focusSent=false, lastSentCols/Rows=0, sendFailLogged=false, reconnecting=false`,
   - mark `connReady=true` (the dial already completed) and dismiss the overlay,
   - kick a draw so `reconcilePluginViewSize` sends resize→focus.

`laidOut` stays true across a reconnect (the pane never un-laid-out), so the post-layout rect is immediately authoritative — the first envelope on the new connection is the real size, not the 13x8 default.

Backoff is computed from `attempt` (no wall-clock randomness needed; deterministic for tests). The loop never gives up on its own — only `deactivatePluginView` (via QQ or Esc) cancels it.

### 5. Keys while reconnecting

In `handleGlobalKey`'s `modePluginView` branch, before the Ctrl+Q failsafe check, add: if `a.activePlugin != nil && a.activePlugin.reconnecting` and the key is `KeyEscape`, call `deactivatePluginView()` and return nil. QQ failsafe is unchanged (already exits). When not reconnecting, Esc forwards to the plugin as today.

### 6. Teardown

`deactivatePluginView` cancels `m.reconnectCancel` (if set), removes the overlay page, and resets all reconnect fields — alongside the existing blur/close/reset. Because `Close()` sets `explicitClose`, the connector that gets closed here never fires `onClose`, so no phantom reconnect is started during teardown. A reconnect goroutine that wins the race and dials successfully after cancel re-checks `a.activePlugin == m && m.reconnecting` under QueueUpdateDraw and bails (closing the orphan connection).

## Risks / Trade-offs

- **Keystrokes during the gap are dropped.** Acceptable: the plugin re-renders fully on resume, and a frozen view can't meaningfully accept input anyway.
- **A wedged-but-alive daemon (accepts WS, never frames)** would dismiss the overlay yet show a stale frame until the reconciler/plugin pushes a frame. The resize→focus handshake nudges the plugin to repaint; same behavior as a normal fresh activation.
- **Goroutine leak risk** if cancel is missed — mitigated by storing the cancel on the mount and calling it in the single teardown path, plus the active-plugin re-check on success.

## Migration

None — no schema, no protocol, single user. Existing plugin views gain reconnect transparently.
