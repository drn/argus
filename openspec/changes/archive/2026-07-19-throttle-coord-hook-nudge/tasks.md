**Design doc:** `openspec/changes/throttle-coord-hook-nudge/design.md`

## 1. Tests

- [x] 1.1 In `internal/db/hera_test.go` (or nearest existing constants test), assert the new `HeraMetaKeyLastNudgedContextSize = "last_nudged_context_size"` constant exists with that exact value (mirrors any existing key-constant coverage).
- [x] 1.2 In `internal/config/config_test.go`, assert `DefaultConfig().Hera.CoordinatorNudgeIncrement == 50000`.
- [x] 1.3 In `cmd/argus/coord_hook_test.go`, extend `fakeCoordHookEnv` with fields/fakes for the two new seams (`lastNudged int`, `hadLastNudged bool`, `lastNudgedErr error`, `stampedLastNudgedSize int`, `stampLastNudgedCalled bool`, `stampLastNudgedErr error`, `nudgeIncrement int`, `nudgeIncrementErr error`) and wire them into `(*fakeCoordHookEnv).env()` as `ReadLastNudgedContextSize` / `StampLastNudgedContextSize` / `NudgeIncrement`, following the exact shape of the existing `ReadContextSize`/`StampContextSize`/`Budget` fakes.
- [x] 1.4 Replace `TestCoordHook_OverBudgetNudge_RecursThenStops`'s middle assertion (turn 2 at `contextSize: 260000` after turn 1 at `250000`, budget `200000`, increment default `50000`) so it now asserts turn 2 does NOT emit a nudge (only 10000 of the 50000 increment has elapsed) — this pins scenario "Nudge is suppressed within the same increment window".
- [x] 1.5 Add `TestCoordHook_Nudge_FiresOnFirstOverBudgetTurn`: `hadLastNudged=false`, `contextSize=250000`, `budget=200000` → nudge emitted, `StampLastNudgedContextSize` called with `250000` — pins scenario "Nudge fires on the first over-budget turn".
- [x] 1.6 Add `TestCoordHook_Nudge_SuppressedWithinIncrementWindow`: `hadLastNudged=true`, `lastNudged=250000`, `nudgeIncrement=50000`, `contextSize=260000` (10000 of growth, budget already crossed) → no nudge, `StampLastNudgedContextSize` NOT called — pins scenario "Nudge is suppressed within the same increment window" (standalone case, independent of the sequential test in 1.4).
- [x] 1.7 Add `TestCoordHook_Nudge_RepeatsAfterFullIncrement`: `hadLastNudged=true`, `lastNudged=250000`, `nudgeIncrement=50000`, `contextSize=300000` (exactly the increment) → nudge emitted, `StampLastNudgedContextSize` called with `300000` — pins scenario "Nudge repeats once context has grown by a full increment". Add a second case at `contextSize=299999` (one under) asserting no nudge, for the boundary.
- [x] 1.8 Add `TestCoordHook_Nudge_FiresImmediatelyOnFreshEpisode`: `hadLastNudged=true`, `lastNudged=300000` (stale, from a prior session), `nudgeIncrement=50000`, `contextSize=210000` (fresh session back over budget=200000, but still under the stale `lastNudged`) → nudge emitted, `StampLastNudgedContextSize` called with `210000` — pins scenario "Nudge fires immediately on a fresh over-budget episode". This is the regression test for the trap flagged in design.md D2 (omitting the `size >= lastNudged` guard would wrongly suppress this case).
- [x] 1.9 Add `TestCoordHook_Nudge_ReadLastNudgedError_StillBlocks` and `TestCoordHook_Nudge_ReadIncrementError_StillBlocks`: `lastNudgedErr`/`nudgeIncrementErr` set (any budget-exceeding size) → nudge still emitted (fail-open, mirrors `TestCoordHook_PendingRecycleAlready_ReadError_StillBlocks`).
- [x] 1.10 Confirm all new tests fail against the current (pre-implementation) `coord_hook.go` before writing any implementation code (Prove-It Pattern) — run `make test-pkg PKG=./cmd/argus/` and `make test-pkg PKG=./internal/config/` and `make test-pkg PKG=./internal/db/` and confirm the expected new failures.

## 2. Config field

**Depends on:** Stage 1

