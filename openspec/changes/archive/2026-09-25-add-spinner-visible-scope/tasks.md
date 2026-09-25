## 1. Scope spinnerLoop's redraw gate to visible work

- [x] 1.1 Add `App.visibleActiveSpinner` field (cached, `a.mu`-guarded).
- [x] 1.2 Add `computeVisibleActiveSpinner`: Tasks-tab-scoped via
  `TaskListView.VisibleTaskIDs()`; every other mode falls back to the
  original fleet-wide check unchanged.
- [x] 1.3 Wire the cache-write into `refreshTasksWithIDs` (tview main
  goroutine, already under `a.mu`).
- [x] 1.4 Replace `spinnerLoop`'s inline fleet-wide computation with a read of
  the cached flag under `a.mu` — no direct widget access from the background
  goroutine.

## 2. Tests

- [x] 2.1 `TestComputeVisibleActiveSpinner`: no running sessions; Tasks-tab
  visible-running-not-idle; Tasks-tab running-but-idle; Tasks-tab
  running-but-hidden (hera-managed, hidden); Hera-tab fallback unchanged;
  fullscreen-agent-view fallback unchanged; `refreshTasksWithIDs` wiring.
- [x] 2.2 Full existing `internal/tui` suite (incl. `-race`) passes unmodified
  — no regression.

## 3. Docs

- [x] 3.1 Gotcha bullet in `context/knowledge/gotchas/ui-threading.md`.

## 4. Investigated, not implemented (documented judgment call)

- [x] 4.1 Attempted to move the needs-input/content-idle scan off the tview
  main goroutine — found it requires a dedicated mutex to decouple from
  `a.tasks`'s shared `*model.Task` pointers (a genuine data race against
  `Draw()` otherwise). Deferred as a separate, larger effort.
- [x] 4.2 Attempted a TTL-based memoization cache for
  `readHeraRoles`/`readPRStates`/`readManagedTasks` to move their DB I/O off
  the tview main goroutine. Reverted after it caused two real test failures
  (a blind time-based cache masked a write that happened within the TTL
  window — `New()`'s own startup `refreshTasks()` call poisoned the cache
  before a test's `SetMetaBatch` write). Deferred; a correct fix needs a real
  dirty-check invalidation audit at every hera/PR-meta write site, the same
  scale of effort `add-tasks-fetch-dirty-check` required.
