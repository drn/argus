## Context

`RoleView.ShowsNeedsInput()` (`internal/tui/hera/model.go`) is the sole source of the rail/plan-view/Details `(?)` glyph. It reads `needsInputOwn()`:

```go
func (r *RoleView) needsInputOwn() bool {
	return r.NeedsInput || (r.HasStatus && r.Status == db.HeraStatusBlocked)
}
```

`r.NeedsInput` is a per-task content-scan flag (`App.detectNeedsInputSticky` → `agent.NeedsInputClear`, fed through `HeraPage.SetNeedsInput` → `BuildModel` → `buildRoleView`, keyed by the role's live binding's argus task ID). `r.Status == HeraStatusBlocked` is the role's OWN self-reported hera status ladder value (per role, not per task).

Ground-truth investigation (live DB + daemon.log + ux.log for hera role `contrib-classifier`, a nested sub-orchestrator coordinator dual-bound to the same argus task as its parent-orchestrator worker hat `contribution-classifier`) ruled out the working hypothesis that a stale self-reported `blocked` ladder value on one hat was the cause (both hats read `hera_role_status = working`, and the existing `autoClearBlockedHeraRoles`/`ClearBlockedRoleStatus` auto-clear pass is already keyed by shared task ID, not per-role, so it is already cross-hat-safe). The false positive instead traces to `r.NeedsInput`: `agent.ResumeActivityTick`'s sustained-activity clear signal (`resumedOf`, used only inside `agent.NeedsInputClear`) requires 5 CONSECUTIVE ticks of the "working" content affordance with a hard reset to zero on any single non-working tick — and this role's actual output (long narrated numbered findings, periodic decision points) plausibly interrupts that streak often enough that it never reaches 5, even during many minutes of genuine, substantial activity. `ux.log` for the exact task shows its content-classification flip-flopping between "busy" and "blocked on user prompt" within seconds, repeatedly, supporting this.

Separately (NOT in scope for this change — see Non-Goals), the investigation found that `ready_to_close`/`in_review` on this task's shared workflow status came from a daemon-bounce race (`Daemon.SessionStatus` transiently reporting a supervisor-still-alive session as dead right after a daemon restart, incorrectly firing `RollHeraWorkerToReview`). That is a daemon/supervisor reattach reliability bug, tracked as a documented follow-up, not fixed here.

## Goals / Non-Goals

**Goals:**

