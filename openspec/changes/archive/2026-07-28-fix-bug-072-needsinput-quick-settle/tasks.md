## 1. Regression tests (RED)

- [x] 1.1 Add `TestSettleTick` to `internal/agent/needsinput_test.go` pinning
  the step function: threshold-exact settlement, immediate reset when not
  idle, immediate reset when the signal is still present, sparse/isolated
  idle-and-clear ticks never accumulating past a reset.
- [x] 1.2 Add `NeedsInputClear` subtests for the new `settledOf` clear path:
  clears despite no user input and no sustained resumed activity; interacts
  correctly with the BUG-063 stale-recandidacy guard; a `settledOf` that
  never reports true never clears a still-blocked agent (regression guard).
- [x] 1.3 Update all existing `NeedsInputClear`/`computeNeedsInput` call sites
  (production and test) for the new trailing `settledOf`/`prevSettle`
  parameter and (for `computeNeedsInput`) the new 6th return value. One
  pre-existing daemon test (`TestDetectNeedsInputTick`'s BUG-061 subtest) had
  its fixture corrected: it modeled "flooding" as a session marked idle with
  the signal gone for two consecutive ticks — which is now, correctly, the
  BUG-072 scenario itself, not the true BUG-061 case (a session that never
  goes idle). Fixed by keeping the session out of the idle set for those
  ticks instead.

## 2. Implementation (GREEN)

- [x] 2.1 Add `agent.NeedsInputSettleTicks` and `agent.SettleTick` to
  `internal/agent/needsinput.go`.
- [x] 2.2 Extend `agent.NeedsInputClear` with the `settledOf func(string)
  bool` parameter, checked alongside `resumedOf`, reusing the existing
  `newCleared` marker mechanism.
- [x] 2.3 Wire a settle pass into `internal/api/push.go`'s
  `computeNeedsInput` (new `idleWatcherState.needsInputSettle` field,
  `prevSettle` parameter, `newSettle` return value) and
  `internal/tui/app.go`'s `detectNeedsInputSticky` (new
  `App.needsInputSettle` field).

## 3. End-to-end regression coverage

- [x] 3.1 `TestComputeNeedsInput_SettledActivityClears` /
  `TestDetectNeedsInputSticky_SettledActivityClears`: `lastInputOf` frozen
  and the working-affordance streak never reaches
  `NeedsInputResumeTicks`, but the session goes genuinely idle with the
  blocking signal no longer showing for `NeedsInputSettleTicks` consecutive
  ticks — the flag clears.
- [x] 3.2 `TestComputeNeedsInput_StillBlockedIdleDoesNotSettle` /
  `TestDetectNeedsInputSticky_StillBlockedIdleDoesNotSettle`: the session goes
  idle but the tail STILL shows the identical blocking signal — the flag
  stays set (this is the ordinary idle-gated re-detection case; must not be
  misinterpreted as settled).
- [x] 3.3 `TestComputeNeedsInput_NotIdleNeverSettles`: the session keeps
  producing output (never goes raw-idle) — the settle counter never advances,
  regression-guarding against conflating "signal absent" with "settled".

## 4. Docs

- [x] 4.1 Add a BUG-072 entry to `context/knowledge/gotchas/events.md`
  following the existing BUG-0XX style, and update the events.md row in
  `context/knowledge/index.md`.

## 5. Verification

- [x] 5.1 `make pre-pr` clean (vuln gate's stdlib-only findings confirmed
  pre-existing/advisory per CI's `continue-on-error` policy; the two
  `internal/agent` profile-env tests confirmed as the known hera-worker-
  sandbox env-contamination artifact, and one flaky PTY test per run
  confirmed as the documented macOS full-suite `-race` PTY-exhaustion flake
  — both pass cleanly in isolation).
- [x] 5.2 Archive this change (`openspec archive
  fix-bug-072-needsinput-quick-settle`) in the same PR before merge.
