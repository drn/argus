## Why

**BUG-063 — the Hera rail's `(?)` needs-input indicator can get permanently
stuck after the user has already answered, on both coordinator and worker
rows.** A manual focus+keypress nudge (which forces a PTY resync/repaint)
sometimes clears it, but it can flap back to `(?)` later with no new input at
all, and once stuck it stays stuck until the user produces a strictly newer
keystroke in that session.

Root cause: `agent.NeedsInputClear` (`internal/agent/needsinput.go`) freezes a
per-task `baseline` (the session's last-user-input timestamp) the first tick a
task becomes a needs-input candidate, and clears the flag once the session's
last-input timestamp advances past that baseline. The baseline map is rebuilt
from scratch every tick, scoped ONLY to the current tick's candidate list — so
the instant a task is correctly cleared and drops out of candidacy (even for a
single tick), its baseline is forgotten entirely.

`detectNeedsInputSticky` (TUI) and `computeNeedsInput` (daemon) both maintain
extra passes — a content-stability fingerprint match and (TUI only) a
BUG-029/060 escalation-grace tick — that scan a session's tail independent of
whether it is a current candidate, specifically so a session that never goes
idle still gets flagged. Either of these can re-present an already-answered
task as a "fresh" candidate on some LATER tick, if the session's visible tail
still shows the same stale, already-resolved content (a rendering/log-write lag,
or a widget that hasn't visually cleared yet). When that happens, because the
task has no baseline, `NeedsInputClear` treats it as a brand-new candidacy and
recaptures `baseline = lastInputOf(id)` — which is the SAME timestamp as the
user's last real keystroke, since nothing newer has happened. The comparison
`lastInputOf(id).After(baseline)` can then never become true, and the task is
stuck in the candidate set until the user types something newer — explaining
"clears briefly, flaps back, needs a fresh nudge, sometimes flaps again."

`RoleView.NeedsInput` (`internal/tui/hera/model.go`) is just the same
daemon/TUI-published needs-input set filtered by task ID, so there is no
separate coordinator-only code path — the bug hits both row kinds identically.

## What Changes

- **`agent.NeedsInputClear` gains a `running` parameter and a persisted
  "recently cleared" marker (`prevCleared`/`newCleared`, keyed by task ID,
  scoped to `running`) alongside the existing per-candidate baseline.** When a
  real clear fires, the session's last-input timestamp at that moment is
  recorded as the clear marker. That marker is carried forward for every
  STILL-RUNNING task regardless of whether it is a candidate on any given
  tick — closing the exact gap that let a stale re-candidacy recapture a
  stuck baseline. A later re-candidacy at the SAME (or older) last-input
  timestamp is recognized as stale and suppressed; a genuinely NEWER
  last-input timestamp drops the marker's effect on its own (no explicit
  expiry needed) and re-arms normally.
- **The marker is dropped when a task leaves the `running` set or is
  archived** — mirrors the existing baseline-drop-on-archive behavior, so an
  un-archived or restarted task re-arms cleanly, exactly like a task's first
  candidacy.
- **Both callers are updated in lockstep**: `internal/tui/app.go`'s
  `detectNeedsInputSticky` (which already has `runningIDs` available) and
  `internal/api/push.go`'s `computeNeedsInput` (same). Neither gains new
  detection signals — this only changes how already-computed candidates are
  filtered for clearing.
- **Accepted scope limit** (documented, not silently swallowed): the fix
  cannot distinguish "the exact same already-answered prompt re-detected" from
  "a second, textually distinct prompt that happens to arrive with the
  session's last-user-input timestamp unchanged" (e.g., an immediate follow-up
  permission prompt the agent shows before the user types anything new). Both
  look identical to `NeedsInputClear` (same task ID, same timestamp). Given
  BUG-063 is a confirmed, frequently-hit PERMANENT stuck bug and the traded-off
  scenario requires a brand-new, still-unanswered prompt to coincide with a
  suppression window and zero intervening keystrokes, this trade is accepted.
  See the design doc and the gotcha entry for detail; a content-fingerprint-
  aware refinement is a named follow-up, not required here.

## Capabilities

### Modified Capabilities

- `idle-detection`: the needs-input clear-on-input requirement gains a
  stale-re-candidacy suppression tied to the running-task lifecycle, so a
  correctly-cleared flag cannot be recaptured by a later stale detection pass
  while the underlying session is still running and no new input has arrived.
