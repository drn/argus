## Why

When a plugin view (e.g. hera-view) is focused, argus is supposed to hand the keyboard to it, but the pane's key encoder silently drops arrows and every modifier combo (Ctrl/Shift/Alt-arrow, function keys) before they reach the plugin's WebSocket — so hera can't bind Cmd/Ctrl-arrow to move focus across its panes, and the bottom bar keeps showing argus hints whose keys are being surrendered. This change formalizes a "who has the ball" model: a focused plugin owns the keyboard, argus forwards every key faithfully, and the chrome reflects who is in control.

## What Changes

- Add a single shared key encoder (`internal/tui/keyenc`) that maps a `tcell.EventKey` to standard xterm byte sequences, including modified arrows via the `CSI 1;<mod><final>` form (e.g. `Ctrl+Right` → `\x1b[1;5C`, `Ctrl+Alt+Right` → `\x1b[1;7C`, the form iTerm2 emits for Cmd+arrow). Route all three current encoders through it: `terminalpane.eventBytes`, `streampane.eventBytes`, and `app.go:tcellKeyToBytes`. Existing sequences stay byte-identical.
- **Full surrender** while a plugin view has the ball: argus stops intercepting Esc and forwards every key to the plugin (Esc, Ctrl+Q, Ctrl+C, modified arrows, `?`). Argus reserves nothing for its own navigation. **BREAKING** for the current `modePluginView` behavior (Esc no longer exits the view; Ctrl+C no longer quits argus while a plugin is focused).
- Add a plugin → argus `{"type":"release"}` control envelope: the plugin hands control back and argus tears down the view.
- Add a **fast double-Ctrl+Q failsafe**: argus forwards single Ctrl+Q to the plugin, but two presses within ~400 ms force-return to argus regardless of plugin cooperation.
- Add a plugin → argus `{"type":"hotkeys",...}` dictionary (items flagged `bar:true/false`) and a `{"type":"help"}` request. Argus renders the `bar:true` subset in a context-sensitive bottom bar (with a reserved, non-overridable "return to argus" hint) and the full dictionary in argus's `?` help overlay when the plugin asks for it.
- Extend the views WebSocket connector to deliver plugin → argus control frames (today they are dropped) and dispatch `release` / `hotkeys` / `help`.
- Update `docs/plugins.md` and `context/knowledge/gotchas/` for the new contract.

## Capabilities

### New Capabilities

- `plugin-views`: full-screen plugin views — key surrender while focused, the plugin → argus control envelopes (`release`, `hotkeys`, `help`), the double-Ctrl+Q failsafe, the context-sensitive bottom bar, and the shared key-encoding contract for forwarded keystrokes.

### Modified Capabilities

<!-- None. No base specs exist yet (OpenSpec was just initialized); plugin-views is introduced fresh by this change. -->

## Impact

- **Code:** new `internal/tui/keyenc` package; `internal/tui/terminalpane`, `internal/tui/streampane`, `internal/tui/app.go` (encoder call sites + `handleGlobalKey` plugin-view branch + Ctrl+Q failsafe state); `internal/tui/plugin_views.go` (control-frame dispatch, hotkey-dictionary storage); `internal/tui/views/connector.go` (plugin → argus control-frame callback); `internal/tui/widget/statusbar.go` (plugin-view state); argus's help modal (render plugin dictionary).
- **Protocol:** additive plugin → argus envelopes (`release`, `hotkeys`, `help`) on the existing plugin-view WebSocket. No schema or DB migration; `plugin_views` table unchanged.
- **Plugins:** hera-view is the only consumer and will implement `release` / `hotkeys` / `help`. No backwards-compat burden.
- **Docs:** `docs/plugins.md`, `context/knowledge/gotchas/`.
