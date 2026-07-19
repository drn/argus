## Context

`cmd/argus/coord_hook.go`'s `runCoordHook` is the pure core of the `argus coord-hook` Stop hook (registered globally in `~/.claude/settings.json`, self-gated on `ARGUS_TASK_ID` + coordinator role kind). On every Stop event it:

1. Reads the transcript's latest `cache_read_input_tokens` and unconditionally stamps `task_meta(hera, context_size)`.
2. Reads `config.Hera.CoordinatorContextBudget` (default 200000, `coordinator_context_budget` in `config.toml`).
3. If `size < budget`, returns (no-op).
4. If `size >= 1.5x budget` (hard-stop), calls `Daemon.ForceRecycleCoordinator` unconditionally — an immediate, idle-gate-free kill-and-restart. Out of scope for this change.
5. Otherwise, if `task_meta(hera, pending_recycle) != "true"`, emits a Claude Code `"block"` decision whose `Reason` re-injects the same "reach a safe seam and recycle" instruction.

Step 5 is stateless: `context_size` is read fresh every invocation and compared against the same fixed `budget`, with no memory of "have I already nudged in this range." This is explicit, spec'd behavior (`coordinator-context-management`, "nudges over budget" requirement, scenario "Over-budget nudge repeats every turn until resolved") and is pinned by `TestCoordHook_OverBudgetNudge_RecursThenStops`.

The problem: a coordinator that has a legitimate reason not to recycle (its task genuinely requires reading more context than the budget allows, and recycling now would just re-accumulate to the same point and loop) gets the byte-identical nudge on every single subsequent turn, forever, with no way to signal "I've seen this, stop repeating it" short of lying via `pending_recycle`.

## Goals / Non-Goals

**Goals:**

- Throttle the graceful over-budget nudge (step 5 above) so it re-fires only after `context_size` grows by a configurable increment (default 50000) past the size at which it last fired.
- Make a fresh over-budget episode (context dropped back below budget, e.g. after a real recycle, then crossed budget again) nudge immediately — the throttle should not carry a stale window across recycles.
- Keep the increment configurable via `config.toml`, following `coordinator_context_budget`'s existing pattern exactly (same `HeraConfig` struct, same override precedence: `DefaultConfig() < DB < config.toml`).

**Non-Goals:**

- The 1.5x hard-stop escalation (step 4) is not touched. It remains unconditional on every turn once crossed — it is a safety net for a wedged/unresponsive coordinator, not a nudge, and quieting it would defeat its purpose.
- The `pending_recycle` idempotency gate (step 5's existing condition) is not replaced or weakened. The new increment gate is an additional, independent condition — the nudge fires only when BOTH the increment condition and the `pending_recycle` condition allow it.
- No change to `readContextSizeReal`'s transcript-scanning logic, the REST/RPC transport, or any other coord-hook responsibility.

## Decisions

### D1: New task_meta scalar `last_nudged_context_size`, not a derived/computed value

Mirrors the existing `context_size` key exactly: a single scalar under `task_meta` namespace `hera`, overwritten (not appended) whenever the nudge fires. Read via a new `coordHookEnv.ReadLastNudgedContextSize(taskID string) (int, bool, error)` (the `bool` distinguishes "unset" from "zero" — a coordinator's first-ever nudge must fire even though there's no prior value to compare against, and 0 is a legitimate stamped value only in edge cases that should still be treated as "no throttle applied yet") and stamped via `StampLastNudgedContextSize(taskID string, size int) error`, exactly mirroring `ReadContextSize`/`StampContextSize`'s DI shape. Real implementations added alongside `stampContextSizeReal`/reuse the same GET/PUT `/api/tasks/{id}/meta` endpoint `context_size` already uses (just a different key) — no new REST endpoint needed.

**Alternative considered:** compute the "last nudged size" implicitder from `context_size`'s own history. Rejected — `context_size` is explicitly documented as "a single scalar, not a time series" (`internal/db/hera.go` `HeraMetaKeyContextSize` doc comment); there is no history to derive from, and inventing one would require a schema change (a real time-series table) for a problem a second scalar solves directly.

### D2: Gating logic lives inline in `runCoordHook`, not a new helper type

The existing function already reads `size`/`budget` and branches on `pending_recycle`; the new check is one more independent boolean ANDed into the existing "should we emit the block decision" condition:

```go
if size < budget {
    return
}
if size*2 >= budget*3 {
    // hard-stop, unchanged
}
lastNudged, hadLastNudged, err := env.ReadLastNudgedContextSize(taskID)
// error → fall back to "no throttle applied yet" (hadLastNudged=false), matching
// the existing PendingRecycleAlready error-fallback precedent: a read failure
// must never silently suppress the nudge.
increment, err := env.NudgeIncrement(taskID)
// error → treat as 0 (no throttle), same fail-open precedent as above.
if hadLastNudged && size >= lastNudged && size < lastNudged+increment {
    return
}
if pending, err := env.PendingRecycleAlready(taskID); ... { return }
// emit block decision (unchanged)
if err := env.StampLastNudgedContextSize(taskID, size); err != nil { ... }
```

The reset-on-drop-below-budget requirement (Goal 2) needs no explicit "clear" call, but the throttle condition MUST include `size >= lastNudged` as a precondition, not just `size < lastNudged+increment` — omitting it is a trap: since `context_size` only ever grows within a session and resets low on recycle (a fresh session, fresh transcript), the one case where `size < lastNudged` is exactly a fresh episode following a recycle, where `lastNudged` is a stale value from the *previous* session. Without the `size >= lastNudged` guard, `size < lastNudged+increment` is trivially true whenever `lastNudged` is stale-and-larger than the new `size`, which would wrongly suppress the fresh episode's first nudge — precisely the case Goal 2 requires to fire immediately. With the guard, `size >= lastNudged` is false in a fresh episode (current size hasn't caught up to the old session's last-nudged value yet), so the whole AND short-circuits to false, the `return` is skipped, and the nudge fires — no explicit clear/reset call or "did we recycle" signal needed, since monotonic-growth-within-a-session plus the reset-on-recycle invariant (`context_size`'s existing documented semantics) is exactly the signal.

