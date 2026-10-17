## Context

PR #1054 review identified a real anchor-clobber regression and mutation-resistant coverage gaps. The original sampled delays describe two newly created 80-column tasks rather than repeated repair of the same process: the previously repaired task skips at the next 98-column bind. Persisted reconciled spawn width alone does not explain a latency reduction.

The 2026-10-16 ux/supervisor traces distinguish task `1792191732555994000` (fresh start 16:02:12, PID 77784, 80 columns; repair 16:02:23–31) from task `1792192091641510000` (fresh start 16:08:11, PID 96294, 80 columns). The first task skips at 98 columns at 16:08:23. Four Hera spawn/materialization inputs and headless creation omit dimensions; CreateAndStart previously defaulted each to 80×24. This is evidence for preventing fresh-spawn drift, not a measured post-fix latency claim.

## Goals / Non-Goals

**Goals:** Eliminate the measured fresh-spawn 80→98-column repair, preserve outstanding anchors, keep layout publication SQL off the UI thread, and make tests distinguish the intended routing and timing.

**Non-Goals:** Reap task descendants during width repair or alter the existing PTY drain safety invariant. Publishing web/macOS viewport preferences and showing repair status remain the named follow-up; their fresh sessions use the same shared spawn defaults.

## Decisions

- Delete the per-task reconciled-width record and all its writers/readers. The actual session spawn width remains authoritative; unresolved anchors remain in memory and survive Skip and successful stop until successful new start.
- Publish the drawn main and Hera coordinator/worker dimensions as one timestamped local DB record, with coalesced background writes and a short expiry. Fresh CreateAndStart calls fill only omitted dimensions from the appropriate pane; no report or stale report falls back to 24×80. Explicit dimensions always win.
- Ordinary KickRerender remains non-escalating. Width-repair callers use an explicit bounded variant routed over a distinct KickWidthRerender RPC capability; session switches and revive retain their previous stop behavior. A missing method on older peers falls back to ordinary KickRerender; other errors propagate.
- Keep deferred status as “Re-render pending”; active stop/restart uses “Re-rendering…”. Do not imply active repair when the main view has no scheduled retry.
- Use slog for supervisor/daemon diagnostics. Keep descendant preservation explicit and report the existing possible 3s grace plus 5s PTY drain limit.

## Risks / Trade-offs

- A fresh spawn can race the first completed layout publication → log publication/use/fallback and publish after first draw; the ordinary guarded repair remains available.
- Multiple viewers can publish different layouts → the most recent local TUI report wins and expires; this is a launch preference, not a claim that existing history has been reconciled.
