## 1. TaskListView mouse scroll

- [x] 1.1 Add `MouseHandler` to `internal/tui/taskview/tasklist.go`: gate on `InRect`, `MouseLeftDown` → `setFocus(tl)`, `MouseScrollUp`/`MouseScrollDown` → `CursorUp`/`CursorDown` (mirrors `gitpanel.FilePanel.MouseHandler`).
- [x] 1.2 Add tests in `internal/tui/taskview/tasklist_test.go`: scroll down/up moves the cursor and returns to the original task, left-click focuses the list, and an out-of-rect event is not consumed and does not move the cursor.
- [x] 1.3 Run `make test-pkg PKG=./internal/tui/taskview/` — confirm green.

## 2. Hera Rail mouse scroll

- [x] 2.1 Add `MouseHandler` to `internal/tui/hera/rail.go`: gate on `InRect`, `MouseLeftDown` → `setFocus(r)`, `MouseScrollUp`/`MouseScrollDown` → `CursorUp`/`CursorDown` (mirrors `gitpanel.FilePanel.MouseHandler` and the new `TaskListView.MouseHandler`).
- [x] 2.2 Add tests in `internal/tui/hera/rail_test.go`: scroll down/up moves the cursor and returns to the original selection, left-click focuses the rail, and an out-of-rect event is not consumed and does not move the cursor.
- [x] 2.3 Update the stale comment in `internal/tui/hera/panes_cover_test.go`'s `TestPanes_MouseScrollRoutesToWorkerPane` doc comment, which previously asserted the rail had no `MouseHandler` — the test's own assertions (wheel-over-agent/coord routes to that pane) are unchanged.
- [x] 2.4 Run `make test-pkg PKG=./internal/tui/hera/` — confirm green.

## 3. Docs and gate

- [x] 3.1 Add gotcha bullets to `context/knowledge/gotchas/tasklist-ui.md` and `context/knowledge/gotchas/hera-view.md` documenting the new handlers (InRect gating rationale, wheel routes through the same `CursorUp`/`CursorDown` keyboard nav uses).
- [x] 3.2 Run `make pre-pr` and confirm it passes clean (build, vet, fmt-check, lint-pr, test-cover-gate) before opening/updating a PR. `make vuln`'s stdlib CVE advisories are pre-existing and unrelated (CI runs it continue-on-error).
- [x] 3.3 Archive this change per this repo's CLAUDE.md: run `openspec archive add-rail-mouse-scroll` (or apply the merge-and-move by hand) so `openspec/specs/task-list-view/spec.md` and `openspec/specs/hera-view/spec.md` reflect the new requirements atomically, before any PR/merge.
