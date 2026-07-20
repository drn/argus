## 1. Fix and verify

- [x] 1.1 Swap the `MouseScrollUp`/`MouseScrollDown` case bodies in `internal/tui/taskview/tasklist.go`'s `MouseHandler`.
- [x] 1.2 Swap the `MouseScrollUp`/`MouseScrollDown` case bodies in `internal/tui/hera/rail.go`'s `MouseHandler`.
- [x] 1.3 Update `internal/tui/taskview/tasklist_test.go` and `internal/tui/hera/rail_test.go` scroll-direction assertions to match.
- [x] 1.4 Update gotcha bullets in `context/knowledge/gotchas/tasklist-ui.md` and `context/knowledge/gotchas/hera-view.md`.
- [x] 1.5 Run `make pre-pr` (or the individual build/vet/fmt-check/lint-pr/test gates) and confirm green.
- [x] 1.6 Archive this change: `openspec archive fix-rail-scroll-direction` so the base specs reflect the corrected direction atomically.
