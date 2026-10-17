## Context

Aaron chose PID-scoped anchors instead of accepting a conservative extra repair after external restarts. Split launch geometry and contractual report expiry/minimums are already implemented; descendant reaping remains on hold.

## Goals / Non-Goals

**Goals:** Retain BUG-078/BUG-081 same-process anchors while preventing stale anchors or cached attach decisions from affecting a replacement process.

**Non-Goals:** Change daemon APIs, persist anchors, or reap helpers. Web/macOS do not own this TUI bind state; their viewport publication and repair-status UI remain named follow-ups.

## Decisions

- Record the live SessionHandle PID alongside each task's observed anchor identity. Use no DB AgentPID lookup, since the persisted PID can lag an external restart.
- On a PID change, drop the anchor and attach cache before the predicate; a new process's own InitialPTYSize still decides whether genuine width drift needs repair.
- Capture PID with async probes and reject callbacks superseded by a later bind. Keep recordUnreconciledCommittedCols as the only width writer.
- Clear both maps only after successful start or task deletion. A failed Start must preserve both.

## Risks / Trade-offs

- PID reuse is possible over long runtimes → this contract uses the supplied live session identity, and existing successful-start/deletion cleanup still applies.
- An old RPC may complete after a new bind → compare the captured PID against current bind identity before modifying state.
