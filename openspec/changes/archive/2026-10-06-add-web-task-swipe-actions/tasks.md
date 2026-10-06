## 1. Gesture + markup

- [x] 1.1 Wrap each `.task-item` in a swipe container with left/right action layers (CSS + markup in `renderTaskList`).
- [x] 1.2 Delegated touch handlers on `#task-list`: axis lock, threshold commit, snap-back, tap suppression after a swipe.
- [x] 1.3 `setTaskStatus(id, status)` helper: POST `/api/tasks/{id}/status`, optimistic local update, re-render, toast + revert on failure.
- [x] 1.4 Skip swipe handling while the poll re-render would clobber an in-progress drag.

## 2. Housekeeping

- [x] 2.1 Bump `SW_VERSION` in `internal/api/static/sw.js`.
- [x] 2.2 Gotcha note in `context/knowledge/gotchas/web-remote.md` (touch-action / axis lock / click suppression).
- [x] 2.3 Playwright test via `cmd/argus-test-server` harness, if one covers the task list; else document manual verification.
- [x] 2.4 `make pre-pr`; archive this change into `openspec/specs/mobile-pwa/spec.md` before merge.
