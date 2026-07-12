# Fix BUG-034: needs-input from a free-text question never clears

## Why

An agent (commonly a hera coordinator) that ends a turn on a free-text question
— "should I…?", not a numbered selection/permission widget — is correctly
flagged needs-input (`?`) by the trailing-question heuristic (`endsInQuestion`).
But the flag NEVER clears, even after the user responds: the question text sits
in the 16 KB recent-output tail indefinitely, so the idle/sticky detector
re-matches it every tick. The only existing removals are "not detected fresh
this tick" and "session no longer running" — there is no "user responded" or
"archived" clear path. The free-text `?` is desired (users away from the
keyboard want to know an agent is waiting), so the fix keeps the signal and adds
the missing clear conditions.

## What Changes

- The needs-input flag persists indefinitely while the signal is present, with
  NO time-based or idle-based decay (current persistence is the desired
  behavior).
- The flag clears when EITHER the user delivers new input to that session after
  the flag was raised, OR the session's task is archived.
- Clear-on-input is deterministic: it compares the session's last-input
  timestamp against a per-task baseline captured when the task entered the set,
  so it fires even while the stale question still matches in the tail (it does
  NOT depend on the `?` scrolling out).
- Input to a different session never clears another session's flag.
- Selection/permission prompts are unchanged: answering one IS input, so the
  same clear-on-input path unifies with their existing natural clear.
- The trailing-question entry heuristic and the BUG-032 / BUG-033 guards are
  unchanged.
- The clear logic is a single shared `agent` helper invoked by both the daemon
  (`computeNeedsInput`) and the TUI (`detectNeedsInputSticky`) so the two stay
  in lockstep.

## Impact

- Affected specs: `idle-detection`
- Affected code: `internal/agent/needsinput.go` (shared clear helper),
  `internal/api/push.go` (daemon detector + watcher state),
  `internal/tui/app.go` (TUI detector + state).
