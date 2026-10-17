## Context

The current Active fallback is unsafe for Hera roles: Tasks and agent views overwrite role dimensions, and fullscreen publishes wide dimensions. Existing stop and status tests also allow several plausible regressions.

## Goals / Non-Goals

**Goals:** Publish split coordinator/worker dimensions on every eligible frame regardless of tab, preserve explicit dimensions, distinguish repair routing, and assert the reviewed state transitions and timing.

**Non-Goals:** Web/macOS viewport publication and repair-status UI remain named follow-ups. Do not reap descendants during width repair. External restarts may leave a conservative in-memory anchor and cause one additional guarded repair; PID-scoped anchor redesign is deferred.

## Decisions

- Derive split sizes from current host width and content height, using the same rail and border arithmetic as Hera Draw. Fullscreen never increases a launch preference.
- A known Hera role with absent/invalid role geometry returns no preference, rather than Active. Non-Hera omitted dimensions use Active, including REST create and schedule run-now.
- Keep two-minute report expiry and minimum 5×20 dimensions as tested contracts. Explicit dimensions still win individually.
- Preserve PID-only stops and the existing possible five-second PTY drain after the three-second grace. Document helper orphaning alongside the full-stop leak invariant.
- Retain conservative anchors after external restarts; clearing before successful Start risks losing real drift.

## Risks / Trade-offs

- Multiple local viewers can disagree → each publishes split role geometry, never its full-width Active fallback.
- Launching while fullscreen uses narrower split defaults → returning to split avoids narrow-pane corruption; genuine later drift remains guarded.
