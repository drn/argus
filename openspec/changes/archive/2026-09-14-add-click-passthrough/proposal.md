## Why

**Argus never forwards a mouse CLICK to a live agent's PTY.** The default
terminal pane (`internal/tui/terminal/terminalpane.go`) already forwards the
scroll WHEEL to a live agent when it owns the alternate screen (BUG-026), but
`MouseHandler()` unconditionally consumes every `MouseLeftDown`/`MouseLeftClick`
for Argus's own focus-switching (`setFocus(tp)` + `OnClick()`), regardless of
whether the pane is already focused or whether a live agent is running.

This was discovered live: Claude Code renders some of its own interactive
affordances as PLAIN clickable text with no OSC-8 hyperlink at all — e.g. a
"Jump to bottom (click) ↓" line shown when scrolled up, meant to jump the
scrollback on a plain click. Inside Argus this does nothing, because the click
never reaches Claude Code's stdin as a mouse event — Argus eats it for its own
focus handling every time, even when the pane already had focus and there was
nothing left for the click to do on Argus's side.

## What Changes

- **A click on an unfocused pane still just focuses it** (unchanged): `setFocus`
  + `OnClick()`, click NOT forwarded.
- **A click on an already-focused pane with a live agent session now forwards
  the click to the agent's PTY** as a real SGR mouse press/release pair,
  instead of being a silent no-op. Not gated on alternate-screen/full-screen —
  applies whenever the session is live, in any view mode.
- **A click on an already-focused pane with no live session** (dead / replay /
  diff view) keeps calling `OnClick()` — unchanged, since there is nothing live
  to forward to.
- Only plain left-click press/release is forwarded. Right/middle click,
  drag-to-select, and double-click semantics are out of scope.
- The plugin-view terminal surface (`internal/tui/terminalpane/terminalpane.go`)
  gets the same click forwarding for parity — mirroring how it already carries
  the wheel-forwarding pattern (#681). That surface is always the focused
  widget while its page is shown, so its click forwarding is unconditional
  (mirrors its existing unconditional wheel forwarding), with no
  focus-vs-forward branch to make.
- Wheel forwarding (`forwardWheel`/`agentOwnsWheel`) is untouched — still
  gated on alternate-screen. This change is click-only.

## Capabilities

### Modified Capabilities

- `terminal-rendering`: the default terminal pane now forwards a plain left
  click to a live agent's PTY when the pane already has focus, instead of
  silently doing nothing; the plugin-view terminal surface unconditionally
  forwards clicks alongside its existing wheel forwarding.

## Impact

- **Modified code:**
  - `internal/tui/terminal/terminalpane.go` — `MouseHandler()` press/release
    branch, new `forwardClickPress`/`forwardClickRelease` (or equivalent)
    helpers, a pane-scoped flag remembering whether the in-flight click is
    being forwarded (so `MouseLeftUp` matches the decision `MouseLeftDown`
    made).
  - `internal/tui/terminalpane/terminalpane.go` — `MouseHandler()` gains a
    `MouseLeftDown`/`MouseLeftUp` branch mirroring the existing wheel case.
- **No new key, no new dependency, no schema change, no daemon RPC.**
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
