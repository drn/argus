## Context

First TUI draw is fast. The delay is an asynchronous width-drift repair: busy deferral, ten-second retry, then a slow SIGTERM exit. Width anchors currently live only in App memory.

## Goals / Non-Goals

**Goals:**

- Avoid repeated kicks at a locally reconciled pane width across TUI launches.
- Retain BUG-078/BUG-081 unresolved-anchor semantics and #1053 transcript guards.
- Bound an uncooperative agent-only stop and expose repair progress.

**Non-Goals:**

- Web and macOS width memory and repair indicators are a named follow-up: cross-client width-reconciliation parity. Remote TUI retains its current width behavior; no REST contract changes.
- Change the existing busy/prompt safety gates or add screen.Sync.

## Decisions

- Store a reconciled width separately from the unresolved committed anchor in local task_meta, scoped to SessionID and agent PID (a resume keeps SessionID but changes the process). Load before evaluating a kick; hydrate and persist the anchor through recordUnreconciledCommittedCols. A reconciled width replaces the spawn-width reference only for that process; unresolved drift remains an independent trigger. A successful stop alone is not reconciliation: record the resumed width only after start succeeds. Conversation or process changes invalidate persisted state.
- A dedicated rerender stop scope gets a named three-second SIGTERM grace followed by PID-only SIGKILL. Cancel escalation on process exit, not on PTY drain completion. Existing transcript guards run before kicks; ordinary agent-only stops retain their behavior. Preserve descendants deliberately: a width repair resumes the same task, whose dev servers must survive. Full teardown continues using StopTree to reap the group, including MCP children. Use an injectable interval in tests.
- Render a short status on the pane border while repair is pending. Keep all terminal content visible so deferred prompt input remains readable. Clear status on resolution, failed restart, and rebinding.
- Route the new stop scope over an explicit internal StopRerender RPC verb on both hops. Older peers reject the verb; sending an unknown scope to their StopSession would instead fall through to a full tree stop. Log a missing-method rejection and retry with the existing non-escalating agent-only stop; other errors propagate. Protocol v10 and supervisor stream v9 identify this capability.

## Risks / Trade-offs

- Multiple TUI viewers can overwrite local reconciliation metadata → preserve unresolved anchors, scope records to SessionID, and retain existing cross-view width predicates.
- Forced exit can interrupt final flushing → allow three seconds after SIGTERM and preserve #1053's pre-stop transcript check.
- PID-only repair preserves helper processes as well as dev servers → this is the existing resize-bounce contract; full task teardown retains process-group cleanup. Test that escalation preserves descendants rather than weakening that contract.
