## 1. Fix the redraw-loop supersede race

- [x] 1.1 Add `App.redrawLoopGen map[string]uint64`, initialized in `New`.
- [x] 1.2 `startAgentRedrawLoop` bumps the task's generation under `a.mu`
  before spawning the goroutine and captures it as `myGen`.
- [x] 1.3 Extract the exit check into `redrawLoopShouldExit(taskID, myGen)` —
  exits when no longer viewing the task OR when superseded by a newer
  generation.
- [x] 1.4 Clear `redrawLoopGen[taskID]` in `deleteTask`, alongside the
  existing `pendingRerenderRestart`/`committedCols` cleanup.

## 2. Tests

- [x] 2.1 Unit test: a stale generation must exit once a newer
  `startAgentRedrawLoop` call bumps the counter for the same task, even
  though `stillViewing` still reads true.
- [x] 2.2 Unit test: `deleteTask` clears the task's `redrawLoopGen` entry.

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/ui-threading.md`.