- [x] 2.1 In `internal/config/config.go`, add `CoordinatorNudgeIncrement int \`toml:"coordinator_nudge_increment"\`` to `HeraConfig`, directly below `CoordinatorContextBudget`, with a doc comment following that field's exact style (what it gates, default value, cross-reference to the coord-hook).
- [x] 2.2 In `DefaultConfig()`, add `CoordinatorNudgeIncrement: 50000` to the `Hera: HeraConfig{...}` literal.
- [x] 2.3 Run `make test-pkg PKG=./internal/config/` — confirm 1.2 passes.

## 3. task_meta key

**Depends on:** Stage 1

- [x] 3.1 In `internal/db/hera.go`, add `HeraMetaKeyLastNudgedContextSize = "last_nudged_context_size"` to the task-meta mirror-keys `const` block, with a doc comment following `HeraMetaKeyContextSize`'s style (namespace, what stamps it, that it's a scalar not history).
- [x] 3.2 Run `make test-pkg PKG=./internal/db/` — confirm 1.1 passes.

## 4. coord_hook.go gating logic

**Depends on:** Stage 2, Stage 3

- [x] 4.1 Add `ReadLastNudgedContextSize func(taskID string) (size int, ok bool, err error)`, `StampLastNudgedContextSize func(taskID string, size int) error`, and `NudgeIncrement func(taskID string) (int, error)` to the `coordHookEnv` struct, with doc comments mirroring `ReadContextSize`/`StampContextSize`/`Budget`.
- [x] 4.2 In `runCoordHook`, after the existing hard-stop check and before the `PendingRecycleAlready` check, add the increment gate per design.md D2: read `lastNudged, hadLastNudged, err := env.ReadLastNudgedContextSize(taskID)` (log-and-fall-back-to-`hadLastNudged=false` on error, mirroring the `PendingRecycleAlready` error-handling style) and `increment, err := env.NudgeIncrement(taskID)` (log-and-fall-back-to-`0` on error); if `hadLastNudged && size >= lastNudged && size < lastNudged+increment`, return with no decision (**the `size >= lastNudged` guard is required** — see design.md D2's fresh-episode trap and test 1.8).
- [x] 4.3 After the block decision is successfully emitted (existing `json.NewEncoder(out).Encode(dec)` call succeeds), call `env.StampLastNudgedContextSize(taskID, size)` and log-to-`errOut` on failure (same pattern as the existing `StampContextSize` error handling) — do NOT stamp when the nudge was suppressed by the increment gate or by `pending_recycle`.
- [x] 4.4 Update `runCoordHook`'s doc comment to describe the increment-gated recurrence instead of "recurs every turn."
- [x] 4.5 Add `realCoordHookEnv()` wiring for the three new fields, plus `readLastNudgedContextSizeReal`, `stampLastNudgedContextSizeReal` (reuse the existing GET/PUT `/api/tasks/{id}/meta` endpoint with the new key, mirroring `stampContextSizeReal`'s shape exactly), and `nudgeIncrementReal` (reuse the existing `GET /api/config` fetch `budgetReal` already performs, reading `cfg.Hera.CoordinatorNudgeIncrement`).
- [x] 4.6 Run `make test-pkg PKG=./cmd/argus/` — confirm every test from Stage 1 now passes.

## 5. Docs and gate

**Depends on:** Stage 4

- [x] 5.1 Add a gotcha bullet to `context/knowledge/gotchas/orchestration.md`'s "coordinator context management" entry noting the increment-gated nudge (new `last_nudged_context_size` scalar, `coordinator_nudge_increment` config default 50000, the `size >= lastNudged` fresh-episode guard) per this repo's CLAUDE.md documentation requirement.
- [x] 5.2 Update the README's Reference appendix config table (if `coordinator_context_budget` is documented there) to add `coordinator_nudge_increment` alongside it — factual config-table update, not a top-half marketing edit.
- [x] 5.3 Run `make pre-pr` and confirm it passes clean before opening/updating the PR.
- [x] 5.4 Archive this change per this repo's CLAUDE.md: run `openspec archive throttle-coord-hook-nudge` (or apply the merge-and-move by hand) so the base spec at `openspec/specs/coordinator-context-management/spec.md` reflects the new requirement text atomically with this PR, before merge.