- A role that is sustained-active (per-task, debounced, reusing existing machinery) never shows `(?)`, regardless of the bound task's workflow status or a stale self-reported `blocked` value from any hat bound to the same task.
- A genuinely idle, parked-at-a-prompt, or unresolved-`blocked`-with-no-subsequent-activity role is unaffected — no regression on real blocking cases.
- Dual-bound (subcoord) cross-hat scoping is closed BY CONSTRUCTION: the new signal is computed once per TASK (not per role/binding), so any two roles sharing a live binding to the same task automatically share the same sustained-active reading — no separate "look at the other hat" logic is needed.
- Reuse `agent.ResumeActivityTick`'s existing tick machinery as the FIRST cut, unchanged. Only add a scoped grace-period softening if a test demonstrates the zero-grace reset genuinely fails to converge for bursty-but-real activity — and if so, scope it to this new consumer only, without touching `ResumeActivityTick`'s existing semantics (BUG-065's coordinator-relay-answer clear path has its own tuned meaning and its own callers).

**Non-Goals:**

- Fixing the daemon-bounce `Daemon.SessionStatus` false-negative race that causes `RollHeraWorkerToReview` to fire on a still-live session. Documented as a gotcha follow-up (`gotchas/daemon-rpc.md`), not fixed here — it is a daemon/supervisor reattach reliability concern, materially bigger and higher-stakes than this display-layer change, and is not required to satisfy the behavior change above (BUG-015's `coordStatusLabel` already renders task-status and role-status as independent, intentionally-decoupled axes; this change only touches the `(?)` glyph).
- Any change to `RollHeraWorkerToReview`, `ReviveHeraWorkerToInProgress`, `hera.ReviveRole`, or the worker-completion lifecycle in general.
- Any change to `agent.ResumeActivityTick`'s existing callers (`agent.NeedsInputClear`'s `resumedOf`, `autoClearBlockedHeraRoles`'s per-role blocked-status auto-clear) beyond, if needed, a scoped grace-period variant used ONLY by the new sustained-active signal.

## Decisions

### D1 — Compute sustained-active once per TASK, shared across dual-bound hats

The existing "resumed" boolean set inside `App.detectNeedsInputSticky` is already computed for every RUNNING task ID, independent of needs-input candidacy — this is exactly the per-task, already-debounced signal needed. It is currently a function-local variable (`resumed := make(map[string]bool)`, used only to build the `resumedOf` closure passed into `agent.NeedsInputClear`). This change exposes it as a new `App` field (mirroring `needsInputResume`/`needsInputSettle`) so the caller (the tick loop) can also feed it to `HeraPage.SetSustainedActive`, threaded through `BuildModel`/`buildRoleView` into `RoleView.SustainedActive` — keyed by the SAME live-binding argus task ID `NeedsInput`/`SessionIdle`/`SessionRunning` already use. Since a dual-bound sub-coordinator's two roles (worker hat + coordinator hat) share the SAME task ID, they automatically read the SAME `SustainedActive` value — no per-hat lookup or "check the other binding" logic required.

Alternative considered: compute sustained-active independently inside `buildRoleView` from `SessionRunning`/`SessionIdle` (i.e., reuse `IsActive()`'s instantaneous liveness check) instead of threading a new debounced set. Rejected — `IsActive()` has no consecutive-tick requirement, so a single-frame blip of activity would flap the glyph off and back on; the coordinator's own instruction (and the acceptance criteria) explicitly call for the SAME debounce pattern already proven for BUG-065, not a new one.

### D2 — Gate `needsInputOwn()`, not `ShowsNeedsInput()` or the rollup

`needsInputOwn()` is the single function both `ShowsNeedsInput()` (own-row glyph) and the `SubtreeNeedsInput` rollup's leaf computation ultimately depend on. Gating here (rather than gating `ShowsNeedsInput()` directly, or gating in `buildRoleView` before either OR'd clause is even read) keeps the change to one function, keeps `r.NeedsInput`/`r.Status` as the raw, ungated signals available for any other future consumer, and mirrors the shape of the existing OR — sustained-active is a THIRD, suppressing condition checked first:

```go
func (r *RoleView) needsInputOwn() bool {
	if r.SustainedActive {
		return false
	}
	return r.NeedsInput || (r.HasStatus && r.Status == db.HeraStatusBlocked)
}
```

Alternative considered: suppress upstream in `App`/`detectNeedsInputSticky` so `r.NeedsInput` itself never gets set while sustained-active, and separately suppress the blocked-ladder read in `buildRoleView`. Rejected — two separate suppression sites for what is conceptually ONE invariant ("(?) means genuinely stuck") is more surface area for the two to drift; `needsInputOwn()` is already the documented single choke point (BUG-018, "ShowsNeedsInput reads ONLY the role's own signal").

### D3 — First cut reuses `agent.ResumeActivityTick` unchanged; grace-period softening only if a test proves non-convergence

Per the coordinator's explicit direction: implement the plain reuse first, add a repro test simulating the bursty/narrated-output pattern observed in `ux.log` (an occasional non-"working" tick more often than once every `agent.NeedsInputResumeTicks` (5) ticks) against `agent.ResumeActivityTick`, and only add a scoped grace period (mirroring `EscalateParkedSelection`'s BUG-060 one-tick-grace pattern) if that test demonstrates the streak never reaches threshold. If added, it is a NEW, separately-named step function (not a modification of `ResumeActivityTick` itself) so BUG-065's existing coordinator-relay-answer callers keep their current, deliberately-strict semantics ("a single non-working tick resets the streak outright... the failure mode this guards against — clearing a still-stuck agent — is not [safe]").

## Risks / Trade-offs

- **[Risk]** Widening what "sustained-active" suppresses could, in theory, mask a role that flips from active back to genuinely blocked within the same tick window it was previously marked sustained-active, for a few ticks, until `SustainedActive` itself drops (mirrors the existing `IsActive()` staleness trade-off already accepted for the spinner/BUG-F). → **Mitigation:** `SustainedActive` is read fresh every tick from the same running-session content signal `IsActive()`/`resumedOf` already use — it drops the instant the session goes idle or stops producing, same as every other content-derived signal in this file; under-suppression for a tick or two is the accepted trade-off already made everywhere else in this codebase's needs-input family ("under-clearing... is safe, a false clear is not" — but here a stale-active read that goes stale within a tick is bounded to a single tick, not indefinite).
- **[Risk]** A genuinely-blocked role that ALSO happens to be actively burning tokens on an unrelated background sub-task (rare, but structurally possible for a coordinator with live workers) could have its own `(?)` suppressed by its bound task's sustained activity even though the role itself is the one that's stuck. → **Mitigation:** out of scope for this change — `SustainedActive` is intentionally task/session-scoped (matching `IsActive()`, `SessionRunning`, `SessionIdle` — all already session-scoped, not role-scoped), and a role's PTY session being genuinely busy is itself strong evidence the role is not the one blocked; this mirrors the product ask directly ("if there is no job to do" — an actively-producing PTY is doing SOME job).
- **[Risk]** The deferred daemon-bounce race (Non-Goals) continues to mis-stamp `in_review`/`ready_to_close` on an unlucky daemon restart. → **Mitigation:** documented precisely in `gotchas/daemon-rpc.md` with reproduction evidence (task ID, timestamps, code pointers) so a follow-up worker can pick it up without re-deriving ground truth; explicitly out of scope here per the coordinator's scope call.

## Migration Plan

No data migration. Purely a TUI display-logic change (new in-memory per-tick field + one gating function) plus, if triggered, a new pure step function alongside `agent.ResumeActivityTick`. No schema, config, or API changes. Rollback is a plain revert.

## Open Questions

None — scope and approach are settled per the coordinator's msg #4174.
