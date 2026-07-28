## Context

`agent.NeedsInputClear` (as of BUG-065) has three clear paths: (a) the
session's `LastUserInput()` advancing past the flag's baseline, (b) the
task's archive, and (c) `resumedOf` — `agent.ResumeActivityTick` reporting
`NeedsInputResumeTicks` (5) CONSECUTIVE ticks of Claude's "working" affordance
since the flag was raised.

`ResumeActivityTick` is deliberately unforgiving: `if !workingNow { return 0,
false }` resets the streak to zero on ANY tick without the working affordance
— no grace period, by design (BUG-065's own rationale: clearing too slowly is
safe, clearing a still-blocked agent is not). This is correct for its
intended case — a brief acknowledgment burst from a still-blocked agent that
re-parks at the same or a new prompt must not cross the threshold — but it
has a blind spot BUG-065 didn't examine: a worker that resolves its block and
finishes up in FEWER than 5 ticks does not re-park at a blocking prompt at
all. It goes idle. And going idle is exactly what makes `workingNow` false,
which resets the streak to zero — permanently, since a genuinely idle session
never shows the working affordance again. There is no way for `resumedOf` to
ever become true for this session again; the only remaining path is (a),
which requires an actual keystroke.

Live repro (Aaron dogfooding): the Details pane showed the session idle and
the task status `in_review` — i.e. the worker had already resolved whatever
raised the flag and moved on — but the rail still showed `(?)`. A no-op
keystroke (space) into the pane was the only thing that cleared it, via path
(a): any input, however meaningless in content, advances `LastUserInput()`
past the baseline.

## Goals / Non-Goals

- **Goal:** a flag raised on a session that goes on to resolve itself
  quickly — faster than `NeedsInputResumeTicks` consecutive ticks of visible
  work — and settle into a genuinely idle, non-blocking state clears without
  requiring a literal keystroke.
- **Goal:** do not weaken any existing clear-path guard. In particular, a
  session that is STILL genuinely blocked (still idle, still showing the
  signal that raised the flag) must never be cleared by this path — that
  would resurrect the exact false-clear risk BUG-034's `LastUserInput` split
  and BUG-061's sticky-carry-forward hardening both exist to prevent.
- **Non-goal:** replacing or loosening the BUG-061 sticky-carry-forward
  policy (no re-match required to STAY flagged). This change adds a new,
  narrowly-scoped way to CLEAR a flag — it does not change what keeps one
  raised.

## Decision: a fourth clear path gated on genuine raw-idle, not on content stability alone

