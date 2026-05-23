# Remote TUI via HTTP — unify TUI on REST API

**Date:** 2026-05-22
**Status:** In progress
**Goal:** `argus --remote https://host/` launches the TUI pointed at a remote argus instance. Achieved by collapsing the TUI's dual access pattern (direct `*db.DB` + daemon RPC) onto a single transport: HTTP REST + SSE. Local mode talks to `http://127.0.0.1:7743`; remote mode talks to the given URL. The daemon's HTTP server becomes the universal entry point.

## Why

Today the TUI reaches into local SQLite for 49 distinct call sites and into the daemon's Unix socket for session management. The web PWA already proves a clean REST surface works for the *entire* product. Unifying lets us:

- Ship `--remote` as a first-class deployment mode (Tailscale → MagicDNS → HTTPS).
- Delete one entire transport (TUI-side daemon RPC client).
- Make the TUI and PWA exercise the same code paths — bugs found in one are fixed in both.
- Future-proof for richer remote scenarios (multi-user, hosted argus).

## Performance budget (loopback HTTP vs direct SQLite + Unix RPC)

| Path | Today | After |
|---|---|---|
| `Tasks()` refresh @ 1 Hz | ~1 ms (SQLite in-process) | ~5 ms (loopback HTTP + JSON) |
| Keystroke `WriteInput` | ~0.5 ms (Unix RPC) | ~0.5 ms (loopback POST, keep-alive) |
| Output streaming | Unix socket raw bytes | SSE (HTTP chunked) — already in use by PWA |
| Remote keystroke (Tailscale) | n/a | 5-30 ms — mosh-like, acceptable |

**Net:** negligible local cost. Remote-over-Tailscale is a real, usable feature, not a corner case.

## Architecture

```
Before:                          After:
TUI ──> *db.DB (SQLite)          TUI ──> apiclient.Client ──┐
TUI ──> daemon.Client (Unix)                                 ├──> HTTP (local 127.0.0.1:7743
                                                             │     or remote https://host/)
Daemon ──> agent.Runner          API Server ──> *db.DB + agent.Runner
```

The daemon stays. The Unix-socket RPC also stays for `argus daemon stop/restart` admin only. The TUI never speaks to either directly.

## Decisions

- **Local-mode DB:** Don't open. The daemon is the only process touching SQLite.
- **Auth:** `--token` flag or `ARGUS_TOKEN` env. Local mode auto-reads master token from `~/.argus/data.sql` via a small RO helper (not the full DB).
- **Daemon required for local mode:** If auto-start fails, exit with a clear error. The in-process fallback runner is gone.
- **No backwards compat shims.** Single user, mid-development, breaking changes fine.

## Phases (one commit each)

### Phase 1 — `internal/apiclient` foundation

Create a typed Go HTTP client wrapping every endpoint in `internal/api/routes.go`. One file per surface area (`tasks.go`, `projects.go`, `schedules.go`, etc.). Returns typed structs that match existing response shapes. Bearer auth, JSON, keep-alive, context-aware. Unit tests against `httptest.NewServer(api.Server.routes())`.

### Phase 2 — API gap fills

Add what the TUI needs but isn't exposed yet:
- `POST/PUT/DELETE /api/backends/{name}` — full backend CRUD.
- `GET /api/config` — full `config.Config` snapshot (so we can drop `db.Config()` from TUI).
- `GET /api/sessions/running-and-idle` — exposes `SessionProvider.Running()/Idle()/RunningAndIdle()` for the TUI's status polling.
- `GET /api/sessions/{id}/has-pending-restart` — exposes `HasPendingRestart`.

### Phase 3 — store interface + remove db.DB from TUI

Define `internal/tui/store.Store` interface covering every method TUI currently calls on `*db.DB`. Provide one implementation: `apistore.Store` (HTTP-backed). Swap all 49 callsites. Update `tui.New` signature.

### Phase 4 — HTTP-backed `SessionProvider`

Replace daemon-client `SessionProvider` impl with an `apiclient`-backed one. `Start`/`Stop`/`Resize`/`WriteInput` are REST calls; `Get(id)` returns an `httpSession` with a local ring buffer fed by SSE `/stream`. Same writer-fanout pattern as today's `RemoteSession`.

### Phase 5 — wire `--remote` flag in `cmd/argus/main.go`

```
argus                          # local: auto-start daemon, baseURL=http://127.0.0.1:7743
argus --remote URL --token T   # remote: no daemon, baseURL=URL
```

Hide daemon-admin actions (install/uninstall/restart) when in remote mode.

### Phase 6 — tests + docs

- Unit tests for `apiclient`, `apistore`, HTTP `SessionProvider`.
- Smoke test: spin up real API server in `httptest`, point `tui.App` at it, exercise list/attach/input.
- README reference appendix: document `argus --remote URL`.
- CLAUDE.md: update Architecture section to reflect single-transport design.
- gotchas/web-remote.md: add `--remote` mode notes.

## Open items

1. **Connection failure UX.** Remote drops mid-session → status-bar indicator + exponential backoff in `apiclient`.
2. **Session exit notifications.** Daemon `OnSessionExit` push is gone; start with polling `GET /api/tasks` status transitions, add SSE delta stream later if laggy.
3. **TLS verification.** Default to standard verification. Tailscale MagicDNS issues valid LetsEncrypt certs.
4. **Master-only endpoints.** Remote-mode device-tokens can't hit `requireMaster()` endpoints. Document the caveat; settings actions gracefully degrade.

## Effort

4-5 days of focused work. One commit per phase. Final `/pr`.
