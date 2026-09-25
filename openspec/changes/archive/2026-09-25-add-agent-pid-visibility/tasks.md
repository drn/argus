## 1. Detail panel PID row

- [x] 1.1 In `internal/tui/taskview/taskdetail.go` `Draw()`, add a "PID" row
  (via the existing `drawField` helper) right after Backend, shown only when
  `t.AgentPID != 0`.
- [x] 1.2 In `taskShape()`, hash `td.task.AgentPID` (mirroring how
  Project/Branch/Backend/Worktree are hashed) so a PID change fires
  `OnBranchChange`.

## 2. Filter matches PID

- [x] 2.1 In `internal/tui/taskview/tasklist.go` `matchesFilter`, add a PID
  candidate string — `strconv.Itoa(t.AgentPID)` only when `t.AgentPID != 0`,
  otherwise excluded from matching — alongside the existing name/project
  candidates, same substring/case-insensitive semantics.

## 3. Tests

- [x] 3.1 `taskdetail_test.go`: PID row renders when `AgentPID != 0`, is
  absent when `AgentPID == 0`, and still renders for a non-running task with
  a stale nonzero PID.
- [x] 3.2 `taskdetail_test.go`: `taskShape()` changes when `AgentPID` changes
  (same task ID, different PID) — asserts `OnBranchChange` fires.
- [x] 3.3 `tasklist_test.go`: `matchesFilter` — a filter term matching a
  substring of a nonzero `AgentPID` keeps the task visible; a `"0"` filter
  term does not match a task with `AgentPID == 0` on that basis alone.

## 4. Docs

- [x] 4.1 No new gotcha expected (this is straightforward field plumbing,
  not a new invariant) — skip unless implementation surfaces one.
