## Context

This supersedes acceptance of orphaned helpers during width repair. Ordinary switches/revive/recycle still use non-escalating agent-only stops.

## Goals / Non-Goals

**Goals:** Escalated repairs stop the original agent and descendants, including setsid children; cooperative exits preserve descendants; unrelated processes remain untouched; helper-held PTYs drain without the five-second fallback.

**Non-Goals:** Reap already unrelated double-fork daemons with no observable ancestry. Web/macOS repair-status and viewport publication remain named follow-ups; all daemon-backed width repairs share this stop behavior.

## Decisions

- Capture PID, parent PID and creation time using the existing gopsutil dependency before killing the root, while ancestry still exists. Walk ancestry rather than relying on process groups, so setsid does not evade selection.
- Only commit descendant cleanup after the original agent's os.Process SIGKILL succeeds. A cooperative exit or failed root signal cancels descendant signals.
- Send descendants SIGTERM, allow a short 250ms grace, then SIGKILL surviving identities. Recheck creation times before signals to avoid targeting reused PIDs. Enumeration and cleanup run off the caller thread.
- Log matched, stopped and failed counts with slog and uxlog. Inspection failures log degraded cleanup rather than broadening targets to unrelated processes.

## Risks / Trade-offs

- A descendant can exit or reparent during cleanup → retain captured creation-time identity independently of its later parent/group.
- Process enumeration can fail → still bound the agent stop, log the failed cleanup, retain the existing drain safety fallback.
- Dev servers started by an unresponsive agent may die → explicitly accepted for escalation only.

## Validation

- Red tests demonstrated surviving descendants before implementation. Final race regressions pass in 5.301s, including a real detached child holding the PTY, outsider survival, creation-time mismatch rejection, cooperative preservation and production cleanup counts. No production-length grace sleeps are used in tests.
- Final full agent/API/daemon/client race packages pass in 85.297s/73.613s/33.320s/29.605s; the daemon run includes the stream-surface digest. Reviewer independently verified Q1/Q2 clean.
- `make -k pre-pr` passes build, vet, formatting and lint; only explicitly waived pinned-Go advisories and DB/TUI package time budgets fail. Complete longer suites pass; filtered combined coverage is 88.5% (36172/40855). Strict OpenSpec validation passes 69 items. Gate details are recorded in the companion PID-anchor change.
