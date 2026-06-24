# Plugin-view reconnect: survive a daemon bounce with a "reconnecting…" screen

## Why

A plugin view (e.g. hera, registered via `RegisterView` with a `ws://…/view` callback) renders entirely server-side: the plugin daemon owns the WebSocket and pushes ANSI frames; argus is just the client piping bytes into a `terminalpane`. When the plugin daemon restarts — a dev runs `iris_reload`, which rebuilds and bounces it — the WebSocket server dies with the process. Argus's `views.Connector.readPump` returns silently on EOF with no callback to the App, so the pane simply **freezes** on its last frame. The only recovery today is to quit the plugin view (double-Ctrl+Q) and relaunch it.

Aaron's goal: "Make a hera bounce a momentary stutter. I'm working along, hera bounces, I see a 'reconnecting…' screen, and I'm back." Pure client-side resilience — notice the drop, show a screen, retry with backoff, resume seamlessly. No approval/permission handshake.

## What Changes

- The connector detects an **unexpected** disconnect (read/write pump exits for a reason other than an explicit `Close()`) and fires a one-shot `onClose` callback. Explicit close (deactivate / failsafe) does NOT fire it.
- On unexpected disconnect while a plugin view is active, argus shows an **app-level "Reconnecting…" overlay** over the frozen pane (spirit of hera's per-pane REATTACHING splash, but for the whole plugin view) and starts a **backoff redial loop** against the view's `callback_url`.
- On a successful redial, argus wires a **fresh connector** to the same persistent byte sinks, **resets the resize/focus reconciliation state**, and dismisses the overlay — the existing post-layout reconciler re-sends resize→focus exactly like a new connection (reusing the fix for the "first-frame wrong dimensions" quirk). The plugin re-renders a full frame and the pane resumes seamlessly.
- The redial loop retries **effectively forever** with capped backoff; after a grace period (~2 min) the overlay flips to a "still trying…" message that surfaces the exit keys. There is no hard give-up.
- **Key handling while reconnecting:** the double-Ctrl+Q failsafe still force-exits to the task list, and a **single Esc** also exits (the plugin is gone, so full-surrender no longer applies — there is nothing to forward Esc to). While the plugin is live, Esc still forwards to the plugin (unchanged).

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `plugin-views`: adds disconnect detection, the reconnect overlay + backoff redial loop, the clean-resume handshake on redial, and reconnect-scoped Esc-to-exit. Narrows the existing key-surrender requirement so Esc surrenders to the plugin only while the plugin is connected.

## Impact

- `internal/tui/views/connector.go` — add `onClose` callback wiring; fire once on unexpected pump exit, suppress on explicit `Close()`.
- `internal/tui/plugin_views.go` — reconnect state on `pluginViewMount`, the redial loop, overlay show/hide, fresh-connector rewire + state reset on success, teardown that cancels the loop.
- `internal/tui/app.go` — `handleGlobalKey` `modePluginView` branch: route Esc to exit while reconnecting; cancel the reconnect loop in `deactivatePluginView`.
- New `modal` overlay for the reconnecting screen (or reuse the error-modal style).
- No protocol change: the `{"type":"resize|focus|blur"}` envelopes and the binary ANSI stream are untouched.
