## Why

A single agent session has ONE PTY shared by multiple viewers: the TUI agent
pane, the web app (xterm.js), and `--remote` TUIs. A PTY has exactly one size,
and Claude Code (an Ink full-screen TUI on the alternate screen buffer) renders a
full frame baked to that one width — it does not reflow. So whenever viewers
disagree on size, whoever resizes last wins and Claude repaints.

Today three callers (TUI Draw loop, web `/resize`, daemon RPC) each call
`Session.Resize` independently with **no coordination and no `min()`** — pure
last-writer-wins. The visible symptom: switching between the task list, the agent
view, and the web app re-sizes the PTY and forces a full Claude repaint on nearly
every switch, even when nothing actually changed. On entry the TUI also calls
`ForceResyncPTY()` unconditionally, reposting a resize (and SIGWINCH) every time.

This is precisely the problem tmux solves with `window-size smallest`: size the
shared surface to the smallest *active* viewer, letterbox the larger ones, and
re-size only when the set of active viewers changes — not on every focus switch.

## What Changes

- **Viewer registry + `min()` chokepoint on `agent.Session`** (replaces
  last-writer-wins). The session gains a registry of *active* viewers keyed by a
  stable viewer ID, each carrying `(cols, rows)`. The effective PTY size is the
  per-dimension `min()` over all active viewers; the session applies it through
  the existing `pty.Setsize` path. New methods `SetViewerSize(id, cols, rows)` and
  `RemoveViewer(id)` are the ONLY way callers influence PTY size — direct
  `Resize(rows, cols)` from viewers is removed. With zero active viewers the
  session keeps its last applied size (never shrinks to zero).

- **"Active" means focused/visible, not merely connected** (per the design
  decision). A viewer that is connected but hidden releases its size claim and
  does not constrain the `min()`. This makes alt-tabbing away from the web app —
  without closing it — grow the TUI back to full screen.

- **TUI registers as a focused viewer on agent-view entry, releases on exit.**
  `onTaskSelect` registers `(paneCols, paneRows)` under a per-App viewer ID; the
  Draw loop updates the registered size on pane-size change (debounced) instead of
  calling `Resize`; `exitAgentView` calls `RemoveViewer`. The unconditional
  `ForceResyncPTY()` on entry is removed — registry-driven `min()` makes it
  redundant (re-entry at the same `min()` produces no resize, so the switch flicker
  is gone), and a genuinely stale PTY is corrected the moment the recomputed
  `min()` differs.

- **Web `/resize` registers a per-connection sized viewer; stream disconnect
  removes it.** `handleResize` calls `SetViewerSize(connID, cols, rows)`; the
  existing `defer sess.RemoveWriter(cw)` path (driven by `r.Context().Done()`)
  also calls `RemoveViewer(connID)`, so closing/navigating the web app reliably
  releases its claim and the PTY grows back for remaining viewers.

- **Web app reports visibility.** The SPA listens for `visibilitychange`: on
  hidden it releases its viewer claim (so a backgrounded tab stops constraining
  the size); on visible it re-asserts its current `(cols, rows)`. `pagehide` is a
  best-effort release on top of the connection-drop path. SPA shell change ⇒ bump
  `SW_VERSION`.

- **Rerender-kick still fires off real size changes.** The width-drift kill+restart
  that re-emits history at the new width is driven by an actual `min()` change, not
  by per-attach. The `isRedundantAttach`/`isRedundantResize` gates are subsumed by
  the registry (re-entry at unchanged `min()` no longer reaches a resize) and are
  simplified accordingly.

## Impact

- Affected specs: `agent-execution` (MODIFIED: PTY sizing and resize → viewer
  registry + `min()` chokepoint; ADDED: active-viewer registry), `agent-view`
  (ADDED: TUI registers/releases a focused viewer on agent-view enter/exit),
  `rest-api` (MODIFIED: resize registers a per-connection viewer, removed on
  disconnect), `mobile-pwa` (ADDED: visibility-driven viewer claim).
- Affected code: `internal/agent/session.go` (registry + min), `internal/agent/iface.go`
  (interface methods), `internal/agent/runner.go` + daemon/supervisor RPC
  (`internal/daemon/sessioncore.go`, `client/`) to proxy `SetViewerSize`/`RemoveViewer`
  in place of `Resize`, `internal/tui/app.go` (register on enter, release on exit,
  drop `ForceResyncPTY`), `internal/tui/terminal/terminalpane.go` (Draw posts
  viewer size, not Resize), `internal/api/handlers.go` (`/resize` → SetViewerSize;
  disconnect → RemoveViewer; visibility release endpoint), `internal/api/static/{index.html,sw.js}`
  (visibilitychange + SW_VERSION bump).
- **Breaking change, fine** (single user): the direct `Resize(rows,cols)` viewer
  path is removed in favor of the registry. No DB schema change. No new keybinding
  (help modal unchanged). No new MCP tool.
- Gotchas: add the `min()`-over-active-viewers invariant, the "active = focused,
  not connected" rule, and the "zero active viewers keeps last size" rule to
  `context/knowledge/gotchas/pty-terminal.md`.
- Quality gate stays `make pre-pr`; coverage floor 88% (target ≥95% on touched
  packages). New behavior is unit-testable on `internal/agent` (registry/min) plus
  API handler tests for register/remove-on-disconnect and a TUI smoke test that a
  same-`min()` re-entry posts no resize.
