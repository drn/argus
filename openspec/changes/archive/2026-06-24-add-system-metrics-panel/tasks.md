## 1. sysmetrics package

- [x] 1.1 Add `github.com/shirou/gopsutil/v4` (current release) to `go.mod`;
      `go mod tidy`.
- [x] 1.2 `internal/sysmetrics/collector.go`: `Snapshot` struct (JSON-tagged) with
      CPU%, `LoadAvg [3]float64`, mem (total/used/available + percent), swap
      (total/used + percent), disk (total/used/free + percent + path), process RSS,
      uptime seconds, `SampledAt`, and a per-group availability flag
      (`LoadAvail`/`DiskAvail`/…) so an unsupported metric degrades to unavailable.
- [x] 1.3 `Collector` with `Latest() Snapshot` (mutex-guarded), `loop(ctx)` on a
      `time.Ticker`, and `Close()`. Sample: CPU via `cpu.Percent(0,false)` (primed),
      mem via `mem.VirtualMemory`/`mem.SwapMemory`, disk via `disk.Usage(dataDir)`,
      load via `load.Avg` (tolerate ErrNotImplemented → LoadAvail=false), proc RSS
      via `process.NewProcess(os.Getpid())`, uptime via `host.Uptime`. Inject the
      sampler func for testability.
- [x] 1.4 `uxlog.Log("[sysmetrics] ...")` on start, rate-limited sample errors, stop.
- [x] 1.5 Tests (`internal/sysmetrics/collector_test.go`): zero-value before first
      sample; injected sampler populates `Latest()`; an unavailable metric sets the
      flag without erroring; `Close()` stops the loop.

## 2. API wiring + endpoint

- [x] 2.1 `internal/api/server.go`: add `metrics *sysmetrics.Collector` field;
      construct it in `New(...)` with `db.DataDir()` as the disk path; start it in
      `ListenAndServe` (not `New`, so unit tests driving handlers via `routes()` don't
      spin up the real-syscall sampler); stop it in `Shutdown(...)` (alongside the
      `stopCh` close).
- [x] 2.2 `internal/api/metrics.go`: `handleSystemMetrics` reads `s.metrics.Latest()`,
      adds live `running, idle := s.runner.RunningAndIdle()`, marshals a response
      struct, `writeJSON(w, http.StatusOK, resp)` — mirroring `handleStatus`.
- [x] 2.3 `internal/api/routes.go`: register
      `mux.HandleFunc("GET /api/system-metrics", s.handleSystemMetrics)` near the
      other auth-protected GETs.
- [x] 2.4 Tests (`internal/api/metrics_test.go`): authed GET → 200 + well-formed JSON
      (session counts reflect runner state); unauthed GET → 401.

## 3. SPA System panel

- [x] 3.1 `internal/api/static/index.html`: add a `System` `.settings-section` to the
      Settings view with rows for CPU% + load, memory + swap (bars), disk (bar +
      path), Argus RSS, uptime, and active/idle sessions; reuse `.settings-section`/
      `.settings-row` styling and add a small CSS meter/bar.
- [x] 3.2 `loadSystemMetrics()` fetches `/api/system-metrics` via the `api()` helper
      and renders (bytes→GiB + percent→bar-width helpers; "—" when a field is
      unavailable). Call it once inside `loadSettings()`.
- [x] 3.3 Start `setInterval(loadSystemMetrics, 2000)` when entering the Settings tab
      and clear it on tab-leave (track the handle; clear in the tab-switch path where
      `loadSettings()` is gated on `tab === 'settings'`).
- [x] 3.4 Bump `SW_VERSION` in `internal/api/static/sw.js`.

## 4. Docs + knowledge

- [x] 4.1 README Reference appendix: add `/api/system-metrics` to the REST endpoints
      table (in place; no top-half edit).
- [x] 4.2 `context/knowledge/gotchas/web-remote.md`: add a bullet — System panel polls
      only while the Settings tab is visible (interval cleared on tab-leave to avoid
      orphan timers); server samples CPU on its own ticker and caches; per-field
      availability flags drive the "—" fallback. Bump the bullet count in
      `context/knowledge/index.md`.

## 5. Gate + apply

- [x] 5.1 `make pre-pr` passes clean (target ≥95% on `internal/sysmetrics` and touched
      `internal/api`).
- [x] 5.2 Archive this change in-PR: merge the deltas into
      `openspec/specs/rest-api/spec.md` and `openspec/specs/mobile-pwa/spec.md`, and
      move this folder to
      `openspec/changes/archive/<date>-add-system-metrics-panel/`.
