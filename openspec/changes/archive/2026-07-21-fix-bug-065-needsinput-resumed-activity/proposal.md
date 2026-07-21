## Why

**BUG-065 — a hera worker's needs-input `(?)` rail indicator gets permanently
stuck even after the human has answered and the worker has visibly resumed
real work.** Confirmed live via a screenshot repro on orchestrator
"old-sherlock-cli", node "1a-clicore": the worker checked in "awaiting go"
(raising `(?)`), the coordinator relayed the human's decision via `hera_send`
and told it to proceed, the worker resumed — its Agent pane showed it running
shell commands and ticking off task checkboxes, clearly not idle — and the
rail STILL showed `(?)`.

Root cause: `agent.NeedsInputClear`'s clear-on-input filter deliberately reads
`LastUserInput()`, never `LastInput()` — this is BUG-034's own regression fix,
which exists specifically so a coordinator's reliable-notify delivery
(`WriteInputSystem`, used by `hera_send`) does not falsely clear a genuinely
still-parked worker's flag. That guard is correct on its own terms, but it has
a blind spot BUG-034 didn't anticipate: `WriteInputSystem` NEVER advances
`LastUserInput`, so when the relayed message IS the human's real answer and
the worker genuinely un-sticks, there was still no path back to clearing the
flag. Compounding this, the BUG-061 sticky pass keeps a flagged task flagged
unconditionally once raised (no re-match against the current tail required),
and needs-input `(?)` outranks the `IsActive` spinner in the rail's status-icon
precedence (BUG-A/BUG-F) — so the flag, once stuck, stays stuck forever
regardless of how much real, visible work the worker does afterward. Only
archiving the task cleared it.

## What Changes

- **`agent.NeedsInputClear` gains a third clear condition, `resumedOf func(string)
  bool`**, alongside the existing `lastInputOf`/`archivedOf` — checked after the
  user-input clear and before the ordinary baseline/candidate path, reusing the
  SAME cleared-marker (`newCleared`) mechanism the user-input clear already
  uses, so it inherits the BUG-063 stale-recandidacy guard for free with no new
  bookkeeping.
- **A new pure step function, `agent.ResumeActivityTick(prevTicks int, workingNow
  bool) (newTicks int, resumed bool)`**, structurally the mirror image of
  `EscalateParkedSelection` (BUG-029/060) but for the opposite direction: it
  counts CONSECUTIVE ticks a flagged session shows Claude's "working" affordance
  (`ContentIdleFingerprint`'s `working` return — the same "esc to interrupt"
  discriminator BUG-035/036 use for "genuinely generating or executing a tool,
  not merely idling/animating") and reports `resumed=true` once the streak
  reaches `agent.NeedsInputResumeTicks` (5) consecutive ticks.
- **Deliberately NO grace period on a miss**, unlike `EscalateParkedSelection`:
  the risk asymmetry runs the other way here — clearing too slowly (the flag
  lingers a few extra ticks after a genuine resume) is safe, while clearing a
  still-stuck agent is not — so a single non-working tick resets the streak to
  zero outright, even one tick before threshold. This is also what keeps a
  brief single-utterance acknowledgment from an agent that is still genuinely
  blocked ("still waiting on X", a few seconds of `working=true` while
  composing, then re-parking at the same or a new prompt) from crossing the
  threshold before the streak breaks — the BUG-034 regression this change must
  not reintroduce.
- **Both callers compute the per-session streak the same way the BUG-029
  escalation counter is computed** — over every RUNNING session each tick,
  independent of candidacy, using the SAME already-fetched tail/cols/rows the
  other passes in that tick use — and thread the counter map exactly like the
  other per-tick maps in this area: `internal/tui/app.go`'s
  `detectNeedsInputSticky` (`App.needsInputResume`) and `internal/api/push.go`'s
  `computeNeedsInput` (`idleWatcherState.needsInputResume`, whose signature
  grows a `prevResume` parameter and a 5th `newResume` return value).

## Capabilities

### Modified Capabilities

- `idle-detection`: the needs-input clear-on-input requirement gains a third,
  independent clear path — demonstrated sustained resumed activity — so a flag
  raised on a worker who was un-stuck by a coordinator's relayed answer (which
  never counts as user input) can still clear once the worker provably resumes
  real work, without weakening the existing protection against a mere system
  nudge falsely clearing a genuinely still-parked agent.
