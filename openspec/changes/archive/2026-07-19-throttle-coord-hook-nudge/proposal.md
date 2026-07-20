## Why

The `argus coord-hook` Stop hook re-evaluates a coordinator's context budget on every single turn, and by design (`coordinator-context-management`'s "nudges over budget" requirement) re-emits the identical "reach a safe seam and recycle" block decision on every subsequent turn for as long as `context_size` stays at or above budget. That's fine for a coordinator that's about to comply — but a coordinator that has a legitimate reason not to recycle right now (e.g. its task genuinely requires reading more context than the budget allows, and recycling would just re-accumulate to the same point and loop) gets the exact same nudge spammed on every turn forever, with no way to make progress without the noise. Throttling the nudge to fire only after another meaningful chunk of context growth keeps the safety signal alive without spamming a coordinator that has already seen and consciously deferred it.

## What Changes

- Add a new project config field `coordinator_nudge_increment` (default `50000`), alongside the existing `coordinator_context_budget`.
- Add a new persisted `task_meta` key under namespace `hera`: `last_nudged_context_size`, a scalar recording the `context_size` at which the over-budget nudge last fired.
- Change `runCoordHook`'s over-budget nudge gating: emit the block decision only when `context_size >= budget` AND (`last_nudged_context_size` is unset, OR `context_size >= last_nudged_context_size + coordinator_nudge_increment`). On emission, stamp `last_nudged_context_size = context_size`.
- Clear `last_nudged_context_size` once `context_size` drops back below budget (e.g. after a real recycle), so a fresh over-budget episode nudges immediately rather than waiting out a stale increment window.
- The existing `pending_recycle` gate is unchanged and remains an independent, additional condition — either gate can suppress the nudge.
- The 1.5x hard-stop escalation (`ForceRecycleCoordinator`) is explicitly untouched — it keeps firing every turn once past its threshold, regardless of the new increment gate, since it's a safety net rather than a nudge.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `coordinator-context-management`: the "Context-budget Stop hook stamps a live signal and nudges over budget" requirement's recurrence behavior changes from "recurs on every subsequent turn" to "recurs only after context_size grows by at least `coordinator_nudge_increment` past the last nudge, or immediately on a fresh over-budget episode following a drop back below budget."

## Impact

- `cmd/argus/coord_hook.go`: `runCoordHook` gains the increment-gate check; `coordHookEnv` gains two new DI seams (read/stamp `last_nudged_context_size`), mirroring the existing `ReadContextSize`/`StampContextSize` pattern; real implementations added alongside `stampContextSizeReal`/`readContextSizeReal`.
- `internal/db/hera.go`: new `HeraMetaKeyLastNudgedContextSize` constant.
- `internal/config/config.go`: new `CoordinatorNudgeIncrement` field, default `50000`, following `CoordinatorContextBudget`'s existing pattern (including its config.toml override key and defaulting logic).
- `cmd/argus/coord_hook_test.go`: `TestCoordHook_OverBudgetNudge_RecursThenStops` changes to assert increment-gated recurrence instead of every-turn recurrence; new test cases added for suppressed-within-window, fires-after-another-increment, and resets-after-drop-below-budget-then-re-crosses.
- No REST API, TUI, or macOS client changes — this is entirely internal to the headless Stop-hook mechanism and its config/db plumbing.