**Alternative considered:** explicitly clear `last_nudged_context_size` as part of the recycle flow (`internal/hera/recycle.go`), the mirror-opposite of how `pending_recycle` is cleared post-recycle. Rejected — it would require the recycle path (which doesn't currently touch `context_size`-adjacent keys at all) to learn about a coord-hook-only implementation detail, adding a cross-package coupling for a case the `size >= lastNudged` comparison already handles for free using only data already in hand.

### D3: New config field `CoordinatorNudgeIncrement`, default 50000

Added to `HeraConfig` right next to `CoordinatorContextBudget`, same `toml:"coordinator_nudge_increment"` tag pattern, same `DefaultConfig()` initialization site (line ~332), same `config.toml` override precedence (`DefaultConfig() < DB < config.toml`, `FileLoader.Apply`'s `toml.Decode` onto a cloned base — a scalar int field overlays cleanly with no partial-zeroing risk, unlike the map fields the config gotchas file warns about). Read in `runCoordHook` via a new `coordHookEnv.NudgeIncrement(taskID string) (int, error)` seam mirroring `Budget`'s shape exactly (including the same "taskID accepted but currently unused, in case this becomes per-project" rationale `budgetReal` already documents); `nudgeIncrementReal` reads `cfg.Hera.CoordinatorNudgeIncrement` off the same `GET /api/config` response `budgetReal` already fetches — no new REST endpoint.

**Alternative considered:** hardcode 50000 as an unexported constant. Rejected per explicit user preference (configurable, 50000 default) — matches this codebase's existing precedent of making the budget itself tunable rather than baking in a number future users may need to tune per-project.

## Risks / Trade-offs

- **[Risk]** A coordinator whose context genuinely oscillates right around a `budget`/`budget+increment` boundary turn-to-turn (unlikely in practice — `cache_read_input_tokens` is monotonically non-decreasing within a session absent a recycle) could see slightly uneven nudge timing. → **Mitigation:** none needed; `cache_read_input_tokens` only grows or resets to near-zero on recycle, never oscillates mid-session, so this is not a realistic failure mode.
- **[Risk]** `size >= lastNudged` as the "fresh episode" signal (D2) is an inference from monotonicity, not an explicit recycle event. If some future change made `context_size` non-monotonic without recycling, the throttle could misbehave. → **Mitigation:** `context_size`'s doc comment already codifies monotonic-within-session, non-time-series semantics; this design leans on an existing invariant rather than introducing a new one, and the risk is called out in D2 for the next person touching this code.
- **[Trade-off]** Two additional fields on `coordHookEnv` (four new methods total: `ReadLastNudgedContextSize`, `StampLastNudgedContextSize`, `NudgeIncrement`) grow the DI surface further. Accepted — the existing seam is already this granular (one method per concern), and splitting keeps each real implementation and each test fake single-purpose, consistent with the file's existing style.

## Migration Plan

No data migration — `last_nudged_context_size` is simply absent (never written) for existing coordinator tasks until their next over-budget nudge, at which point `ReadLastNudgedContextSize` returns `hadLastNudged=false` and the nudge fires immediately (same as today's first-nudge behavior). `coordinator_nudge_increment` defaults to 50000 for every existing config with no `config.toml` entry — no override needed unless a user wants a different cadence.

## Open Questions

None — design confirmed with the user (50K default, configurable) before this doc was written.
