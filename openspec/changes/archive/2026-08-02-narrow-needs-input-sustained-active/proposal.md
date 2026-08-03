## Why

The Hera rail's needs-input `(?)` glyph is meant to mean "this agent is genuinely stuck right now, with no path forward without you." Aaron's own words: "(?) interrupts the user from whatever they are doing... I don't want to do that if there is no job to do. Further, if several (?) exist that the user has dismissed, then when one of them or another agent DOES need help it diminishes their likeliness of unblocking an agent that needs it." Ground-truth investigation of a live false positive (hera role `contrib-classifier`, a nested sub-orchestrator coordinator dual-bound to the same argus task as its parent-orchestrator worker hat) confirmed the role was genuinely, continuously active (mid-tool-call, 30k+ tokens streamed over 7+ minutes) while still showing `(?)`. The role's self-reported hera status was `working` on both of its bindings the whole time — not a stale `blocked` ladder value — so the false positive traces to the PTY-content needs-input scan (`RoleView.NeedsInput`) not clearing despite sustained activity, most plausibly because this role's bursty, narrated output repeatedly re-triggers the content classifier's "looks like a parked prompt" signature before `agent.ResumeActivityTick`'s zero-grace-period consecutive-tick counter can sustain the 5-tick threshold needed to clear it.

False positives are not neutral: they train the operator to dismiss `(?)` on sight, which degrades the signal for every other agent that is genuinely blocked.

## What Changes

- A role that is **sustained-active** (per-task, genuinely and continuously producing output — reusing the existing `agent.ResumeActivityTick` debounce, not a new threshold) SHALL NOT show the `(?)` needs-input glyph, regardless of the bound task's workflow status (`in_review`/`ready_to_close`) or a self-reported `blocked` hera role status left over from an earlier phase or a different hat on the same dual-bound task.
- This is threaded as a new per-task signal (`RoleView.SustainedActive`), computed once per tick and shared naturally across any roles bound to the same underlying argus task (including a dual-bound sub-coordinator's worker hat and coordinator hat), the same way `SessionIdle`/`SessionRunning`/`NeedsInput` are already threaded from `App` through `HeraPage`/`BuildModel`/`buildRoleView`.
- `RoleView.needsInputOwn()` (the sole source of the rail's `(?)` glyph, `ShowsNeedsInput`) is gated on this new signal: sustained-active suppresses BOTH of its existing OR'd sources (the content-scan flag and the self-reported `blocked` ladder value) uniformly.
- This deliberately narrows BUG-A's existing, documented invariant ("`(?)` admits any hera-managed role... regardless of task status") — an intentional sharpening, not a regression. A genuinely idle, parked-at-a-prompt, or unresolved-`blocked`-with-no-subsequent-activity role is unaffected and still shows `(?)` exactly as today.
- **Non-goal, explicitly deferred**: a separate daemon-bounce reliability bug was found during this investigation (`Daemon.SessionStatus` can transiently report a supervisor-still-alive session as dead during a daemon restart, incorrectly rolling a hera worker's task to `in_review`/`ready_to_close` via `RollHeraWorkerToReview`). It is documented as a gotcha (see Impact) but is NOT fixed by this change — it is materially bigger and higher-stakes (daemon/supervisor reattach reliability) than this display-layer fix and is not required to satisfy the behavior change above.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-view`: narrows the rail/plan-view/Details status-glyph precedence requirement so a role's needs-input `(?)` signal is suppressed while the role is sustained-active, regardless of task status or a self-reported `blocked` hera status from another hat on the same dual-bound task.

## Impact

- `internal/tui/app.go`: expose the existing per-task sustained-activity signal (`agent.ResumeActivityTick`'s `resumed` set, currently computed and used only inline within `detectNeedsInputSticky`) as a new tracked set fed to the Hera page each tick.
- `internal/tui/hera/page.go`: new `HeraPage.SetSustainedActive` setter, mirroring `SetNeedsInput`/`SetSessionIdle`/`SetSessionRunning`.
- `internal/tui/hera/model.go`: `BuildModel`/`buildRoleView` gain a fourth per-task map parameter; `RoleView` gains a `SustainedActive` field; `needsInputOwn()` is gated on it.
- Test call sites across `internal/tui/hera/*_test.go` updated for the new `BuildModel` parameter.
- `context/knowledge/gotchas/hera-view.md`: BUG-A entry updated to describe the sharpened invariant.
- `context/knowledge/gotchas/events.md`: note on the new consumer of `agent.ResumeActivityTick`.
- `context/knowledge/gotchas/daemon-rpc.md`: new entry documenting the daemon-bounce `SessionStatus` false-negative race as a known, unfixed follow-up.
- Possible (only if a test demonstrates non-convergence): a minimal grace-period softening scoped to this new consumer of `agent.ResumeActivityTick`, mirroring the existing `EscalateParkedSelection`/BUG-060 pattern, without touching `ResumeActivityTick`'s existing tuned semantics for BUG-065's coordinator-relay-answer use case.
