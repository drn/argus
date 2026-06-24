## Why

Argus retired the legacy `depends_on` DAG (and with it the standalone DAG tab, `/api/dag`, and the SPA DAG view) in favor of Hera coordinator-driven orchestration. The TUI gained a native Hera view as its always-on second tab, but the webapp was left with no orchestration surface at all — the DAG tab and its SPA view were deleted and nothing replaced them. A user on their phone can see the task list but has no way to see which coordinator owns which workers, what each role's status is, or which finished workers are awaiting close-out. This change restores a second tab — "Hera" — to the webapp, mirroring the TUI rail as a read-only roster.

## What Changes

- Add `GET /api/hera`: a read-only orchestration roster returning orchestrators (with `pinned`/`archived` flags) and their coordinator/worker roles, plus hoisted freelance roles. Each role carries its hera status (`idle`/`working`/`blocked`/`done`), bound argus task id/name/status, `live`, and `ready_to_close`. The handler mirrors `hera.BuildModel`'s read logic (freelance hoisting, `ready_to_close` from the `task_meta` sidecar, bound-task status) but emits JSON directly from `*db.DB` so `internal/api` never imports the tview-laden `internal/tui/hera` package.
- Add a **"Hera" tab** to the webapp SPA (second slot, `g` hotkey, help-modal entry). It renders Pinned / Active / Archived orchestrator cards plus a Freelance section, each role row showing a status dot, kind, name, and — when bound — the task name + workflow badge + a ready-to-close pill. Tapping a live role drills into that task's existing terminal detail overlay (`openTaskById`); the roster itself stays read-only.
- Wire the roster into the existing connection lifecycle: the 5s poll tick refreshes the roster (instead of the task list) while the Hera tab is foregrounded, and `loadHera` drives `onConnectSuccess`/`onConnectFailure` + `conn-dot` so the offline screen still works on this tab. The reconnect paths (`retryConnection` and the browser `online` event) also refresh the roster when it is the active tab. Bump `SW_VERSION` since the app shell changed.

## Capabilities

### Modified Capabilities

- `rest-api`: adds the read-only `GET /api/hera` orchestration-roster endpoint (authenticated like every other `/api/*` route).
- `mobile-pwa`: adds the read-only Hera orchestration tab (roster render + live-role drill-in) and folds the Hera tab into the existing poll/offline/reconnect lifecycle.

## Impact

- **New code:** `internal/api/hera.go` (handler + JSON projection), `internal/api/hera_test.go`, `web-tests/tests/hera.spec.ts`.
- **Modified code:** `internal/api/routes.go` (route registration), `internal/api/static/index.html` (tab, view, `loadHera`/`renderHera*`, `switchTab`, poll tick, reconnect paths, `g` hotkey, help modal), `internal/api/static/sw.js` (`SW_VERSION` v58→v59), `cmd/argus-test-server/main.go` (`seedHera` so the PWA spec has a populated roster), `web-tests/tests/hotkeys.spec.ts`.
- **Dependencies:** none added. `internal/api` deliberately does NOT import `internal/tui/hera`.
- **Data:** read-only; no schema changes. Reads existing `hera_*` tables + the `hera` `task_meta` namespace.
