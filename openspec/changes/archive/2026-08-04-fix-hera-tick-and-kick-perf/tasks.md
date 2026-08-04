## 1. Kick debounce (Fix 1)

- [x] 1.1 Add `KickDebounce` constant (300ms) and a `kickPending{taskID, cols, deadline}` type in `internal/tui/hera/panes.go`.
- [x] 1.2 Add `HeraPage.coordKickPending`/`agentKickPending` fields and a `kickNow func() time.Time` clock seam (defaults to `time.Now` in `NewHeraPage`), plus a `SetKickClock` test setter mirroring `hera.Refresher.SetNow`.
- [x] 1.3 Rewrite `maybeKickPaneRerender` to arm-then-fire: a newly-bound task (vs. `pending.taskID`) arms a fresh deadline instead of kicking immediately; a later call past the deadline for the SAME task fires the kick via the existing `kickRerender` callback, unchanged otherwise.
- [x] 1.4 Update the four `Draw` call sites to pass the corresponding pending struct.

## 2. Tests for the kick debounce

- [x] 2.1 Update `TestPanes_DrawInvokesRerenderKicker` (semantics changed: the FIRST Draw after a bind no longer fires immediately) to use the clock seam: first Draw arms but does not kick; advancing the clock past the dwell and drawing again fires exactly once; a further Draw does not re-fire.
- [x] 2.2 New test: a rebind to a DIFFERENT task before the dwell elapses never kicks the first task (the kick-storm regression case — simulates a fast rail traversal across 3+ rows, none within the dwell).
- [x] 2.3 New test: a genuine dwell-and-stay (same task bound across multiple Draws, clock advanced past the dwell) fires exactly once.
- [x] 2.4 New test: unbind clears the effective pending state (rebinding to the SAME task after an unbind mid-dwell re-arms rather than firing early from stale state) — covers Decision 2's reasoning directly rather than just asserting it in prose.

## 3. Tick change-detection gate (Fix 2)

- [x] 3.1 `internal/db`: add `DB.DataVersion() (int64, error)` (`PRAGMA data_version` passthrough).
- [x] 3.2 `internal/tui/hera`: add unexported `dataVersioner` interface + `HeraPage.dbFingerprint() (int64, bool)` type-asserting `p.reader`.
- [x] 3.3 `internal/tui/hera`: add `HeraPage.SetTasks([]*model.Task)`; `BuildModel` accepts a tasks parameter, falling back to `r.Tasks()` when not supplied (nil).
- [x] 3.4 `internal/tui/hera`: add `HeraPage.shouldRebuild() bool` (first-ever call, OR fingerprint changed/unsupported, OR any of the 4 runtime maps changed via `maps.Equal` against the last-rebuild snapshot) and `markRebuilt()` (snapshot fingerprint + the 4 maps by reference — safe because every producer always reassigns a fresh map rather than mutating in place, confirmed by reading all 4 setters).
- [x] 3.5 `doRefresh` calls `shouldRebuild()` first; skips (with a `uxlog.Log("[hera] ...")` noting the skip, per the CLAUDE.md silently-skipped-work logging rule) when false, else proceeds as today and calls `markRebuilt()` at the end.
- [x] 3.6 `internal/tui/hera/page.go`: `HeraPage.Refresh()` (the general "force it now" primitive already used by tab-entry, `heraRefresh`, and tests) calls `InvalidateChangeGate()` before flushing, so its own doc contract ("forces an immediate rebuild") holds regardless of caller — NOT scoped to `heraRefresh` alone (see design.md Decision 5 for why the first, narrower attempt broke an existing test).
- [x] 3.7 `internal/tui/app.go`: feed `HeraPage.SetTasks(a.tasks)` alongside the existing `SetNeedsInput`/etc. calls in `refreshTasksWithIDs`.

## 4. Tests for the change-detection gate

- [x] 4.1 `shouldRebuild`/`markRebuilt` table tests: first call always true; unchanged fingerprint + unchanged maps → false; changed fingerprint → true; each of the 4 maps individually changed → true; unsupported fingerprint (fake reader without `DataVersion`) → always true.
- [x] 4.2 `doRefresh` integration test (`TestHeraPage_DoRefresh_SkipsRebuildWhenQuiescent`): two calls with nothing changed → the rail's rebuilt model keeps the coordinator role's stale `NeedsInput=false` (no rebuild happened); a call after `SetNeedsInput` with a genuinely different set → the model reflects `NeedsInput=true` on the very next call.
- [x] 4.3 `DB.DataVersion` test (`TestDB_DataVersion`, `internal/db`): value changes after a write from a SECOND connection to the same file; stays stable across repeated reads with no writes; a write through the SAME connection does NOT change what that connection reads back (mirrors the cross-connection experiment from design.md, made a permanent regression test — `t.TempDir()`-backed, no real `~/.argus/`).
- [x] 4.4 Gate-invalidation test: rather than testing `App.heraRefresh` directly (it lives in package `tui`, not `hera`, and would need a full App fixture), tested at the layer `heraRefresh` calls into — `TestHeraPage_InvalidateChangeGate` (unit) and `TestHeraPage_Refresh_ForcesRebuildDespiteSameConnectionBlindSpot` (end-to-end: a same-connection write is confirmed invisible to `dbFingerprint()`, yet `Refresh()` still rebuilds and the rail model reflects the change).
- [x] 4.5 Reader-wrapping tasks-dedup test (`TestHeraPage_SetTasks_AvoidsRedundantFetch`, using a call-counting `HeraReader` wrapper): supplying a snapshot via `SetTasks` keeps the underlying reader's `Tasks()` call count at 1 (paid once, before `SetTasks`); never calling `SetTasks` falls back to the reader's own fetch on every rebuild, unchanged from today.

