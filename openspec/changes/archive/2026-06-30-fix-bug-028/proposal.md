# Fix BUG-028: Hera rail surfaces needs-input on coordinator-less headers

## Why

In the native Hera view a role's needs-input "(?)" is gated on the bound argus
task being `in_progress` — at both the App feed (`needsInputInProgress`) and the
model (`buildRoleView`). That gate is correct for WORKERS (BUG-023: a finished
worker idling at its final prompt must clear), but WRONG for COORDINATORS. A hera
coordinator routinely rolls its task to complete/in_review while its session
stays alive and keeps coordinating, and may itself block on a user prompt. Both
gates then strip its needs-input, so the coordinator's (usually collapsed) header
shows no "(?)" — the live-dogfood repro: the bug-bash coordinator's bound task is
`complete` while it is still running. The directive's contradiction warning was
precise: a blocked agent that is NOT `in_progress` is stripped by BOTH gates, so
an app.go-only change is insufficient.

A second, narrower gap: the collapsed-orchestrator header surfaces the rollup
ONLY through a coordinator role's status glyph. An orchestrator with no
coordinator role (e.g. nuked, BUG-022 Tier-2) renders no cue even when the rollup
is set — unlike the always-flat task list's `projectStatusIcon` aggregate.

Note: for an `in_progress` worker the per-role and with-coordinator rollup paths
already render "(?)" correctly (shipped with BUG-023, #772); that is not the gap.

## What Changes

- `buildRoleView` applies the in_progress gate to WORKER roles only. A live
  non-worker role (coordinator/freelance) surfaces needs-input regardless of task
  status — its "finished" condition is a session exit, which already drops it from
  the upstream sticky set, so there is no stale-marker hazard.
- The App's Hera-rail needs-input feed (`needsInputForHeraRail`) admits a task
  that is `in_progress` OR bound to a hera coordinator role (regardless of status).
  Coordinators are MANAGED, so this never affects the unmanaged attention-summary
  count (BUG-005). `needsInputInProgress` stays for the agent-view attention bar.
- The needs-input rollup is also stamped on the `OrchView` (`SubtreeNeedsInput`),
  and `drawOrchRow` surfaces it on a coordinator-less header when no coordinator
  glyph carries it.
- No new keybinding, no glyph vocabulary change, no detection change. BUG-023 is
  preserved: a finished WORKER still clears.

## Impact

- Affected spec: `hera-view` (the needs-input rollup requirement).
- Affected code: `internal/tui/hera/model.go` (worker-only in_progress gate in
  `buildRoleView`; `OrchView.SubtreeNeedsInput` + `rollupNeedsInput`),
  `internal/tui/hera/rail.go` (`drawOrchRow` coordinator-less header),
  `internal/tui/app.go` (`needsInputForHeraRail` feed admitting coordinators).
- Behavioural change scoped to Hera-rail needs-input surfacing; no data, schema,
  or key changes. BUG-023 (finished worker) and BUG-005 (summary box) preserved.
