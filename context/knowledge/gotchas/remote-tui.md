# Remote TUI (--remote URL)

Non-obvious invariants for the `argus --remote URL --token TOKEN` mode added in PR for branch `argus/enable-the-ability-to-launch`. The TUI process runs locally; persistence + sessions live on a remote daemon. Transport is HTTP REST + SSE; no Unix socket, no local SQLite.

## Architecture

- **`internal/apiclient`** — typed Go HTTP client; one `*http.Client` per `*Client`, Bearer auth, JSON. Implements `agent.SessionProvider` via `Provider` + `Session`. SSE stream feeds a local 256 KiB `RingBuffer`; writes go through `POST /api/tasks/{id}/input`.
- **`internal/apistore`** — implements `internal/tui/store.Store` via the apiclient. Config snapshot is cached and refreshed on a 30 s ticker (`Store.RefreshConfig`) — never burn a request per UI tick.
- **`internal/tui/store`** — narrow interface extracted from the methods the TUI actually calls on `*db.DB`. `*db.DB` satisfies the interface implicitly. The `store/assert_test.go` compile-time assertion catches signature drift.

## Key invariants

- **Compile-time assertions exist in two places: `tui/store/assert_test.go` and `apistore/store.go`. Adding a Store method MUST update both implementations.** Otherwise *db.DB or *apistore.Store silently fails the build at one of the two assertions, not both.
- **Remote-only `--token` is required.** `runRemoteTUI` exits before tcell takes over if the token is empty or the server returns 401 — `apiclient.IsUnauthorized` is the dedicated predicate. Don't string-match the error.
- **New-task creation works in remote mode** via `App.createTaskTransactional`: local (`a.db` is `*db.DB`) runs `agent.CreateAndStart` in-process; remote (`a.db` is `*apistore.Store`) routes through `POST /api/tasks` so the daemon does the worktree + session creation server-side. The remote branch is reached through the structural `remoteTaskCreator` interface (so the `tui` package doesn't import `apistore`). **The REST JSON `createTaskReq` has no `base_branch` or attachment field**, so a base-branch override set in the new-task form is silently dropped over remote (logged via uxlog, not surfaced) and attachments aren't carried — use the daemon-host TUI or the PWA's multipart form for those. The remote branch double-bumps `startGen` (mirroring `CreateAndStart`'s Before/AfterStart hooks) so a concurrent tick doesn't reconcile the new task as "not running" before its SSE stream attaches.
- **Prune-completed DOES work in remote mode** too. When `a.db.(*db.DB)` fails, `pruneCompletedTasks` falls through to `pruneCompletedRemote`, which type-asserts `a.db` to a narrow `remotePruner` interface (`PruneCompleted(ctx) (pruned, worktrees, orphans int, err error)`, satisfied by `*apistore.Store`) and fires `POST /api/maintenance/prune-completed` in a background goroutine — the whole git/PTY cleanup runs server-side on the daemon, so no local shell-out is needed. The result refresh dispatches via `QueueUpdateDraw`. `remotePruner` is deliberately NOT on the `store.Store` interface (its signature differs from `db.DB.PruneCompleted`, and the local flow uses `agent.PrunePrepare` directly), so neither compile-time assertion needs touching. The defensive `!ok` fallback in `pruneCompletedRemote` re-shows the old "requires local mode" error and is only reachable by a store that's neither `*db.DB` nor `*apistore.Store`.
- **Two TUI sites still type-assert `a.db.(*db.DB)` and gracefully degrade in remote mode**: fork, schedule fire. They surface a status-bar error pointing to the REST equivalent. These ops only work locally because they shell out to git/PTY directly (fork additionally needs `OnWorktreeCreated` callbacks that run in the daemon's process); in remote mode the user must hit the REST endpoint from the daemon's host.
- **Backend writes are master-only.** `apistore.SetBackend` POSTs first then falls back to PUT on conflict. The same is true of `SetProject`. If the operator's token is a device token (PWA share), backend/project CRUD will 403 — `Store.SetProject` returns the apiclient error verbatim, the Settings tab surfaces it in the status bar.
- **`apistore.DeleteMessagesForTask` returns an error** because no REST endpoint exposes it today. Archive cleanup covers most callers (the server-side archive handler fires the same DB call). If you need explicit message purge over remote, add `DELETE /api/tasks/{id}/messages` and wire it in `apistore` before relying on that code path.
- **The `cmd/argus/remote.go` config refresher runs every 30 s.** If a config value mutates on the server (e.g. someone adds a project from the PWA), expect up to a 30 s lag before the remote TUI sees it. Don't shorten this without confirming the round-trip cost — Config() is read on every drawTaskRow and every Settings refresh.
- **`apiclient.Provider.OnSessionExit` callback signature is intentionally a near-mirror of `daemon.ExitInfo`** so `tui.App.HandleSessionExit` works identically across local and remote. If you add a field to `daemon.ExitInfo`, add it to `apiclient.SessionExitInfo` too.
- **`AddWriter` / `AddWriterFrom` / `AddWriterFromTolerant` / `RemoveWriter` are no-ops on `apiclient.Session`** — same contract as the daemon-client `RemoteSession`. The TUI reads the ring buffer directly via `RecentOutput*`; fanout doesn't happen client-side.
- **`apiclient.Session.IsIdle` and `Session.PTYSize` block on HTTP RTT.** Don't call them from the tview main goroutine — same rule as the daemon-client `SessionHandle` (see `gotchas/daemon-rpc.md`). Wrap in a goroutine + `QueueUpdateDraw`.
- **`a.runner.(clipboardAccessor)` must hold for BOTH transports or ctrl+y silently dies on one.** The agent-staged clipboard feature (ctrl+y copy) is gated by a type assertion on the runner: `*dclient.Client` (local) and `*apiclient.Provider` (remote) must each expose `ClipboardGet(taskID) (string, bool)` + `ClipboardClear(taskID) error`. The assertion failing degrades to a no-op (the in-process `agent.Runner` fallback case), so a missing method on `Provider` makes ctrl+y look "broken" only in `--remote` mode with no error. The `Provider` methods proxy to `Client.GetClipboard`/`ClearClipboard`; **presence is keyed on non-empty text, NOT the dead `ClipboardEntry.Present` field** (the server's `clipboardGetResp` only carries `text` and returns 204 — decoded as empty text — when nothing is staged; downstream TUI consumers treat empty text as "no payload" anyway). Compile-time assertions for both transports live in `tui/clipboard_test.go`.
- **`ui.*` display preferences (`ui.theme`, `ui.spinner`, `ui.show_elapsed`, `ui.show_icons`, `ui.default_agent_zoom`) are NOT persistable in `--remote` mode.** `apistore.SetConfigValue` has no case for any `ui.*` key (only sandbox/kb/api/defaults map to `/api/settings`), so toggling one in the Settings tab returns the "no remote handler" error (logged, not surfaced), the local `SettingsView` field flips cosmetically, and the next 30 s `RefreshConfig` reverts it to the server's value. These settings are intentionally TUI-local and read from the server's cached config; this is a shared limitation, not specific to `ui.default_agent_zoom`. If you ever need a `ui.*` setting to be remote-settable, add it to `apiclient.SettingsUpdate` + the `/api/settings` handler AND the `apistore.SetConfigValue` switch together.

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
- `POST /api/tasks` multipart attachments via remote TUI. Remote-mode new-task creation is wired (JSON only — name/prompt/project/backend); attachments and base-branch override are NOT carried because `apiclient.CreateTaskReq` / the JSON `createTaskReq` have no field for them. To support uploads over remote, add a multipart round-trip through `c.do(ctx, "POST", "/api/tasks", multipartBody, multipartContentType)` and feed the new-task form's attachments into it.
- `agent.CreateAndStart`'s callback hooks (`OnWorktreeCreated` for fork-context-file writes) — these run in the daemon's process. The TUI's fork flow needs to be redesigned around `POST /api/tasks/{id}/fork` for remote mode.
