## Why

On the Projects (Hera) tab, typing freezes for about 0.1s roughly every 7 seconds. Measured on the build with `reduce-needs-input-scan-cost` (ux.log, matched 204s windows): 31 of 204 per-second ticks took 80ms or more, and every one matched a `[hera-view] rail refreshed` line within 1ms. The same stalls appear on the build before that change, so they are not caused by it.

Two defects combine. (1) Each rebuild costs about 90–110ms because two per-orchestrator cost queries scan the whole `hera_bindings` table 412 times (no usable index on `role_id`). Measured on a copy of the real database: `BuildModel` takes 88ms, 80% of it in those two queries, and one index brings it to 12ms. (2) The change gate is the global `PRAGMA data_version`, so any write by any process forces a full synchronous rebuild on the tview goroutine. The daemon's PR poller commits one `task_meta` `pr/state`+`pr/url` batch per repo group, about 0.5s apart, every 60s, rewriting unchanged values (`none`) with a new `updated_at`. That is 5–8 commits per minute, hence the bursts of about 3 consecutive slow ticks once a minute.

This supersedes `move-needs-input-scan-off-ui-thread`, which was never implemented: `reduce-needs-input-scan-cost` cut the scan by about 89% (roughly 40ms per 20s), so moving it off-thread would now buy almost nothing and carries a known pointer-sharing race. That change folder is deleted in the same PR.

## What changes

- Add a non-partial index on `hera_bindings(role_id)` so the per-orchestrator cost sums stop scanning the table.
- The tick-driven rail rebuild builds the model on one background goroutine from an immutable snapshot (value-copied tasks, cloned runtime maps, fingerprint captured before the build) and publishes the result to the UI goroutine. A generation counter drops stale publishes.
- A built model equal to the published one is dropped at publish time with no UI work. This replaces "any write rebuilds the rail" with "only writes that change what the rail renders reach the UI goroutine", without auditing every write site.
- The forced `Refresh()` (tab entry, every hera mutation) stays synchronous, so a user action is reflected immediately. It bumps the generation so any in-flight build is dropped.
- uxlog lines for the async path: build duration, publish, stale drop, unchanged drop.

Selection, fold state, sticky reveal, needs-input rollup and the BUG-069/070/071 re-arm semantics do not change: the same `SetModel`+`applySelection` runs on the UI goroutine, only its input arrives later. The rail can lag a DB write by up to one build.

## Capabilities

### New capabilities

None.

### Modified capabilities

- `hera-view`: the tick-driven rebuild runs off the UI goroutine with snapshot, single-flight, stale-drop and unchanged-drop rules; the forced refresh stays synchronous.
- `hera-coordination`: role-keyed binding aggregates must not scan the bindings table (index requirement).

## Impact

- `internal/db/schema.go` (index), `internal/tui/hera/page.go` + `refresher.go` (async build/publish), `internal/tui/app.go` (UI-queue seam wiring, `tierResolver` must be goroutine-safe), tests, `context/knowledge/gotchas/hera-view.md` and `ui-threading.md`.
- Frontend parity: TUI-internal only. No REST, event or wire change, so no web or macOS follow-up.
- Deletes `openspec/changes/move-needs-input-scan-off-ui-thread/`.
