## 1. Batch the needs-input/content-idle fleet scan

- [x] 1.1 Add `needsInputScanBatchSize` constant and `App.needsInputScanCursor`
  field.
- [x] 1.2 Add `needsInputScanBatch`: no-op predicate at/below the threshold,
  stable sorted rotation-batch assignment above it.
- [x] 1.3 Wire `reuseCached` into `detectNeedsInputSticky`'s 3 pass call sites
  (content-stability, resumed-activity, settlement), replacing the direct
  `logUnchanged` calls. Gate the batching check BEFORE `logUnchanged`'s
  Stat()-recording side effect so a skipped tick never poisons the next
  tick's dirty-check baseline.
- [x] 1.4 Ensure a session with no prior raw signal is always treated as due
  (first-observation never delayed).

## 2. Tests

- [x] 2.1 A fleet at the batch-size threshold scans every session every tick
  (no behavior change for the common case).
- [x] 2.2 A fleet above the threshold still advances every session's
  tick-counters every tick under static content.
- [x] 2.3 A change to an out-of-batch session is caught within one full
  rotation, never permanently lost.
- [x] 2.4 Full existing `TestDetectNeedsInputSticky_*` / `TestContentIdleSignalOf`
  / `TestRefreshTasksWithIDs_*` suites pass unmodified (regression net for the
  correctness-critical state machine).

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/ui-threading.md`
  (or a new scaling-focused file) documenting the batching contract and the
  `logUnchanged`-ordering pitfall found during implementation.

## 4. Follow-up (tracked, not in this change)

- [ ] 4.1 Scope `spinnerLoop`'s forced-redraw gate to visible/active-tab
  content instead of the whole fleet.
- [ ] 4.2 Move `refreshTasksWithIDs`'s heavy computation off the tview main
  goroutine.
