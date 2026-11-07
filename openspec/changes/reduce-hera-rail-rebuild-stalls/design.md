## Context

`HeraPage.doRefresh` runs on the tview goroutine: `shouldRebuild` (global `data_version` + four runtime maps), then `BuildModel` (about 25 DB reads plus 2 cost sums per orchestrator and one board read per coordinator), `AnnotateRoles` (tier resolver reads config and disk), a PR-meta read, `rail.SetModel`, `applySelection`.

Evidence (this session, real data):

- **Writer.** A read-only watcher on `~/.argus/data.sql` diffed tables on every `data_version` bump. Every minute at about :43–:46 there are 5–8 bumps 0.4–0.6s apart, each a `task_meta` `pr/state`+`pr/url` rewrite. This is the daemon PR poller (`prPollInterval` = 60s, batched per repo group). Most rows rewrite `none`/`''` with only `updated_at` changed. A `config` row is also rewritten every 30s, and `events`/`api_tokens`/`plugin_mcp_tools` bump occasionally. PR 2's ux.log shows fingerprint 33→38 then 38→41 with 3 rebuilds each, matching.
- **Cost.** `BenchmarkRealBuildModel` against a copy of the real DB: 87.5ms/op. pprof: 80% in `SumNukedHeraRolesCostByOrchestrator` (45%) and `SumHeraRoleCostAccruedByOrchestrator` (35%), almost all SQLite page reads. `EXPLAIN QUERY PLAN` shows `SCAN hb` for both: `hera_bindings` has only partial indexes on `role_id` (`WHERE ended_at IS NULL`), which cannot serve ended bindings. 412 orchestrators × 2837 wide rows. Adding `CREATE INDEX ON hera_bindings(role_id)` gives 12.2ms/op (no `ANALYZE`).

## Goals / Non-Goals

**Goals:**

- No rail rebuild work on the tview goroutine on the tick path.
- Writes the rail does not render cause no UI-goroutine work.
- Identical rail behaviour: selection, fold state, sticky reveal, needs-input rollup, BUG-069/070/071.

**Non-Goals:**

- Skipping no-op `pr/state` rewrites in the daemon PR poller. It would also cut `data_version` churn for every TUI view, but it is a separate daemon change. Named follow-up.
- Batching or caching board reads, or rewriting `BuildModel`'s query shape. After the index it is 12ms and off-thread.
- Other tabs' refresh paths (task list reads), already gated by `add-tasks-fetch-dirty-check`.

## Decisions

**D1. Add the index (root cause of the cost).** `CREATE INDEX IF NOT EXISTS idx_hera_bindings_role ON hera_bindings(role_id)` in schema.go next to the other `hera_bindings` indexes. Alternative: rewrite both sums as one grouped query for all orchestrators. Rejected as primary: it changes query contracts used by REST too, and the index fixes every role-keyed aggregate, including future ones.

**D2. Build off the UI goroutine from an immutable snapshot.** On the UI goroutine the tick path (`ScheduleRefresh`) takes a snapshot: `[]model.Task` value copies (`model.Task` has no slice/map/pointer fields), cloned runtime maps, the current data-version fingerprint, and a new generation number. One goroutine (single-flight; a request arriving while one runs sets a "rerun" flag) runs `BuildModel`, `AnnotateRoles` and the PR-meta read against the DB reader (mutex-guarded). The result is published with `QueueUpdate` on the UI goroutine, which checks the generation, then does `SetModel`, `SetPRMeta`, `applySelection`, `markRebuilt(snapshot)`. No `*model.Task` or map ever crosses threads. This is the structure the reverted `a.tasks` attempt lacked. Requirements: the tier resolver reads only `a.db.Config()` and disk, so it is goroutine-safe; a test asserts it under `-race`.

**D3. Fingerprint is captured before the build.** `markRebuilt` records the snapshot's fingerprint and maps, not a re-read after the build. A write that lands during the build therefore differs from the recorded fingerprint and triggers another rebuild, so nothing is lost.

**D4. Generation guard.** Every forced refresh, gate invalidation and model replacement bumps the generation. A publish whose generation is older than current is dropped and logged (`stale-drop`). The forced `Refresh()` stays synchronous on the UI goroutine (about 12ms after D1) so tab entry and mutations show their result immediately; it bumps the generation, discarding any in-flight build.

**D5. Publish-if-changed (the tighter gate).** Before publishing, compare the built model to the last published one (`reflect.DeepEqual` over the model value, which holds no funcs; checked in a test). Equal: drop silently (`unchanged-drop`), leaving the rail, cursor and panes untouched, but still `markRebuilt` so the gate advances. This makes the 5–8 PR-poller commits per minute free for the UI goroutine. Alternatives rejected: per-table change tracking or triggers (needs an audit of every write site, the exact trap `add-tasks-fetch-dirty-check` fell into: 4 sites missed); a TTL (unsafe, see ui-threading.md, the reverted cache); comparing only a subset of tables via `data_version` (not available per table).

**D6. Test seam.** The page gets a `queueUpdate func(func())` (App wires `tapp.QueueUpdateDraw`). When unset (unit tests, bare `New()`), the async path degrades to today's synchronous inline behaviour, same convention as the other tview seams.

## Risks / Trade-offs

- [Rail lags a write by one build, about 12ms plus a tick] → Forced paths stay synchronous; ticks were already up to 1s granular.
- [Stale publish applies old data after a mutation] → D4 generation guard plus a stale-publish test.
- [Background reads contend on the DB mutex with UI reads] → each query holds the mutex briefly; after D1 the longest hold is milliseconds, versus 88ms of back-to-back holds today.
- [DeepEqual cost] → proportional to model size (hundreds of rows), on the background goroutine, only when the gate fired.
- [Cursor/sticky-reveal semantics drift] → the publish path calls the unchanged `SetModel`+`applySelection`; existing BUG-069/070/071 tests run against it, plus a test publishing an unchanged model asserts no `SetModel`.

## Migration Plan

Schema change is an idempotent `CREATE INDEX IF NOT EXISTS`; it is built on first open by the daemon or TUI. Rollback: revert; the index is harmless.

## Open Questions

None blocking. If the A/B shows residual stalls from `Draw` itself, that is a separate change.
