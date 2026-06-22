## 1. Selection.StatusRole (BUG-014)

- [x] 1.1 Add `Selection.StatusRole()` to `internal/tui/hera/model.go`: returns
      the selected role, else `Orch.CoordRole()`, else nil.
- [x] 1.2 Unit test in `model_test.go`: role selected → that role; header over an
      orchestrator with a coordinator → the coord role; header over a
      coordinator-less orchestrator → nil; empty selection → nil.

## 2. Step coordinator status from the header (BUG-014)

- [x] 2.1 `Ops.StepStatus` (`ops.go`) targets `sel.StatusRole()`; keep the
      worker-roll guarded on `Kind == worker` so a coordinator never rolls a task.
- [x] 2.2 `App.heraStatusStep` (`heraactions.go`) guards on
      `sel.StatusRole() == nil`, not `sel.Role == nil`.
- [x] 2.3 Tests (`ops` + actions): `S` on a header `done → blocked` clears the
      rail `✓`; `s` advances back; a coordinator stepping to `done` does NOT call
      `RollHeraWorkerToReview`; a coordinator-less header step is a no-op.

## 3. Task-aware Details coordinator status (BUG-015)

- [x] 3.1 Extract `coordRoleStatusLabel` (current `coordStatusLabel` body) in
      `details.go`, preserving the stale-`working` honesty.
- [x] 3.2 Add `coordTaskStatusLabel` surfacing only terminal task states
      (in_review / complete / failed via `TaskResult {"failed":true}`).
- [x] 3.3 `coordStatusLabel` combines them: `"<role> · task <state>"` when the
      task adds a signal, else just the role status. One row (ContentHeight
      unchanged).
- [x] 3.4 Tests (`details_test.go`): complete / in_review / failed append the
      suffix; ongoing / unbound do not; malformed `TaskResult` JSON is tolerated
      (no failed suffix); stale-`working` role honesty preserved.

## 4. Docs + gate

- [x] 4.1 Update help overlay / README ONLY if the text scopes `s`/`S` to workers.
- [x] 4.2 Add the coordinator role-vs-task status gotcha to
      `context/knowledge/gotchas/hera-view.md`.
- [x] 4.3 `openspec validate hera-coord-status --strict` passes.
- [x] 4.4 `make pre-pr` clean.
