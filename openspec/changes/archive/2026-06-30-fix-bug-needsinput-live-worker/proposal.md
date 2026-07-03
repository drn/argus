## Why

**BUG-A — a live Hera worker stuck in in_review does not surface the `(?)` needs-input indicator when it is genuinely awaiting input.**

Per the #707 invariant, a hera worker deliberately sits in `in_review` (status + `meta:hera.ready_to_close=true`, stamped by `RollHeraWorkerToReview`) while its session lingers alive for the coordinator to close it out — the binding stays LIVE; only status + meta change. If that still-alive worker then genuinely blocks on a prompt (permission prompt / AskUserQuestion chooser / free-text question), the Hera rail showed the review status icon instead of `(?)`, so the user could not tell the worker was blocked on them. Reproduced live.

Three layers, all on the TUI Hera-rail surface, suppressed the indicator:

1. **`needsInputForHeraRail` (app.go)** dropped any non-`in_progress` worker from the rail feed, so the task never entered the rail's needs-input map.
2. **`buildRoleView` gate (model.go)** allowed `(?)` for a worker ONLY while `in_progress` (`taskInProgress || (role.Kind != worker && rv.Live)`), suppressing a live `in_review` worker.
3. **`RoleStatusIcon` precedence (rolestatusicon.go)** ranked `ready_to_close` ABOVE `needs-input`, so even once flagged, the review glyph masked `(?)`.

The original worker `in_progress` gate (layers 1 + 2) was the pre-content-aware BUG-023 protection: a finished worker's stale done-summary marker once lingered in the log tail forever and would pin `(?)` on every ancestor. Post-BUG-032/034/035 the needs-input set is content-aware (a task is flagged only while it shows a CURRENT awaiting-input signal, and clears on user input or archive), so the blunt task-status gate is no longer needed to suppress stale markers — and it is actively wrong for a live worker genuinely at a prompt.

## What Changes

- **`buildRoleView` gate → `taskInProgress || rv.Live`.** This branch runs only under a live binding, so a live role of ANY kind (worker, coordinator, freelance) surfaces needs-input when it is in the content-aware set, regardless of task status. BUG-023 stays protected by liveness, not task status: a worker is "finished" when its SESSION EXITS, which ENDS its binding (`rv.Live` → false → this branch no longer runs).
- **`needsInputForHeraRail` admits any hera-bound task** (worker OR coordinator) regardless of status, not just coordinators — `in_progress || heraManaged`. `buildRoleView` re-gates each on its live binding, so an exited worker is still suppressed. The unmanaged attention-summary count is unaffected (it subtracts the managed set, keyed on the binding regardless of liveness).
- **`RoleStatusIcon`: needs-input now outranks `ready_to_close`.** A worker actively blocked on a prompt is NOT "ready to close" — the actionable `(?)` must win over the now-contradicted close-out stamp. Because needs-input is content-aware upstream, a worker merely idling at its done summary (no interactive affordance) is never flagged and still renders the review glyph.

The daemon-side `computeNeedsInput` and the TUI-side `detectNeedsInputSticky` already produce the same raw content-aware set with no task-status gate, so they remain in lockstep — no daemon change is required. The task-status gates that differ are TUI-rail-render-specific.

## Capabilities

- `idle-detection` — a LIVE hera role surfaces detected needs-input regardless of its bound task's workflow status; liveness (not task status) is the worker clear condition; an actively-blocked role outranks the `ready_to_close` close-out glyph.

## Out of scope

- The flat task-list `(?)` gate (`needsInputInProgress` / `computeRuntimeState`) stays strictly `in_progress` (BUG-006) — a finished task in the always-flat list correctly shows no `(?)`. Only the Hera rail (the orchestration tree, where liveness is meaningful) is loosened.
- Task status / reconcile / revive logic (`RollHeraWorkerToReview`, bounce reconcile, `transitionTaskOnExit`) is untouched — the fix assumes a live worker CAN legitimately be in `in_review` (#707) and makes `(?)` correct in that state. The status-stranding fix is a sibling change.
- Freelance-kind roles with a complete task are unchanged (no meta set names them; the existing live-non-worker branch already covered them where admitted).
