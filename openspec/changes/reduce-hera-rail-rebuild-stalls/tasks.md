## 1. Index

- [ ] 1.1 Add `idx_hera_bindings_role` (non-partial) in schema.go; test the query plan for both cost sums and idempotent creation on an existing DB

## 1b. PR poller no-op skip

- [ ] 1b.1 Add `db.SetMetaBatchIfChanged` (compare and write in one transaction, returns whether it wrote); table tests incl. partial change and new key
- [ ] 1b.2 Switch the PR poller call site only; `res.Merged` notifications unchanged; test that an unchanged poll bumps no `data_version`

## 2. Async rebuild

- [ ] 2.1 Add the `queueUpdate` seam to `HeraPage`; App wires `tapp.QueueUpdateDraw`; nil falls back to inline
- [ ] 2.2 Snapshot (value-copied tasks, cloned maps, pre-build fingerprint, generation) and single-flight build goroutine with one follow-up rerun
- [ ] 2.3 Publish on the UI goroutine: generation check, unchanged-model drop, `SetModel`/`SetPRMeta`/`applySelection`/`markRebuilt(snapshot)`
- [ ] 2.4 Keep forced `Refresh()` synchronous; bump generation on it and on gate invalidation
- [ ] 2.5 Make the tier resolver goroutine-safe (document and test under `-race`)
- [ ] 2.6 uxlog lines: build duration, publish, stale-drop, unchanged-drop

## 3. Tests

- [ ] 3.1 Race regression: mutate tasks/maps on the UI side during a build (`-race`)
- [ ] 3.2 D5 test from real commits: no-op poller commit equal, old-style `SetMetaBatch` identical rewrite equal, real `pr` state flip NOT equal
- [ ] 3.2b Stale-publish rejection (older build finishing after a newer one); write-during-build not lost; unchanged-model drop; single-flight rerun; nil-seam inline fallback
- [ ] 3.3 Existing BUG-069/070/071, sticky reveal, rollup and fingerprint-skip tests stay green against the async path
- [ ] 3.4 Smoke test with the real event loop (`simApp`) for async publish

## 4. Docs and verification

- [ ] 4.1 Gotchas in hera-view.md and ui-threading.md (index root cause, PR-poller write storm, pre-build fingerprint, generation guard, DeepEqual drop)
- [ ] 4.2 `make pre-pr` clean; `/hera-review` and `/hera-review-test-adversary`
- [ ] 4.3 A/B measurement via coordinator (BASE vs rail binaries); PR body with table
- [ ] 4.4 Archive this change in the same PR
