## Context

`agent.NeedsInputClear` is a pure function: it only sees a per-tick candidate
ID list, a frozen per-candidate baseline map, a `lastInputOf` timestamp lookup,
and an `archivedOf` predicate. It has NO visibility into WHY a task became a
candidate this tick (idle-gated raw detection, a content-stability fingerprint
match, or a BUG-029/060 escalation grace tick) or what the underlying terminal
content actually is. This design constraint shapes what fix is safe.

## Goals / Non-Goals

- **Goal:** a task that `NeedsInputClear` correctly cleared must not be
  recaptured into a stuck, permanently-flagged state by a later stale
  re-candidacy at the exact same last-input timestamp, for as long as its
  session keeps running.
- **Goal:** a task that leaves the running set (stops, is archived, or is
  restarted) and later becomes a candidate again must re-arm exactly like a
  brand-new candidacy — unchanged from today.
- **Non-goal:** distinguishing "the same stale content re-detected" from "a
  second, textually distinct, still-unanswered prompt that happens to arrive
  before the user's next keystroke" purely from (task ID, timestamp). Doing
  that correctly requires threading a content signature (e.g. the existing
  `agent.ContentFingerprint`) into the clear/suppress decision, which is a
  larger, riskier change to a pure/heavily-tested function's contract. Flagged
  as a named follow-up below, not attempted in this change.

## Decision: track a "recently cleared" marker scoped to `running`, not to `candidates`

`NeedsInputClear` gains a `running []string` parameter — the full set of
currently-running task IDs (a superset of `candidates`; both callers already
compute this). A new `prevCleared`/`newCleared map[string]time.Time` carries,
per task ID, the `lastInputOf` value recorded at the moment of the most recent
REAL clear. Unlike the existing baseline map (rebuilt only from `candidates`,
so a task drops out of tracking the instant it isn't a candidate for even one
tick), the cleared marker is carried forward for every ID in `running`,
independent of candidacy that tick. A later re-candidacy is checked against
this marker before falling through to the ordinary fresh-baseline capture path:
if the marker is present and `lastInputOf(id)` has not advanced past it, the
candidacy is treated as stale and suppressed outright (not added to `out`, no
baseline recaptured). A genuinely newer `lastInputOf(id)` fails that check on
its own — no explicit expiry/TTL bookkeeping needed — and the task falls
through to the normal fresh-candidacy path, re-arming exactly as before.

### Why scope to `running`, not track indefinitely or use a fixed tick-count cooldown

An indefinite, never-pruned map would leak one entry per task ID ever seen, for
the life of a long-running daemon/TUI process. A fixed "cooldown for N ticks"
requires either wall-clock time (this function takes none, deliberately, to
stay a pure/unit-testable step function) or an approximate tick counter with
its own magic constant to tune (the codebase already carries one such constant,
`NeedsInputEscalationTicks`, and BUG-029/060's history shows tick-count-based
schemes are easy to get subtly wrong). Scoping the marker's lifetime to the
task's presence in `running` is exact (not approximate), needs no new constant,
mirrors the SAME already-established idiom this file uses for `needsInputFP`/
`needsInputEscalation`/`ContentIdleState` (all rebuilt fresh from a
caller-supplied running/candidate set each tick), and is self-pruning: a task
that stops running (deleted, or the session exits) naturally drops out of
`running` and its marker is not carried forward — no leak.

### Why the accepted trade-off is acceptable here

The scenario this cannot handle — a SECOND, distinct, still-unanswered prompt
arriving with `lastInputOf(id)` unchanged from the moment the FIRST prompt was
answered — requires: (a) the agent to show a brand-new prompt before the user
types anything else, AND (b) that prompt to remain unanswered long enough to
matter, AND (c) the exact same task to have a marker still pinned at that
timestamp (i.e. `running` never dropped it in between). In that narrow window,
`(?)` would be delayed rather than instant — but the user can still SEE the
live, unanswered prompt by opening the pane directly, and the flag surfaces the
moment ANY newer input reaches that session (including the user just
interacting with the row at all). This is a materially smaller cost than
BUG-063 itself: a flag that gets stuck TRUE forever after the user has ALREADY
answered, requiring a manual nudge that itself only works some of the time.

**Follow-up (not required here):** thread a content fingerprint into the clear
marker (alongside the timestamp) so suppression additionally requires the
re-presented candidacy's content to match what was showing at clear time — this
would close the accepted gap above. Left for a future change if the trade-off
proves to matter in practice; call out in the PR description for reviewer
awareness.

## Call-site changes

- `internal/tui/app.go`: `detectNeedsInputSticky` already receives
  `runningIDs`; thread it into `agent.NeedsInputClear` and add
  `App.needsInputCleared map[string]time.Time` alongside the existing
  `needsInputSince`.
- `internal/api/push.go`: `computeNeedsInput` already receives `runningIDs`;
  thread it through the same way, adding `idleWatcherState.needsInputCleared`.

Neither caller's detection signals change — this is purely a clearing-logic
fix.
