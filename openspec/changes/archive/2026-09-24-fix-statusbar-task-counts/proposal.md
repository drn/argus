## Why

The TUI's bottom-left task summary does not account for every existing task. It
omits all `in_review` tasks and counts an `in_progress` task as active only while
its session appears in the running-session snapshot. The displayed counts can
therefore add up to far less than the number of task rows.

## What Changes

- Show one count for each stored task status: `active`, `pending`, `review`, and
  `done`. `active` means the task's status is `in_progress`; session liveness
  remains a separate signal for task-row rendering and reconciliation.
- Count the complete task snapshot supplied to the status bar, including
  archived tasks. A task contributes to exactly one status count, so the four
  counts add up to the number of existing tasks.
- Preserve transient notice precedence and the existing refresh path.

## Capabilities

### Modified Capabilities

- `tui-shell`: the default bottom status bar summarizes all persisted task
  statuses and includes archived tasks.

## Impact

- `internal/tui/widget/statusbar.go` and its focused rendering tests.
- TUI only: no REST field, daemon behavior, web UI, or macOS UI changes.
- Specs remain local documentation; `make pre-pr` remains the quality gate.