## 5. Measurement (required by the mission, not optional)

- [x] 5.1 Added a permanent `time.Since` timing around `doRefresh`'s full-rebuild path (`internal/tui/hera/page.go`, logged in the existing `[hera-view] rail refreshed: ...` line only when a rebuild actually happens, plus a `[hera-view] rail refresh skipped: ...` line on the gated no-op path) — kept as a shipped log line, not removed: it's cheap (one `time.Since` per genuine rebuild, never per-tick) and gives an ongoing signal if a future change reintroduces expensive per-rebuild work.
- [x] 5.2 Added `internal/tui/hera/doRefresh_bench_test.go` (`BenchmarkDoRefresh_AlwaysRebuild` / `BenchmarkDoRefresh_SteadyStateGated`), seeding ~900 roles/~900 bindings across 450 archived orchestrators + 1 active one (matching Aaron's reported scale) — a reproducible `go test -bench` measurement rather than a one-off transcript. Results (Apple M5 Max, `-benchtime 30x -benchmem`): **pre-fix** (unconditional rebuild every tick) = 34.3ms, 29.7MB, 132,903 allocs per tick; **post-fix steady-state** (idle, nothing changed) = 844ns, 400B, 12 allocs per tick — ~35,000x reduction in per-tick cost while idle.
- [x] 5.3 Documented in `design.md`'s Open Questions: the fix meaningfully reduces steady-state per-tick cost (measured above) and this is strong evidence of a major GC-pressure contributor (~30MB/s of eliminated allocation churn while idle), but allocation-rate reduction is NOT the same as proof of retained-memory reduction — whether it explains the full reported 59GB RSS is explicitly flagged as unconfirmed, with a named follow-up (dogfood + compare RSS over a comparable multi-hour session) rather than assumed.
- [x] 5.4 Kept: the `doRefresh` timing log (ongoing regression signal, near-zero cost) and the two benchmarks (regression guard, `go test -bench`-only — never runs during plain `go test ./...`, zero CI cost). Nothing purely-diagnostic was added that needed removal before merge.

## 6. Docs and gates

- [x] 6.1 `context/knowledge/gotchas/hera-view.md`: added a bullet for the kick debounce (why it exists, the 300ms constant, the re-arm-on-rebind behavior, and the unbind-clear fix caught by TDD).
- [x] 6.2 `context/knowledge/gotchas/hera-view.md`: added bullets for the tick change-detection gate — the `data_version` same-connection blind spot and why `Refresh()`'s own invalidation (not just `heraRefresh`) closes it, the measured cost reduction, and the explicit non-goals (base task-list reads + archived-row lazy-loading still unconditional/full-cost, named follow-ups).
- [x] 6.3 Updated `context/knowledge/index.md`'s `hera-view.md` row: bullet count 171 → 175, with a summary of the new coverage.
- [x] 6.4 `make pre-pr` confirmed clean: build/vet/fmt-check/lint-pr all green; `vuln` fails only on 3 pre-existing stdlib CVEs (CI `continue-on-error`, unrelated to this diff — confirmed via `.github/workflows/ci.yml`); `test-cover-gate` green at 88.8% (floor 88%) once two pre-existing, documented environmental issues are accounted for: (a) this hera-worker sandbox's own `ARGUS_MODEL`/`ARGUS_TASK_ID`/`ARGUS_ARCHETYPE`/`ARGUS_PROFILE` env vars leak into 2 `internal/agent` profile-env tests (confirmed passing with those unset; CI has no such vars); (b) `TestSmoke_NewTaskFormPaste` is a documented pre-existing `-race` flake (confirmed flaky even in isolation, 4/5 runs passing, unrelated to any file this PR touches). A clean run with the sandbox env excluded passed fully green (exit 0).
- [x] 6.5 `openspec archive fix-hera-tick-and-kick-perf` run in this PR, before merge — archived as `2026-08-04-fix-hera-tick-and-kick-perf`.

## 7. Follow-ups (not in this change — flagged, not silently dropped)

- [x] 7.1 Flagged (proposal.md Impact, design.md Non-Goals): widening the change-detection gate to `refreshTasksWithIDs`'s own base reads (`db.Tasks()`, both `ListMetaByNamespace` calls, `ManagedTaskIDs()`) once this narrower gate is dogfooded. Not implemented in this change.
- [x] 7.2 Flagged (proposal.md Impact, design.md Non-Goals): lazy-loading archived orchestrators/roles in `Rail.buildRows()`'s graph algorithms so an ACTUALLY-needed rebuild is also cheaper, not just less frequent. Not implemented in this change (risk to `rail.go`'s invariant-laden fold logic).
