## 1. Count all persisted statuses

- [x] 1.1 Update the status-bar summary to count `in_progress`, `pending`,
  `in_review`, and `complete` tasks from the supplied full snapshot, including
  archived tasks, independently of the running-session set.
- [x] 1.2 Keep transient notices and keybinding hints working with the longer
  summary.

## 2. Verify

- [x] 2.1 Add focused rendering coverage for mixed statuses, archived tasks,
  and an `in_progress` task with no running session.
- [x] 2.2 Run the affected tests, `make test`, and `make pre-pr` (which includes
  the race and filtered coverage gate).
- [x] 2.3 Document any non-obvious status-count invariant in the relevant
  gotcha file.

## 3. Archive before merge

- [x] 3.1 Merge the delta into `openspec/specs/tui-shell/spec.md` and archive
  this change folder on the same branch before merge.
