# Tasks: add-switcher-status-icons

## 1. Shared classifier

- [x] 1.1 Add `internal/tui/widget/taskstatusicon.go`: `TaskStatusInputs` struct + `TaskStatusIcon(status, in, frame) (rune, tcell.Style)` mirroring `drawTaskRow`'s switch (pending ○ / in_progress: needs-input → idle-unvisited → idle/absent → spinner / in_review clipboard / complete ✓), plus `CurrentSpinnerFrame()` for surfaces without their own animation tick.
- [x] 1.2 Write table tests (`taskstatusicon_test.go`) covering every status + in_progress sub-state precedence, and that `CurrentSpinnerFrame` stays in range.

## 2. Task list refactor

- [x] 2.1 Replace the inline status switch in `drawTaskRow` (`internal/tui/taskview/tasklist.go`) with a call to `widget.TaskStatusIcon`; behavior unchanged (existing render tests stay green).

## 3. Switcher rendering

- [x] 3.1 Add `Running`/`Idle`/`IdleUnvisited` to `taskSwitcherEntry` and a `statusIcon()` method over the shared helper (`internal/tui/taskswitcher.go`).
- [x] 3.2 Draw the status icon to the left of the name in both `drawFlatItems` (at `innerX`) and `drawGroupedItems` (at `innerX+2`, indented under the folder); the icon keeps its status color even on the selected row.
- [x] 3.3 Drop the status name from `taskSwitcherRowText` (keep project) and `switcherTaskRowText` (name only).
- [x] 3.4 Populate the new flags in `App.openTaskSwitcher` from the same `runningIDs`/`idleIDs`/`idleUnvisited`/`needsInputIDs` sets that feed the task list.
- [x] 3.5 Update switcher tests: row text no longer carries the status name; draw tests assert the leading icon renders and the status display name does not.

## 4. Docs + gate

- [x] 4.1 Add a gotcha (`tasklist-ui.md`) recording that the switcher and task list share `widget.TaskStatusIcon` as the single source of truth; bump the index bullet count.
- [x] 4.2 Run `make pre-pr` to a clean pass; archive this change (merge delta into the base `forms-and-modals` spec, move folder to `changes/archive/`).
