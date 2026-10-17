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

## Validation

Geometry and redundant-attach regressions failed before their fixes. All eight touched packages pass with `-race`; the completed TUI run takes 282.685s. The shared missing-method fixture initially conflicted with the new positive fallback test; a dedicated RPC peer fixes that conflict, and the full daemon-client suite passes (32.906s isolated, 39.419s in the final gate).

`make -k pre-pr` with all ambient Argus/backend session variables scrubbed passes build, vet, formatting and lint (zero issues). Its remaining failures are acknowledged pinned-Go advisories, TUI package timeout at 121.018s, terminal package timeout at 120.731s, and the unrelated bulk-delete wall-clock assertion under concurrent worker load. Terminal passes isolated with `-race -timeout 300s` in 116.020s. Bulk-delete had one timing failure among five repetitions while terminal was running, then passes alone in 2.782s. Neither affected test file is changed by this branch; the coordinator approved the load waiver conditioned on these isolated passes.

Combining the passing complete TUI/terminal profiles with the final repository profile gives filtered coverage 88.5% (36070/40744), above the unchanged 88% gate. Seven new or expanded TUI roots total 2.38s in `-race -json`; the selected group including older pane tests and race overhead takes 4.944s, so the increase from roughly 176s to 282s is not added test duration. Strict OpenSpec validation passes all 69 items.
