## Task List & UI

- **Use `Header.SetNotice`/`ClearNotice` for async background operation progress, not blocking modals.** The header notice is a general-purpose left-aligned spinner + text area. Uses the progress spinner from `internal/spinner`. Progress tracking uses `atomic.Int32` + `QueueUpdateDraw` from background goroutines.
- **Every modal close function must call `a.tapp.SetFocus(a.tasklist)`.** Without this, tview focus remains on the deleted modal widget, silently dropping focus-dependent key events (e.g., up/down in the task list). Tab switching (left/right) still works because it's handled in `handleGlobalKey` before focus-based routing, which masks the bug.
- **Page wrappers with non-interactive child panels must guard `setFocus` in `MouseHandler`.** tview's default `Box.MouseHandler()` calls `setFocus(self)` on any click. Non-interactive panels (no `InputHandler`) silently steal focus, dropping all keyboard input. Fix: wrap `setFocus` in the page's `MouseHandler` to redirect focus to the interactive panel (e.g., `TaskPage` → tasklist). Symptom: user must switch tabs and back to regain keyboard control.
- **`rowTask` is `iota` (zero value) — never use `rowKind != 0` as a sentinel.** Use a boolean `hasPrev` flag when checking whether a previous row exists. The zero-value trap silently skips the intended code path for the most common row kind.
- **`SetTasks` must preserve cursor position across rebuilds.** Save the current row before `buildRows()`, then call `restoreCursor()` after. Without this, status changes via `s`/`S` keys appear to not work because the cursor jumps to a different row on refresh.
- **`restoreCursor` must filter by section (active / waiting-for-review / archive).** Cross-section project name collisions cause cursor jumps; `restoreCursor` takes a `rowSection` and uses `sectionAt` to reject matches in the wrong section.
- **`restoreCursor` must handle header/separator rows, not just tasks and projects.** `autoExpand` calls it after a section-toggle rebuild with the cursor possibly parked on a section header or separator. Falling through to `clampCursor` in those cases jumps the cursor away from the section you just entered.
- **`buildRows()` splits tasks into active / waiting-for-review / archived before grouping, in that order.** Archive takes precedence over WaitingReview when both flags are set, so a project with only WR-flagged or archived tasks never appears in the main section.
- **`sectionAt(idx)` scans upward for the first section header.** Rows above all headers → `sectionActive`; rows below WR header but above archive header → `sectionWaitingReview`; rows at or below archive header → `sectionArchive`. The separator before archive logically belongs to WR; the separator before WR belongs to active.
- **`skipUpPastHeader` chains through stacked headers.** Moving up out of the archive section can pass through a (collapsed) WR header/separator pair; the loop keeps going until it finds a task or project row rather than handling a fixed chain depth.
- **Task-list previews must render the latest visible emulator lines, not `CellAt(x,y)` from row 0.** For Codex and any long-running PTY output, useful content often lives in scrollback or lower rows; replay logic must trim to bottom-of-history like `TerminalPane.paintEmu`.
- **Stopped agent → `StatusInReview`, not Pending.** Stopped means "needs human review".
- **Idle+unvisited tasks visually promoted to InReview.** Cleared on entering agent view.
- **Enter on completed task is a no-op.**
- **Daemon process appears as "argusd"** via symlink in `AutoStart`.
- **Task rename is display-only.** Worktree dir and branch unchanged. Use `db.Rename(id, name)` (not `db.Update`) — the modal captures a task pointer at open time, and a background `refreshTasksAsync` can replace `a.tasks` while the modal is open, orphaning the pointer. `db.Update` on the stale pointer overwrites concurrent field changes (e.g., agent exit setting status=Complete).
- **`ensureCursorVisible` must reset scrollOffset when all lines fit.** Check `totalLines <= visibleLines` → reset to 0.
- **`SettingsView.renderList` must clamp `scrollOff` to `max(0, len(rows)-innerH)` after cursor-based adjustment.** Without the clamp, window resize (maximize) leaves `scrollOff` stranded — the top settings rows become unreachable because the cursor is still within the visible range relative to the stale offset.
- **Tabs are 1=Tasks, 2=Reviews, 3=Settings.** All statusbar hints and test assertions must match.
- **Fork task `executeFork` must run worktree creation + context extraction in a background goroutine.** Git diff and session log reads are I/O that blocks the UI thread. The `QueueUpdateDraw` callback handles DB persistence and session start on the tview thread — same race-avoidance pattern as new task creation (use `refreshTasksLocal`, not `refreshTasksAsync`).
- **Task list filter must bypass global rune key handling in `handleGlobalKey`.** When `tasklist.Filtering()` is true, the `KeyRune` case must `break` before checking `q`/`1`-`4` shortcuts — otherwise typing those characters quits the app or switches tabs instead of appending to the filter.
- **`buildRows` must expand all projects when a filter is active.** Without `filterActive` check, filtered tasks in collapsed projects are invisible — the filter matches them but they're hidden behind a collapsed project header.
- **String slicing for backspace must use `utf8.DecodeLastRuneInString`, not `len()-1`.** `len()` counts bytes, not runes — slicing mid-rune corrupts multi-byte UTF-8 characters. Same applies to cursor column positioning: use `ansi.StringWidth()` for display width, not `len()`.
- **`drawTaskRow` cursor fill must not overwrite elapsed time.** The fill loop extends the highlight to the row edge, but elapsed time is drawn right-aligned first. Compute `elapsedCol` once and use it as the fill boundary — filling past it overwrites the duration indicator.
- **`moveCursor` must not fire `OnCursorChange` when clamped at boundaries.** When pressing up at the top or down at the bottom, `tl.cursor` is clamped to the same value — firing the callback triggers unnecessary git diff refreshes and preview fetches. Use a deferred guard comparing `tl.cursor != prev`.

## Modal Forms (`handle*FormKey`)

- **Every `handle*FormKey` early-return on validation or DB save failure MUST set `form.done = false` before returning.** `HandleKey` only ever sets `done = true` (on Enter / Ctrl+S); it never clears it. Without the reset, the next keypress flows back through `Done()`, hits the same validation failure, and the user is stuck in an infinite submit loop with no way out except Escape. `projectform`/`backendform`/`scheduleform` all have this dance — copy it verbatim when adding a new modal form.

## Task Row Rendering

- **`drawTaskRow` gives the task name priority over the branch in width allocation.** The name is sized first (ignoring branch), then the branch fills remaining space. If branch is sized first (reserving space before name), narrow terminals squeeze the name to zero while showing a useless branch fragment.

## Spinner Animation

- **Project status icon priority: actively running > in_review > idle in_progress.** If any task in a project is actively running (`running && !idle`), the project shows the spinner — even if other tasks are in-review. The variable is `hasActivelyRunning`, not the old `allInProgressIdle` inverse.
- **Spinner frame computation is time-based, not tick-based.** `updateSpinnerFrame()` computes `int(time.Now().UnixMilli() / interval) % frameCount` in `Draw()`. The 1-second tick loop is too slow for 100ms spinner frames. A dedicated `spinnerLoop` (100ms ticker) triggers redraws only when `len(runningIDs) > 0`.
- **`model.SetActiveSpinner()` is a package-level setter — not thread-safe for concurrent writers.** Currently safe because only the tview main goroutine (settings UI) calls it. If daemon or API ever sets it, add synchronization.
