## Why

Native Hera does not trap `Ctrl+Z`. The raw `0x1A` byte falls through to the
focused pane's PTY, and the OS delivers `SIGTSTP` to the agent process — which
suspends it. The operator is then stuck: the native Hera view's `Enter` only
reattaches a *dead* session (no live session in the runner), but a suspended
process is *stopped*, not dead — the runner still holds a live session for it —
so `Enter` can never revive it from the Hera view (the operator has to fall back
to the Tasks tab).

The external Hera plugin never had this footgun because it bound `^Z` to
fullscreen-pane, which *consumed* the keystroke before it could reach the PTY.
Native dropped that binding when it omitted the plugin's fullscreen feature, and
that silent omission created the suspend footgun.

## What Changes

- **Trap `Ctrl+Z` in the Hera view and bind it to fullscreen-pane** (plugin
  parity). When a content pane (coordinator or agent/details) is focused, `^Z`
  toggles a fullscreen mode in which the rail stays visible and only the focused
  pane fills the rest of the width. The focus ladder is fullscreen-aware:
  advancing keeps fullscreen, retreating from the coordinator pane to the rail
  exits fullscreen. `^Z` is always consumed (it is a no-op while the rail is
  focused), so `0x1A` can never again reach a pane PTY and suspend an agent.
  This both restores the feature and closes the footgun.
- **Revive a STOPPED (suspended) worker pane from the Hera view, not just a dead
  one.** `Enter` on a live worker/freelance role now triggers an in-place
  stop-and-resume via the runner's `KickRerender` (NOT the TUI-side
  `pendingRerenderRestart`, which only restarts while the operator is viewing the
  agent tab and would otherwise settle the worker at `InReview` from the Hera
  tab): if the worker's session is idle and not blocked on a prompt — the
  signature of a suspended/stuck agent — it is restarted in place and resumed
  losslessly via `--session-id`. A
  busy worker, or one parked at a user prompt, is left untouched (pure
  navigation). Dead sessions still restart via `startSession` as before. A live
  coordinator is navigate-only (it is operator-interactive) — only its dead
  state triggers a restart.

## Capabilities

### Modified Capabilities

- `hera-view`: Add the `Ctrl+Z` fullscreen-pane rail binding and the
  fullscreen-aware focus ladder; extend `Enter` reattach to revive a stopped
  (suspended) worker session, not only a dead one.

## Impact

- **Modified code:**
  - `internal/tui/hera/focus.go` — fullscreen state on `FocusMachine`
    (`ToggleFullscreen`, `Fullscreen`), auto-cleared whenever focus lands on the
    rail (Retreat/ToRail/rebalance).
  - `internal/tui/hera/page.go` — trap `Ctrl+Z` → `ToggleFullscreen`; render the
    fullscreen layout (rail + single focused pane) in `Draw`; broaden the `Enter`
    reattach gate so a live worker/freelance role fires `OnReattach`.
  - `internal/tui/heraactions.go` — `heraReattach` now branches dead-vs-live:
    dead → `startSession`; live worker → idle-gated in-place stop+resume.
  - `internal/tui/modal/help.go` + `help_test.go` — add the `^Z` (fullscreen)
    rail binding to the help overlay (CLAUDE.md keybinding rule).
  - `README.md` — Reference appendix keybinding table.
  - `context/knowledge/gotchas/hera-view.md` — the suspend footgun + the
    stopped-vs-dead distinction.
- **No new dependencies, no schema change, no daemon RPC.** Reuses existing
  in-process-runner / TUI primitives only.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
