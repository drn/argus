## Why

Argus orchestrates many concurrent Claude/Codex agent PTYs, git worktrees, and a
SQLite DB on the host machine. When the parent system gets CPU-, memory-, or
disk-bound, the operator has no visibility from the webapp — they only find out
when agents slow to a crawl or a worktree write fails for lack of disk. The web
Settings tab shows configuration but nothing about the load Argus is placing on
the box it runs on.

There is no system-metrics collection anywhere in the codebase today (no
gopsutil, no `runtime.MemStats`, no `/proc` reads), and no live-updating panel in
the SPA Settings view — settings are fetched once on tab open.

## What Changes

- **New `internal/sysmetrics` package** — a self-contained `Collector` that samples
  host metrics on its own ~2s ticker and caches the latest `Snapshot` behind a
  mutex (so CPU% deltas are accurate and request cadence is decoupled from
  sampling). Backed by `github.com/shirou/gopsutil/v4` (pure-Go, CGO-free — matches
  the repo's no-CGO build, which already avoids CGO via `modernc.org/sqlite`).
  Each field carries an availability flag so a metric a platform can't supply
  degrades to "unavailable" rather than failing the whole snapshot.

- **New `GET /api/system-metrics` endpoint** — auth-protected like `/api/status`,
  returns the cached snapshot plus live active/idle agent-session counts from
  `runner.RunningAndIdle()`. The collector is constructed in `api.New` and stopped
  in `Shutdown`.

- **New "System" panel in the SPA Settings tab** — shows CPU% + 1/5/15-min load
  average, memory + swap (used/total + percent bars), disk usage of the `~/.argus`
  filesystem (worktrees + DB), Argus's own process RSS, host uptime, and the
  active/idle session counts. It polls `GET /api/system-metrics` every ~2s **only
  while the Settings tab is visible**, clearing the interval on tab-leave so no
  orphan timer fetches in the background. SPA shell change ⇒ bump `SW_VERSION`.

## Impact

- Affected specs: `rest-api` (ADDED: system-metrics endpoint), `mobile-pwa`
  (ADDED: live system-metrics panel).
- Affected code: new `internal/sysmetrics/`, `internal/api/{server.go,routes.go}`,
  new `internal/api/metrics.go`, `internal/api/static/{index.html,sw.js}`,
  `go.mod`/`go.sum` (add gopsutil/v4).
- Read-only feature: no new mutations, no DB schema change, no new keybinding
  (so the help modal is unchanged), no new MCP tool. The disk metric targets the
  `~/.argus` filesystem specifically, not `/`.
- `make vuln` (govulncheck) runs over the new dependency — pin a current
  gopsutil/v4 release.
