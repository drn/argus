## Why

Native Hera coordinator/worker panes render visibly garbled content — duplicated footers, blank frames, and mid-render character interleaving — whenever a bound session's PTY was resized at least once during its real life and the pane is (re)bound from scratch (a fresh TUI relaunch, or simply navigating the rail away from a role and back). Reproduced offline against a real dogfood session log: a full-history replay-from-scratch into one fixed-size emulator bakes writes authored for an OLD height/width and writes authored for the NEW one into the same grid, unreconciled, because a raw byte replay has no resize events to replay — the exact "width-mismatched scrollback can only be repaired by kill+resume, not SIGWINCH" limitation already documented for the main agent view. The main agent view already carries a real fix for this (`agent.ShouldKickRerender` / `Runner.KickRerender`, wired via `App.maybeKickRerender`), but it was never wired into the Hera pane-binding path, and Hera panes are especially exposed: `bindPane` calls `ForceResyncPTY()` unconditionally on every single bind, so any session viewed repeatedly in Hera accumulates real size transitions in its history over time.

## What Changes

- Extend the existing size-drift kill+resume safety net (`agent.ShouldKickRerender`, `Runner.KickRerender`, the `RerenderMargin` threshold, and the redundant-attach cache pattern) to the Hera view's pane-binding path, covering BOTH the coordinator (HERA) pane and the worker/agent pane.
- `bindPane` (fresh bind) and `reconcileOne` (late-bind / dead-handle re-resolve) evaluate the same drift predicate the main agent view already uses (`initialCols` vs. the Hera pane's current width, gated on idle/needs-input/no-session-id) before attaching a session, and queue a kick when it fires — same decision function, new caller.
- A per-pane redundant-attach cache (mirroring `App.isRedundantAttach`) avoids re-evaluating/re-kicking on every rebind at an unchanged pane width.
- No change to the kick mechanism itself (`agent.ShouldKickRerender`, `Runner.KickRerender` are unmodified) — this is a new call site, not new gating logic.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: the "PTY size alignment on bind" requirement (area 6) currently only resizes a bound session's PTY to match the Hera pane; it gains a size-drift kick+resume path for the case a plain resize cannot repair (committed scrollback authored at a different size).

## Impact

- `internal/tui/hera/panes.go` (`bindPane`, `reconcileOne`): new drift check + kick call before/around the existing `ForceResyncPTY()` call.
- `internal/tui/hera/*` needs a path to the richer session capabilities `agentview.TerminalAdapter` doesn't expose (`InitialPTYSize()`, `IsIdle()`) and to `Runner.KickRerender` + the task/config the main agent view already has — likely a small resolver/callback the `App` wires into `HeraPage`, mirroring `SetSessionResolver`.
- No change to `internal/agent/rerender.go` (`ShouldKickRerender`, `RerenderMargin`) or `internal/agent/runner.go` (`KickRerender`) — both are reused as-is.
- `context/knowledge/gotchas/hera-view.md` and `gotchas/pty-terminal.md` gain an entry for this fix once implemented.
