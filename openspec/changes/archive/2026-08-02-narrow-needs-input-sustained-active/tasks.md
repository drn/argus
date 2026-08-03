## 1. Sustained-active signal (App)

- [x] 1.1 Expose the `resumed` set already computed inside `App.detectNeedsInputSticky` (currently a function-local closure feeding `agent.NeedsInputClear`'s `resumedOf`) as a new tracked field on `App`, mirroring `needsInputResume`/`needsInputSettle`.
- [x] 1.2 At the tick call site, feed the exposed set to `HeraPage.SetSustainedActive`.

## 2. Threading through the Hera model

- [x] 2.1 Add `HeraPage.SetSustainedActive(ids []string)` mirroring `SetNeedsInput`/`SetSessionIdle`/`SetSessionRunning`.
- [x] 2.2 Add a `sustainedActive map[string]bool` param to `BuildModel` and `buildRoleView`; wire `page.go`'s `doRefresh`/rebuild call site.
- [x] 2.3 Add `RoleView.SustainedActive bool`, populated in `buildRoleView` from the new map keyed by the role's live-binding argus task ID (same live-binding branch as `NeedsInput`/`SessionIdle`/`SessionRunning`).
- [x] 2.4 Update every existing `BuildModel(...)` call site across `internal/tui/hera/*_test.go` for the new fourth parameter (pass `nil` unless the test needs it).

## 3. Gate needs-input on sustained activity

- [x] 3.1 Update `RoleView.needsInputOwn()` to return `false` unconditionally when `SustainedActive` is true, before evaluating the existing `NeedsInput`/`blocked`-status OR.

## 4. Convergence test + optional grace-period softening

- [x] 4.1 Add a test against `agent.ResumeActivityTick` reproducing the bursty/narrated-output pattern from `ux.log` (an occasional non-"working" tick more often than once every `agent.NeedsInputResumeTicks` ticks) and assert whether the consecutive-tick streak ever reaches threshold.
- [x] 4.2 If 4.1 demonstrates non-convergence: add a new, separately-named step function (NOT a modification of `ResumeActivityTick`) with a minimal one-tick grace period mirroring `EscalateParkedSelection`/BUG-060, used only by the new sustained-active signal; wire it in place of `ResumeActivityTick` for that consumer only. If 4.1 converges fine, skip this step.

## 5. Tests for the behavior change

- [x] 5.1 Test: a role with `NeedsInput=true` and `SustainedActive=true` does not show `(?)` (`ShowsNeedsInput()` false).
- [x] 5.2 Test: a role with a self-reported `blocked` hera status and `SustainedActive=true` does not show `(?)`.
- [x] 5.3 Test: a dual-bound task (worker-hat role + coordinator-hat role sharing one live binding's task ID) where one role's hera status is stale `blocked` — with the shared task's `SustainedActive=true` — suppresses `(?)` on BOTH roles.
- [x] 5.4 Test: a genuinely idle or blocked role with `SustainedActive=false` still shows `(?)` exactly as before (no regression).
- [x] 5.5 Test: `SustainedActive=true` plus `in_review`/`ready_to_close` task status still suppresses `(?)` (task status was already irrelevant to `ShowsNeedsInput`, confirm it stays that way).
- [x] 5.6 Update/extend existing BUG-A tests (`TestBuildModel_LiveWorkerInReviewSurfacesNeedsInput`, `TestBUGA_Integration_LiveInReviewWorkerAtPromptSurfaces`, etc.) to pass `sustainedActive=nil`/`false` where they assert the PRE-existing behavior still holds unchanged, and add the new sharpened-invariant coverage alongside rather than replacing them.

## 6. Documentation

- [x] 6.1 Update `context/knowledge/gotchas/hera-view.md`'s BUG-A entry to describe the sharpened invariant (sustained-active suppresses needs-input regardless of task status or a stale blocked flag from another hat on a dual-bound task).
- [x] 6.2 Update `context/knowledge/gotchas/events.md` to note the new consumer of `agent.ResumeActivityTick`'s tick machinery (the Hera rail's `SustainedActive`, alongside the existing `resumedOf`/`heraBlockedResume` consumers).
- [x] 6.3 Add a new entry to `context/knowledge/gotchas/daemon-rpc.md` documenting the daemon-bounce `Daemon.SessionStatus` false-negative race found during this investigation (task 1785216680765732000, the 10:51:08.948 timestamp trail, `isSessionAlive`/`Daemon.SessionStatus` vs `reattachSupervised`) as a known, unfixed follow-up — explicitly out of scope for this change.

## 7. Verification

- [x] 7.1 Run `make pre-pr` clean.
- [x] 7.2 Archive this change (merge the delta spec into `openspec/specs/hera-view/spec.md`, move the change folder to `openspec/changes/archive/`) within the same PR before merge.
- [x] 7.3 Open the PR via `mcp__argus__iris_gh_pr_create`.
