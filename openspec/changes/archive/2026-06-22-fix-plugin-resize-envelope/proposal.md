# Fix plugin-view resize envelope: pre-layout garbage size + no re-send

## Why

A freshly activated plugin view can be told its viewport is 13x8 (the tview Box default 15x10 minus border) because the resize envelope is computed from `pane.GetRect()` before tview's first layout pass — and from the WebSocket dial goroutine, racing the layout. Argus then never corrects the value: the only re-send trigger is a terminal-size change, so a garbage, lost, or raced initial envelope leaves the plugin rendering at the wrong size indefinitely. This shrank hera's worker PTYs to 20x21 in production (daemon.log "KickRerender ... cols=20 rows=21", 2026-06-01).

## What Changes

- Argus never sends a resize envelope computed from a pre-layout pane rect. The first envelope for a plugin view is sent only after the pane has actually been drawn, from its real post-layout rect.
- Argus reconciles the envelope on every draw: when the computed viewport differs from the last envelope sent on the active connection, it re-sends — not just on terminal resize.
- The viewport fallback (no layout yet / no pane) derives from the screen size minus fixed chrome instead of trusting the un-laid-out Box rect.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `plugin-views`: adds resize-envelope requirements — post-layout initial envelope, last-sent reconciliation on every draw, and re-send on re-activation.

## Impact

- `internal/tui/plugin_views.go` — viewport computation, activation dial path, reconciler.
- `internal/tui/app.go` — `afterDraw` runs the reconciler on every draw, not only on terminal resize.
- No protocol change: the `{"type":"resize","cols":N,"rows":M}` envelope and the `views.Connector` surface are untouched.
