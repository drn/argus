# Tasks: add-web-hera-tab

## 1. Tests

- [x] 1.1 Failing handler tests (`internal/api/hera_test.go`): empty roster; orchestrator with a bound coordinator (status + bound task name/status + `ready_to_close`) plus an unbound worker (kind round-trip, empty task fields); freelance hoisted from an active orchestrator + pin/archive flags; freelance hoist-suppression (archived role in active orch, active role in archived orch both stay nested); all three 500 branches (closed DB / dropped `hera_bindings` / dropped `hera_roles`); auth 401 without token and 200 with.
- [x] 1.2 Playwright specs: `hera.spec.ts` renders the seeded orchestrator + coordinator role (bound task name) and drills into the task detail on role click; `hotkeys.spec.ts` `g` switches to the Hera tab.

## 2. Backend endpoint

- [x] 2.1 Add `internal/api/hera.go`: `heraRoleJSON`/`heraOrchJSON`/`heraJSON` DTOs and `handleHera` building the roster directly from `*db.DB` (no `internal/tui/hera` import). Freelance hoist condition mirrors `BuildModel` exactly; `ready_to_close` from the `hera` `task_meta` namespace; bound-task name/status from the task snapshot; soft-fail on meta/task reads, hard-fail (500) on orchestrators/bindings/roles reads.
- [x] 2.2 Register `GET /api/hera` in `internal/api/routes.go` (inside the authenticated mux).

## 3. Webapp tab

- [x] 3.1 Add the "Hera" tab, `#hera-view` markup + CSS, and `loadHera`/`renderHeraSection`/`renderHeraOrch`/`renderHeraRole` (all user strings via `esc`/`escAttr`).
- [x] 3.2 Wire `switchTab('hera')`, the `g` hotkey, and the help-modal row.
- [x] 3.3 Fold the tab into the connection lifecycle: poll tick → `loadHera` when foregrounded; `loadHera` drives `onConnectSuccess`/`onConnectFailure` + `conn-dot`; reconnect paths refresh the roster when active.
- [x] 3.4 Bump `SW_VERSION` (v58 → v59).

## 4. Docs + harness

- [x] 4.1 README REST table entry for `/api/hera`; gotchas in `context/knowledge/gotchas/web-remote.md`; knowledge index bullet.
- [x] 4.2 `cmd/argus-test-server` seeds a demo orchestrator + bound coordinator role (`seedHera`, idempotent across `/test/reset`).

## 5. Gate

- [x] 5.1 `make pre-pr` green (stdlib-only govulncheck findings are non-blocking per CI policy); coverage ≥ 88% floor.
- [x] 5.2 Reviewed via `/rereview-loop` (3 reviewers, 4 iterations): unanimous APPROVE, 0 blocking / 0 warning, regression risk LOW.
