## Why

The Tasks view shows a gradient LAUNCH splash while a task is being prepared. Hera (Projects tab) panes show nothing useful in the same window: a blank pane while a spawned worker's session has not produced output, or the "Session not running" placeholder for a task that has not started. Most Hera launches (hera_spawn_worker, gater materialization, sub-coordinators, revive) originate in the daemon, so the TUI's in-process pending callback never fires for them.

## What Changes

- Hera coordinator and agent panes opt into a launch splash that reuses the Tasks view's banner renderer, with a status line chosen from task/session state: `PREPARING WORKTREE` (bound task status `pending`, no session) and `STARTING AGENT` (live session, no output ever written).
- The splash ends at the first byte of output, and gives up after 45s so a silent agent is never hidden.
- Precedence: closed-out banner > "Re-rendering…" > launch splash > placeholder/live render. Painted only inside the pane rect, no `screen.Sync()`, in split and fullscreen.

## Non-Goals

- Web SPA and macOS app have no launch splash for tasks today, so there is no Hera equivalent to port; parity gap is named here: neither surface renders a launch banner.
- No splash for plan nodes that are still `planned` (no task row yet) — they may wait indefinitely on blockers.
- No stage between worktree creation and DB insert: the task row (and rail entry) only exists after the worktree does, so `PREPARING WORKTREE` covers the pending-status window.
