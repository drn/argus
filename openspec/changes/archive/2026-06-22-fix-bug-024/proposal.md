## Why

**BUG-024 — stepping a worker "out of review" with `s`/`S` does not visibly
change its rail glyph.** When a worker reaches `done`, `RollHeraWorkerToReview`
stamps `meta:hera.ready_to_close=true` on its task. The rail glyph precedence
checks `ready_to_close` FIRST (it wins over the hera role status), so the row
renders the review `✓`. Stepping the worker back DOWN the ladder (`S` / revert,
or `s` off `done`) updates the hera role status in the DB but never clears the
`ready_to_close` mark — so the glyph stays pinned to `✓` and the status step is
invisible. The operator's intent ("step the role out of review") silently fails.

The bug report also cited an ancestor coordinator badge rendering "(?)" and the
cursor drifting down a row. Investigation found:

- The "(?)" is the `IconNeedsInput` glyph (Nerd Font `nf-fa-question_circle`,
  U+F059) — the legitimate marker for a role whose hera status is `blocked` and
  whose bound task is not actively running. The numeric `(N)` live-role count
  badge is `fmt.Sprintf(" (%d)", ...)` and can never emit "(?)". The report
  conflated the blocked status glyph with the count badge; no count-badge defect
  exists.
- Cursor drift could not be reproduced: `restoreCursor` re-pins by the role's
  stable `RoleID` across a status step (the role row never disappears), so the
  cursor stays anchored. A regression guard locks this in.

## What Changes

- **Stepping a worker OUT of `done` clears `ready_to_close`.** `Ops.StepStatus`
  gains the inverse of the done-roll: when a WORKER role steps to any non-`done`
  status, it calls the new `db.ClearHeraReadyToClose(taskID)` (soft-fail, so the
  status update always lands). With the review mark cleared, the glyph
  precedence falls through to the new hera status, so the step is visible. The
  task's argus WORKFLOW status (in_review) is left untouched — that is owned by
  the session lifecycle, not the hera ladder.

## Capabilities

### Modified Capabilities

- `hera-view`: stepping a worker role out of `done` with `s`/`S` clears the
  `ready_to_close` review mark so the rail glyph reflects the new hera status
  (previously the mark masked every step off `done`).

## Impact

- **Modified code:**
  - `internal/db/hera.go` — new `ClearHeraReadyToClose(taskID)` (inverse of
    `RollHeraWorkerToReview`'s stamp; meta-only, never touches workflow status).
  - `internal/tui/hera/ops.go` — `MutateStore` gains `ClearHeraReadyToClose`;
    `StepStatus` clears the mark when a worker steps to a non-`done` status.
- **No new key, no new dependency, no schema change, no daemon RPC.** Reuses
  existing in-process primitives only.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
