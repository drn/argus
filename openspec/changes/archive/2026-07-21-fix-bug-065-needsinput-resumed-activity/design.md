## Context

`agent.NeedsInputClear` is a pure function with two existing clear paths: (a)
a session's `LastUserInput()` advancing past the baseline captured when the
flag was raised, and (b) the task being archived. `LastUserInput()` is
deliberately distinct from `LastInput()` (BUG-034's regression fix): every
input surface funnels through the same session handle, including reliable
pane delivery (`internal/notify`'s `WriteInputSystem`, the delivery mechanism
`hera_send` uses) — and system delivery must NOT count as the user answering a
prompt, or a coordinator's routine nudge to a genuinely still-parked worker
would falsely clear its flag. That guard is correct, but it is also the exact
reason a coordinator RELAYING the human's real answer can never clear the flag
through path (a): `WriteInputSystem` never advances `LastUserInput`, by
design, regardless of whether the delivered message resolves the block or not.

## Goals / Non-Goals

- **Goal:** a worker's needs-input flag clears once the worker demonstrably
  resumes real, sustained activity — regardless of whether the input that
  unstuck it arrived via a direct user keystroke or a coordinator's relayed
  answer.
- **Goal:** do not weaken BUG-034's original protection — an unrelated system
  nudge to a genuinely still-parked agent (one that does not actually resume
  real work) must not clear the flag.
- **Non-goal:** distinguishing "a coordinator's relayed answer" from "a direct
  user keystroke" as the causal trigger. The fix does not attempt to attribute
  WHY the agent resumed — only THAT it demonstrably did, via an independent,
  content-based signal. This sidesteps needing any new plumbing between
  `internal/notify`/hera and the needs-input detector.

## Decision: a third clear path driven by sustained "working" affordance, not by input provenance

Rather than trying to make `WriteInputSystem` conditionally count as user
input (which would reopen the exact BUG-034 hole this change must not
regress), the fix adds an entirely independent, content-based signal: has the
session shown Claude's "working" affordance — the "esc to interrupt" hint
already used elsewhere in this subsystem (BUG-035/036) as the discriminator
for "genuinely generating or executing a tool, not merely idling/animating" —
for a SUSTAINED run of consecutive ticks since the flag was raised. This
signal is orthogonal to input provenance: it fires whether the resuming input
was a user keystroke or a relayed hera message, and it does NOT fire merely
because *some* input was delivered — only when the agent's own visible
behavior demonstrates real, ongoing activity.

`agent.ResumeActivityTick(prevTicks int, workingNow bool) (newTicks int, resumed
bool)` is a pure step function, structurally the mirror image of
`EscalateParkedSelection` (BUG-029/060): both track a consecutive-tick streak
against a tunable threshold. They point in opposite directions and have
opposite miss-tolerance:

- `EscalateParkedSelection` escalates a session INTO the needs-input set when
  a signal that OUGHT to be present (genuinely parked) keeps failing to match
  due to detection noise (torn reads, blink-flood) — so a single miss is held
  in a one-tick grace period rather than discarded, because the failure mode
  of NOT escalating (staying stuck unflagged) is the one being fixed.
- `ResumeActivityTick` moves a session OUT of the needs-input set when a
  signal is sustained — so a single miss resets the streak to zero
  IMMEDIATELY, with no grace period. The failure mode this must avoid is the
  opposite one: falsely clearing a still-genuinely-blocked agent. Clearing too
  slowly (lingering a few extra ticks after a genuine resume) is an acceptable
  cost; clearing too eagerly is not.

### Why "sustained" (a tick threshold) rather than "currently working"

A coordinator's relayed message, even one delivered to a genuinely still-
parked agent, can itself provoke a BRIEF reply — Claude reading and
acknowledging the message ("still blocked on X, awaiting your input") shows
the working affordance for the few seconds it takes to compose that reply,
then re-parks at the same or a new blocking prompt. A single-tick or
first-working-tick clear would falsely resolve the flag on exactly this
nudge-without-resolution case — reopening BUG-034's own regression under a
different name. Requiring `agent.NeedsInputResumeTicks` (5) CONSECUTIVE
working ticks before clearing means a brief acknowledgment burst — bounded by
however long composing one short reply takes — is very unlikely to sustain the
threshold before the agent's own content re-shows the blocking shape (which
resets the streak to zero on the very next tick, per the no-grace-period
design above). A genuinely resumed worker (running shell commands, ticking off
checkboxes) sustains the working affordance for many seconds continuously, and
clears promptly once the tick-cadence-scaled threshold elapses.

### Why reuse the existing cleared-marker mechanism rather than new bookkeeping

The resumed clear path sets `newCleared[id] = li` (the current `lastInputOf`
reading) exactly like the existing user-input clear path does, rather than
inventing a parallel "resumed marker." This means a resumed clear
automatically inherits the BUG-063 stale-recandidacy guard: if some LATER tick
re-presents the SAME task as a candidate via a content-heuristic re-flag with
`lastInputOf` unchanged since the resumed clear fired, it is recognized as
stale and suppressed — without any new logic, because `NeedsInputClear`
already treats "some clear fired, marker recorded" uniformly regardless of
which of the three conditions produced it.

### Why NOT gate on WriteInputSystem/WriteInput provenance directly

An alternative considered and rejected: teach `NeedsInputClear` (or its
callers) to treat a WriteInputSystem delivery as "provisionally answering,"
pending some other confirmation. This was rejected because it reintroduces
exactly the ambiguity BUG-034 resolved — there is no reliable way to tell,
from the fact of delivery alone, whether a relayed message resolves the block
or is an unrelated nudge. The content-based "did the agent demonstrably
resume" signal sidesteps this entirely: it doesn't matter why input arrived,
only whether real work visibly followed.

## Call-site changes

- `internal/agent/needsinput.go`: `NeedsInputClear` gains `resumedOf
  func(string) bool`, checked after the user-input clear branch and before the
  ordinary baseline-capture path. `ResumeActivityTick` and
  `NeedsInputResumeTicks` are new, standalone exports alongside
  `EscalateParkedSelection`/`NeedsInputEscalationTicks`.
- `internal/tui/app.go`: `detectNeedsInputSticky` gains a resumed-activity pass
  (mirroring its existing BUG-029 escalation pass — same tail/cols/rows
  already fetched for `AwaitingInputFingerprint`), backed by a new
  `App.needsInputResume map[string]int` field.
- `internal/api/push.go`: `computeNeedsInput` gains the same pass; its
  signature grows a `prevResume map[string]int` parameter and a 5th
  `newResume` return value, backed by a new `idleWatcherState.needsInputResume`
  field.

Neither caller's ENTRY detection signals change — the trailing-question
heuristic, the idle gate, the content-stability/escalation passes, and the
existing two clear conditions are all unchanged. This is purely an additional,
independent way for an already-raised flag to be resolved.
