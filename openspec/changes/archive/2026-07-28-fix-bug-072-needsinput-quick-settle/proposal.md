## Why

**BUG-072 — a hera role's needs-input `(?)` rail indicator gets permanently
stuck even after the underlying task has already progressed to `in_review`
and gone idle, with no further action actually needed.** Confirmed live by
Aaron dogfooding: the Details pane already showed the session idle and the
task status `in_review`, yet the rail still showed `(?)` for that role.
Focusing the role's pane and pressing a key (e.g. space) in the PTY made the
spinner run briefly, then the rail updated and moved to review — the flag
only cleared in response to a live keystroke reaching the PTY, not in
response to the task's own transition to `in_review` / idle.

Root cause: `agent.NeedsInputClear` has exactly three clear paths — (a) the
user's `LastUserInput()` advancing past the flag's baseline, (b) the task
being archived, (c) `agent.ResumeActivityTick`'s sustained-working-streak
clear (BUG-065). None of the three fires for this scenario. (a) never fires
because nobody typed into the session. (b) never fires because the task
isn't archived. (c) can ONLY fire while the session is actively showing
Claude's "working" affordance for `NeedsInputResumeTicks` (5) CONSECUTIVE
ticks — `agent.ResumeActivityTick` resets its streak to zero the INSTANT
`workingNow` is false, which is exactly what happens the moment the session
goes idle. A worker that resolves its own block and wraps up in FEWER than 5
ticks — a quick "I'll proceed" completion rather than a multi-second visible
burst of tool calls — settles into idle before the streak can ever reach
threshold, and once idle `workingNow` stays false forever, so the streak can
never accumulate again. The flag is then stuck until a literal keystroke
(even a no-op one) advances `LastUserInput()` past baseline via path (a) —
exactly the repro.

This is a genuinely new gap, distinct from all 15 prior BUG-0xx fixes in this
subsystem (context/knowledge/gotchas/events.md): BUG-065's own design
explicitly accepts that a BRIEF working burst that RE-PARKS at a blocking
prompt must not clear the flag (the risk that motivated its no-grace-period,
reset-to-zero-on-any-miss policy) — but it never considered the sibling case
where the brief burst is followed not by a return to blocking, but by genuine
resolution (idle, no signal, task moved to `in_review`). Neither BUG-034 (user
input) nor BUG-065 (sustained work) has a path for "resolved quickly and is
now demonstrably at rest."

## What Changes

- **`agent.NeedsInputClear` gains a fourth clear condition, `settledOf
  func(string) bool`**, checked alongside `resumedOf` (after the user-input
  clear, before the ordinary baseline/candidate path), reusing the SAME
  cleared-marker (`newCleared`) mechanism the other clear paths already use.
- **A new pure step function, `agent.SettleTick(prevTicks int, idleNow,
  awaitingNow bool) (newTicks int, settled bool)`** — the complementary case
  to `ResumeActivityTick`. It tracks CONSECUTIVE ticks a flagged session is
  genuinely raw-idle (`Session.IsIdle()`, not merely "not currently
  generating") AND its current tail shows NONE of the three
  `DetectNeedsInput` signals — the same idle-gated check that raises the flag
  in the first place, re-applied as a negative/clearing signal. Not idle, or
  still showing the signal, resets the streak to zero outright (same
  no-grace policy as `ResumeActivityTick` — under-clearing is safe, a false
  clear is not).
- **Small threshold (`NeedsInputSettleTicks` = 2), deliberately much smaller
  than `NeedsInputResumeTicks` (5) or `NeedsInputEscalationTicks` (8)**:
  `SettleTick` is not guarding against the BUG-061 tail-flooding hazard or a
  BUG-065-style brief-acknowledgment risk — a session that has gone genuinely
  raw-idle has, by construction, stopped producing the continuous
  redraw/animation bytes that cause tail flooding (flooding requires ongoing
  byte production, which raw-idle means has stopped), so a fresh read of its
  tail is trustworthy. Two ticks purely guards against an isolated torn read.
- **Both callers compute the per-session streak over every RUNNING session
  each tick**, mirroring the existing `resumedOf`/escalation passes, and
  thread the counter map exactly like the other per-tick maps in this area:
  `internal/tui/app.go`'s `detectNeedsInputSticky` (`App.needsInputSettle`)
  and `internal/api/push.go`'s `computeNeedsInput` (a new `prevSettle`
  parameter / `newSettle` return, backed by
  `idleWatcherState.needsInputSettle`).

## Capabilities

### Modified Capabilities

- `idle-detection`: the needs-input clear-on-input requirement gains a
  fourth, independent clear path — demonstrated settlement (genuinely idle,
  no current blocking signal) — so a flag raised on a worker that resolves
  its own block and wraps up FASTER than the sustained-resumed-activity
  threshold can still clear, without weakening any existing protection
  against a still-genuinely-blocked session (which never satisfies both
  "idle" and "no current signal" at once).
