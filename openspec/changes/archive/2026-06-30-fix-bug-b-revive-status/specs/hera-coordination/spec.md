## ADDED Requirements

### Requirement: Worker revive restores in_progress

The system SHALL restore a worker-bound task from in_review back to in_progress when its session is genuinely revived/resumed and working again, via the single shared helper `ReviveHeraWorkerToInProgress` — the precise inverse of `RollHeraWorkerToReview`. The restore is worker-kind only, no-ops unless the task is currently in_review (so it never clobbers a human-set complete/pending and never disturbs an already-in_progress task), touches the DB status only (never the session), and is idempotent.

The restore SHALL NOT fire when the worker is awaiting coordinator close-out — that is, when its bound task carries `meta:hera.ready_to_close` (the BUG-050 done / clean-exit stamp) OR any of its live worker roles has a terminal role-status (`done` or `failed`). This guard preserves the PR #707 / BUG-050 invariant: a genuinely-finished worker stays in_review even when its idle session is still alive, because a worker never self-completes — the coordinator/human closes it out or decides on a failure.

Two trigger sites share the helper so they cannot drift:

- The daemon's supervisor-mode startup reattach (`reattachSupervised`) calls it for every task the supervisor confirms ALIVE, so a live worker stranded in in_review by a prior roll or reconcile is restored to in_progress on each bounce (the true orphans the supervisor does NOT report alive still flip the other way, to in_review).
- The TUI's live-session revive (`reviveHeraWorker`, the Enter-key in-place `KickRerender` resume) calls it on a successful kick (local store only; `--remote` mode defers to the live local daemon's own reattach restore).

Derived from: `internal/db/hera.go` (`ReviveHeraWorkerToInProgress`), `internal/daemon/bounce.go` (`reattachSupervised`), `internal/tui/app.go` (`reviveRestoreInProgress`) + `internal/tui/heraactions.go` (`reviveHeraWorker`).

#### Scenario: Stranded live worker is restored on reattach

- **WHEN** the supervisor reports a worker's session still alive across a daemon bounce and that worker's task is parked in in_review with no close-out marker
- **THEN** the task is restored to in_progress while true orphans still flip to in_review

#### Scenario: Revived suspended worker returns to in_progress

- **WHEN** a live-but-suspended worker in in_review is revived in place via KickRerender and the kick succeeds
- **THEN** its task is restored to in_progress

#### Scenario: A done or clean-exited worker stays in_review

- **WHEN** a worker carries meta:hera.ready_to_close (reported done or cleanly exited) and its idle session is still alive on revive/reattach
- **THEN** the restore no-ops and the task stays in_review for coordinator close-out

#### Scenario: A failed worker stays in_review

- **WHEN** a worker's role status is failed and its session is still alive on revive/reattach
- **THEN** the restore no-ops and the task stays in_review for coordinator attention

#### Scenario: Non-worker and non-review tasks are untouched

- **WHEN** the task is coordinator-bound, holds no live worker binding, or is not currently in_review (in_progress / complete / pending)
- **THEN** the restore no-ops and the status is left unchanged