The new clear path re-runs the SAME idle-gated detector that raises the flag
in the first place (`agent.DetectNeedsInputScreen`, the exact check
`detectNeedsInput`/the daemon's initial idle-gated pass already use) as a
NEGATIVE signal: if a flagged session is genuinely raw-idle (`Session.IsIdle`
— no new output for the idle threshold, not merely "not currently
generating") AND that check now returns false — the tail shows none of the
three needs-input signals — for `NeedsInputSettleTicks` (2) consecutive
ticks, the flag clears.

### Why gating on raw-idle (not just "signal absent") is what makes this safe against BUG-061

BUG-061 removed the sticky pass's OWN prior "re-match every tick to stay
flagged" policy because a session's tail can be PERMANENTLY flooded by
Claude's continuous blinking-cursor redraw, pushing the actual prompt content
out of the scanned window while the session is STILL genuinely parked — a
false negative on the signal check that must never be treated as a real
clear. Re-introducing "clear when the signal check misses" as a general
policy would resurrect exactly that hazard.

The discriminator that makes this new path safe is `Session.IsIdle()`: BUG-061's
flooding mechanism REQUIRES the session to keep producing bytes indefinitely
(the blinking-cursor redraw is itself the flooding source). A session that is
raw-idle has, by definition, stopped producing bytes for the idle threshold —
it cannot be mid-flood, because the thing that would be flooding it (ongoing
byte production) has, by the very definition of raw-idle, ceased. So a fresh
read of an idle session's tail is exactly as trustworthy as the read that
raised the flag in the first place (the SAME idle-gated check). This is why
`SettleTick` takes `idleNow` as a hard gate rather than trying to distinguish
"signal absent because resolved" from "signal absent because flooded" some
other way — raw-idle already rules the flooding case out structurally.

### Why a small threshold (2), not 5 or 8

`NeedsInputResumeTicks` (5) and `NeedsInputEscalationTicks` (8) both guard
against specific noise sources that don't apply here:

- `ResumeActivityTick`'s 5-tick threshold exists to make sure a brief
  acknowledgment burst (composing a short reply) doesn't masquerade as
  genuine sustained work. That risk is about a session that is STILL
  producing bytes (still working, still not idle) — irrelevant to
  `SettleTick`, which only progresses while the session has already gone
  fully idle.
- `EscalateParkedSelection`'s 8-tick threshold guards against isolated
  torn-reads/blink-off frames while a session is ACTIVELY, continuously
  producing redraw bytes (never idle). Once idle, that noise source is gone
  by construction (see above).

`NeedsInputSettleTicks` = 2 exists purely to survive an isolated torn read
(the same category BUG-060 named for the escalation counter's own grace
period), not to wait out an ongoing noise process — there isn't one once the
session is idle.

### Why NOT relax the sticky carry-forward's own re-match requirement instead

An alternative considered: teach the BUG-061 sticky carry-forward pass itself
to drop a flag when the CURRENT tick's signal check misses AND the session is
idle, rather than adding a fourth, separate `NeedsInputClear` condition. This
was rejected because it would conflate "stays flagged" (the sticky
carry-forward's sole job, unconditional per BUG-061) with "clears" (which
`NeedsInputClear` alone owns, and which BUG-063/BUG-067's stale-recandidacy
guards specifically reason about via the cleared-marker mechanism). Routing
the new signal through `NeedsInputClear` as `settledOf` — exactly parallel to
`resumedOf` — means it automatically inherits the BUG-063 stale-recandidacy
guard (a later re-candidacy at the same `lastInputOf` timestamp is
suppressed) for free, with zero new bookkeeping, exactly as BUG-065's design
already established for `resumedOf`.

### Why NOT simply loosen `ResumeActivityTick`'s reset policy instead

An alternative considered: give `ResumeActivityTick` a lower "settle" fallback
threshold if the session goes idle before reaching 5. Rejected because it
conflates two structurally different signals — "sustained active work" and
"has gone idle with the blocking signal now absent" are not the same
predicate and have different noise profiles (see above), so folding them into
one counter with one reset rule would either weaken the 5-tick
still-blocked-burst guard or fail to clear the quick-settle case. Two small,
independently-testable pure functions, composed as sibling `NeedsInputClear`
conditions, keep both guarantees intact and independently verifiable — the
same shape this subsystem already uses for `EscalateParkedSelection` vs.
`ResumeActivityTick`.

## Call-site changes

- `internal/agent/needsinput.go`: `NeedsInputClear` gains `settledOf
  func(string) bool`, checked alongside `resumedOf`. `SettleTick` and
  `NeedsInputSettleTicks` are new, standalone exports alongside
  `ResumeActivityTick`/`NeedsInputResumeTicks`.
- `internal/tui/app.go`: `detectNeedsInputSticky` gains a settle pass (mirrors
  the existing resumed-activity pass, reusing the already-idle-gated
  `agent.DetectNeedsInputScreen` check), backed by a new
  `App.needsInputSettle map[string]int` field.
- `internal/api/push.go`: `computeNeedsInput` gains the same pass; its
  signature grows a `prevSettle map[string]int` parameter and a 6th
  `newSettle` return value, backed by a new
  `idleWatcherState.needsInputSettle` field.

Neither caller's ENTRY detection signals change — the trailing-question
heuristic, the idle gate, the content-stability/escalation/resumed-activity
passes, and the existing three clear conditions are all unchanged. This is
purely an additional, independent way for an already-raised flag to be
resolved.
