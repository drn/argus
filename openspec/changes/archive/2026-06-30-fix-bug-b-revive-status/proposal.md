# Fix BUG-B — a revived/reattached hera worker stays mislabeled in_review

## Why

A hera worker whose bound task was rolled to `in_review` — by the BUG-050
close-out roll on an apparent session exit, or by a daemon-bounce startup
reconcile — but whose session is then genuinely revived/resumed (or confirmed
still alive on supervisor reattach) and is actively working again STAYS labeled
`in_review`. Its live work then shows the wrong status everywhere: the rail
status glyph, the task list, the spinner, and completion logic. On a daemon
bounced repeatedly, every live worker drifts into `in_review`, so a machine
running multiple actively-working agents reports `0 in_progress`.

Two stranding sites, both confirmed by reading the code:

- **`internal/daemon/bounce.go` `reattachSupervised`** — the supervisor keeps
  agents alive across a daemon bounce. The reconcile (`ReconcileStaleSessionsExcept`)
  only flips `in_progress → in_review` for true orphans; a session the supervisor
  confirms ALIVE that is already parked in `in_review` (from a prior roll) is
  never restored to `in_progress`. It is stranded.
- **`internal/tui/heraactions.go` `reviveHeraWorker`** — reviving a live-but-
  suspended worker via `runner.KickRerender` resumes the session but never
  touches the task status, so a worker revived out of `in_review` keeps the
  stale label.

The dead-session branch of `heraReattach` already restores `in_progress` (it
routes through `startSession`, which sets `in_progress` on a successful start).
Only the LIVE-session revive and the supervisor reattach miss the restore.

## What Changes

- **New shared DB helper `ReviveHeraWorkerToInProgress(taskID)`** — the precise
  inverse of `RollHeraWorkerToReview`. It restores a worker-bound task from
  `in_review` to `in_progress` ONLY when the worker is genuinely working again
  and is NOT awaiting close-out. A worker is "awaiting close-out" — and is LEFT
  in `in_review` — when it carries `meta:hera.ready_to_close` (the BUG-050
  done / clean-exit marker) OR its role status is `done`/`failed`. This is the
  single guard that preserves the PR #707 / BUG-050 invariant: a genuinely
  finished worker never auto-resumes, even though its idle session is still
  alive; only the coordinator/human closes it out.
- **`reattachSupervised` calls the helper** for each task the supervisor reports
  alive, restoring stranded live workers to `in_progress` on every bounce.
- **`reviveHeraWorker` calls the helper** on a successful revive kick (local
  store only — the live local daemon owns the same restore on its own reattach,
  so `--remote` mode skips it).

Out of scope (owned by the sibling BUG-A): all `needs-input` / `(?)` surfacing
logic. This change touches task STATUS only.

## Impact

- Affected specs: `hera-coordination` (new requirement: worker revive restores
  in_progress).
- Affected code: `internal/db/hera.go` (new helper), `internal/daemon/bounce.go`
  (reattach restore), `internal/tui/heraactions.go` + `internal/tui/app.go`
  (revive restore wiring).
- No schema change, no breaking change. Preserves BUG-050 (`done`/clean-exit
  workers stay `in_review`).
