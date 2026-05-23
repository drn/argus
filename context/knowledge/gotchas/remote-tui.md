# Remote TUI (--remote URL)

Non-obvious invariants for the `argus --remote URL --token TOKEN` mode added in PR for branch `argus/enable-the-ability-to-launch`. The TUI process runs locally; persistence + sessions live on a remote daemon. Transport is HTTP REST + SSE; no Unix socket, no local SQLite.

## Architecture

- **`internal/apiclient`** — typed Go HTTP client; one `*http.Client` per `*Client`, Bearer auth, JSON. Implements `agent.SessionProvider` via `Provider` + `Session`. SSE stream feeds a local 256 KiB `RingBuffer`; writes go through `POST /api/tasks/{id}/input`.
- **`internal/apistore`** — implements `internal/tui/store.Store` via the apiclient. Config snapshot is cached and refreshed on a 30 s ticker (`Store.RefreshConfig`) — never burn a request per UI tick.
- **`internal/tui/store`** — narrow interface extracted from the methods the TUI actually calls on `*db.DB`. `*db.DB` satisfies the interface implicitly. The `store/assert_test.go` compile-time assertion catches signature drift.

## Key invariants

- **Compile-time assertions exist in two places: `tui/store/assert_test.go` and `apistore/store.go`. Adding a Store method MUST update both implementations.** Otherwise *db.DB or *apistore.Store silently fails the build at one of the two assertions, not both.
- **Remote-only `--token` is required.** `runRemoteTUI` exits before tcell takes over if the token is empty or the server returns 401 — `apiclient.IsUnauthorized` is the dedicated predicate. Don't string-match the error.
- **Four TUI sites type-assert `a.db.(*db.DB)` and gracefully degrade in remote mode**: new task creation, fork, schedule fire, prune-completed. They surface a status-bar error pointing to the REST equivalent. These ops only work locally because they shell out to git/PTY directly; in remote mode the user must hit the REST endpoint from the daemon's host or via the PWA's New Task form (server-side runs `agent.CreateAndStart`).
- **Backend writes are master-only.** `apistore.SetBackend` POSTs first then falls back to PUT on conflict. The same is true of `SetProject`. If the operator's token is a device token (PWA share), backend/project CRUD will 403 — `Store.SetProject` returns the apiclient error verbatim, the Settings tab surfaces it in the status bar.
- **`apistore.DeleteMessagesForTask` returns an error** because no REST endpoint exposes it today. Archive cleanup covers most callers (the server-side archive handler fires the same DB call). If you need explicit message purge over remote, add `DELETE /api/tasks/{id}/messages` and wire it in `apistore` before relying on that code path.
- **The `cmd/argus/remote.go` config refresher runs every 30 s.** If a config value mutates on the server (e.g. someone adds a project from the PWA), expect up to a 30 s lag before the remote TUI sees it. Don't shorten this without confirming the round-trip cost — Config() is read on every drawTaskRow and every Settings refresh.
- **`apiclient.Provider.OnSessionExit` callback signature is intentionally a near-mirror of `daemon.ExitInfo`** so `tui.App.HandleSessionExit` works identically across local and remote. If you add a field to `daemon.ExitInfo`, add it to `apiclient.SessionExitInfo` too.
- **`AddWriter` / `AddWriterFrom` / `AddWriterFromTolerant` / `RemoveWriter` are no-ops on `apiclient.Session`** — same contract as the daemon-client `RemoteSession`. The TUI reads the ring buffer directly via `RecentOutput*`; fanout doesn't happen client-side.
- **`apiclient.Session.IsIdle` and `Session.PTYSize` block on HTTP RTT.** Don't call them from the tview main goroutine — same rule as the daemon-client `SessionHandle` (see `gotchas/daemon-rpc.md`). Wrap in a goroutine + `QueueUpdateDraw`.

## Endpoint surface added for the TUI store adapter

The PWA uses lossy `taskJSON` (drops `SessionID`, `DependsOn`, `BaseBranch`, `Result`, `PlanSlug`, `AgentPID`, `Pinned`, `StartedAt`/`EndedAt`). The TUI needs full model fidelity, so phase 3 added "raw" endpoints alongside the lossy ones:

- `GET /api/tasks-raw` — all tasks as full `model.Task`
- `GET /api/tasks/{id}/raw` — one task as full `model.Task`
- `PUT /api/tasks/{id}/raw` — overwrite (master-only)
- `POST /api/tasks-raw` — insert (master-only; rarely used — prefer `POST /api/tasks` for fresh tasks)
- `GET /api/schedules/{id}/raw` — full `model.ScheduledTask`

Phase 2 added:
- `POST/PUT/DELETE /api/backends/{name}` — backend CRUD
- `GET /api/config` — full `config.Config` snapshot (master-only)
- `GET /api/sessions/state` — runner's running/idle lists
- `GET /api/sessions/{id}/pending-restart` — runner's kick-restart flag

If you add a TUI method that needs a new endpoint, follow the same pattern: write the apistore method, add the endpoint to `internal/api/routes.go`, write the handler in `internal/api/handlers.go` (or a topical file), add the apiclient wrapper.

## What doesn't work yet

- Daemon-admin actions: `Settings → Update Argus`, `Restart Daemon`, `Install / Uninstall LaunchAgent`. Conceptually meaningless from a remote process — these manage the OS install on the daemon's machine, not the client's. Phase 6 follow-up: hide them in the UI when `App.db` is `*apistore.Store`.
- `POST /api/tasks` multipart attachments via remote TUI. The TUI's new-task form is local-only today (type-asserts to `*db.DB`); when remote-mode new-task creation is wired, multipart uploads should round-trip through `c.do(ctx, "POST", "/api/tasks", multipartBody, multipartContentType)`.
- `agent.CreateAndStart`'s callback hooks (`OnWorktreeCreated` for fork-context-file writes) — these run in the daemon's process. The TUI's fork flow needs to be redesigned around `POST /api/tasks/{id}/fork` for remote mode.
