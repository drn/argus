## 1. Regression tests (RED)

- [x] 1.1 Add `TestResumeActivityTick` to `internal/agent/needsinput_test.go`
  pinning the step function: threshold-exact escalation, immediate reset on a
  single miss (no grace period, unlike `EscalateParkedSelection`), and
  sparse/isolated working ticks never accumulating.
- [x] 1.2 Add `NeedsInputClear` subtests for the new `resumedOf` clear path:
  clears despite no user input and a stale tail; interacts correctly with the
  BUG-063 stale-recandidacy guard; a `resumedOf` that never reports true never
  clears a still-parked agent (regression guard).
- [x] 1.3 Update all existing `NeedsInputClear`/`computeNeedsInput` call sites
  (production and test) for the new trailing `resumedOf`/`prevResume`
  parameter and (for `computeNeedsInput`) the new 5th return value.

## 2. Implementation (GREEN)

- [x] 2.1 Add `agent.NeedsInputResumeTicks` and `agent.ResumeActivityTick` to
  `internal/agent/needsinput.go`.
- [x] 2.2 Extend `agent.NeedsInputClear` with the `resumedOf func(string) bool`
  parameter, checked after the user-input clear and before the ordinary
  baseline path, reusing the existing `newCleared` marker mechanism.
- [x] 2.3 Wire a resumed-activity pass into `internal/api/push.go`'s
  `computeNeedsInput` (new `idleWatcherState.needsInputResume` field,
  `prevResume` parameter, `newResume` return value) and
  `internal/tui/app.go`'s `detectNeedsInputSticky` (new
  `App.needsInputResume` field).

## 3. End-to-end regression coverage

- [x] 3.1 `TestComputeNeedsInput_ResumedActivityClears` /
  `TestDetectNeedsInputSticky_ResumedActivityClears`: `lastInputOf` frozen for
  the whole scenario (modeling `WriteInputSystem`-only delivery) while the
  tail sustains the working affordance for `NeedsInputResumeTicks`
  consecutive ticks — the flag clears.
- [x] 3.2 `..._ResumedActivityBriefBurstDoesNotClear` siblings: the identical
  setup one tick short of the threshold, then reverting to the identical
  blocking prompt — the flag stays set (BUG-034 regression guard).

## 4. Docs

- [x] 4.1 Add a BUG-065 entry to `context/knowledge/gotchas/events.md`
  following the existing BUG-0XX style, and update the events.md row in
  `context/knowledge/index.md`.

## 5. Verification

- [x] 5.1 `make pre-pr` clean (vuln gate's stdlib-only findings confirmed
  pre-existing on a clean master checkout, matching CI's continue-on-error
  policy; `internal/agent`'s two `TestBuildCmd_ProfileEnv_*` tests confirmed
  as the known hera-worker-sandbox env-contamination artifact, pass cleanly
  with the sandbox's own `ARGUS_*` vars excluded).
- [x] 5.2 Archive this change (`openspec archive
  fix-bug-065-needsinput-resumed-activity` or the manual merge-and-move
  fallback) in the same PR before merge.
