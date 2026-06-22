## Why

Two coordinator-status bugs in the native Hera view:

- **BUG-014 — the coordinator `✓` (done) cannot be cycled with `s`/`S`.** The
  coordinator role is folded into the orchestrator HEADER (it has no child row),
  so landing the cursor on the header yields a selection with `Role == nil` and
  `Orch` set. Both `App.heraStatusStep` and `Ops.StepStatus` early-return when
  `Selection.Role == nil`, so the hera role-status ladder never runs on a
  coordinator. The operator can mark a coordinator done over MCP
  (`hera_status("done")`) but can never clear or re-advance it from the rail —
  the `✓` glyph is stuck.

- **BUG-015 — the Details pane coordinator status is not task-aware.** The
  `" Details "` roster's `coordinator:` line derives its label from the hera
  ROLE status only (`coordStatusLabel`). When the coordinator's bound argus task
  reaches a terminal workflow state (in_review / complete / failed), the Details
  pane gives no hint — it still reads `live`/`idle`/etc. from the manual role
  ladder, which the session lifecycle never reconciles.

## What Changes

- **`s`/`S` cycle the coordinator's hera status from a HEADER selection.** A new
  `Selection.StatusRole()` resolves the role the status keys act on: the selected
  role when one is selected, else the orchestrator's folded coordinator role
  (`OrchView.CoordRole()`), else nil. Both `heraStatusStep` and `StepStatus` use
  it, so `S` moves a coordinator `done → blocked` (the rail `✓` clears), `s`
  advances it back, persisted via `UpsertHeraRoleStatus` (survives restart). The
  worker-only `RollHeraWorkerToReview` roll stays guarded on `Kind == worker`, so
  stepping a coordinator to `done` never rolls a task. A header over a
  coordinator-less orchestrator is a silent no-op.

- **The Details coordinator status line is task-aware.** `coordStatusLabel`
  combines the role status (via the preserved `coordRoleStatusLabel`, which keeps
  the BUG-003 stale-`working` honesty) with a new `coordTaskStatusLabel` that
  surfaces only TERMINAL bound-task states (in_review / complete / failed-via
  `TaskResult {"failed":true}`, mirroring `dagview.parseFailed`). When the task
  adds a signal the line reads `"<role> · task <state>"` (e.g.
  `live · task complete`); an ongoing or unbound task adds no suffix. It stays
  one row, so `DetailsView.ContentHeight()` is unaffected, and the coordinator
  metadata block (Created / Last activity / Repos) is untouched.

## Capabilities

### Modified Capabilities

- `hera-view`: `s`/`S` step the coordinator's hera status from a header
  selection (via `Selection.StatusRole()`), not only an explicit role row; the
  Details coordinator status line is task-aware (terminal bound-task state
  appended to the role status).

## Impact

- **Modified code:**
  - `internal/tui/hera/model.go` — `Selection.StatusRole()`.
  - `internal/tui/hera/ops.go` — `StepStatus` targets `sel.StatusRole()`; the
    worker-roll guard already keys on `Kind == worker`.
  - `internal/tui/heraactions.go` — `heraStatusStep` guards on
    `sel.StatusRole() == nil`, not `sel.Role == nil`.
  - `internal/tui/hera/details.go` — `coordRoleStatusLabel` (extracted),
    `coordTaskStatusLabel` (new), `coordStatusLabel` (combines them).
  - `internal/tui/modal/help.go` + `help_test.go`, `README.md` — only if the
    overlay text scopes `s`/`S` to workers (it does not bind a new key).
  - `context/knowledge/gotchas/hera-view.md` — the coordinator role-vs-task
    status distinction.
- **No new key, no new dependency, no schema change, no daemon RPC.** Reuses
  existing in-process primitives only.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
