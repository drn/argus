## 1. Shared tail view

- [ ] 1.1 Read `context/knowledge/gotchas/events.md` (the needs-input entries, especially `dedupe-redundant-needsinput-reads` and `add-needs-input-fleet-batching`), `gotchas/ui-threading.md` and `context/knowledge/testing.md`.
- [ ] 1.2 Add `agent.TailView` with a test first: one render across all detectors (use a test-visible render count), no render from a raw-first detector on a raw hit, and separate windowed and whole-buffer stripped text.
- [ ] 1.3 Turn `DetectNeedsInputScreen`, `AwaitingInputFingerprint`, `ParkedSelectionSignal` and `ContentIdleFingerprint` into wrappers over the view, with signatures unchanged. Existing tests must pass untouched.
- [ ] 1.4 Add a test that view results match independent calls for every fixture in `needsinput_realbytes_test.go` and its testdata.

## 2. Scan changes

- [ ] 2.1 Move the idle pass after the `reuseCached` closures, calling `needsInputScanBatch` exactly once per scan and keeping idle results first in `fresh`. Test: replay on unchanged log, fresh read on first idle tick, fresh read on a changed log, and a stays-flagged run.
- [ ] 2.2 Skip archived sessions in the idle pass. Test that the output is the same as before.
- [ ] 2.3 Use one view per session per scan across every pass, and have the settlement pass reuse the idle pass's value. Test the read and render counts for each row of the work table in `design.md`.
- [ ] 2.4 Carry the old stat forward for not-due sessions. Add a test with a fleet above 24: an unchanged session isn't re-read when due, and a change during rotation is caught. Confirm the new test fails before the fix.
- [ ] 2.5 Route `ContentIdle`'s fallback through one view, so an archived session costs one render, not two.
- [ ] 2.6 Reuse the scan's `working` reading in `autoClearBlockedHeraRoles` when one exists, and test both branches.
- [ ] 2.7 Confirm all existing `detectNeedsInputSticky` tests pass with no edits to their assertions, and that no `agent.*Tick` function changed.
- [ ] 2.8 Add `uxlog` lines where skipped work could otherwise be silent, with a consistent `[needsinput]` prefix, without logging per session per tick.

## 3. Measure and report

- [ ] 3.1 Run `make pre-pr`, commit and push with `iris_push`. Build a binary from this branch outside the repository, then send `hera_send(status="blocked")` with its path. Do not deploy.
- [ ] 3.2 Repeat the capture from `repaint-panes-on-output/design.md` using `argus-latency`.
- [ ] 3.3 Record the results in `measurements.md`, and copy them to "PR 2 measurements" in `move-needs-input-scan-off-ui-thread/design.md`.
- [ ] 3.4 Report the stall numbers to the coordinator for Aaron. Do not send `status=done` until Aaron has seen them, because that status starts the next change.

## 4. Documentation and ship

- [ ] 4.1 Update the related bullets in `gotchas/events.md` and `context/knowledge/index.md`. Include the out-of-rotation stat bug and why two stripped forms exist.
- [ ] 4.2 Confirm by diff that `internal/daemon/surface.go` and every file in its path lists are untouched, and that `TestSupervisorSurfaceDigest` passes.
- [ ] 4.3 Run `make pre-pr` in full.
- [ ] 4.4 Archive only this change in the same PR (`openspec archive reduce-needs-input-scan-cost`).
- [ ] 4.5 Push with `iris_push` and open the PR with `iris_gh_pr_create`, including the measurements and stating that web and macOS are unaffected and that no supervisor restart is needed.
