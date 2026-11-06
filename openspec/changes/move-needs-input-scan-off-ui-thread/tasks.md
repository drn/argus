## 1. Before starting

- [ ] 1.1 Confirm with the coordinator that Aaron has seen the `reduce-needs-input-scan-cost` stall numbers and wants this to proceed. Fill in "PR 2 measurements" in `design.md` and revise the design if "What could change" applies. Update the deltas on the same turn.
- [ ] 1.2 Read the two reverted attempts in `context/knowledge/gotchas/ui-threading.md`, the needs-input entries in `gotchas/events.md`, and `context/knowledge/testing.md`.
- [ ] 1.3 Re-verify the ownership table in `design.md` with a grep over `internal/tui`, including anything added since. Stop and tell the coordinator if `Draw()` or a widget reads a scan-owned field.

## 2. Separate the scan from the App's shared state

- [ ] 2.1 Add `needsInputScanInput` and `runNeedsInputScan`, moving the body of `detectNeedsInputSticky` with no logic changes other than reading `archived` and `runner` from the input. `detectNeedsInputSticky` builds an input and calls it. All existing tests pass with their assertions unchanged.
- [ ] 2.2 Move `ContentIdle` and `autoClearBlockedHeraRoles` into the same scan step, since they share `needsInputScreen` and `needsInputRawSignals`. Return the content-idle and sustained-active lists in a result struct.
- [ ] 2.3 Add a helper that applies a result on the UI goroutine (scraper set, bridge override, list and rail setters, attention bar). Use it for inline and published results.

## 3. Background scanner

- [ ] 3.1 Add the scanner goroutine and single-slot input with tests: newest input wins, one scan at a time, panic recovery, and no publish after `tickDone`.
- [ ] 3.2 Have the scanner track its own previous scraper result for sticky carry-forward. Test that a flag cleared in scan N isn't restored by a snapshot taken during scan N. Confirm the test fails if the snapshot's value is used.
- [ ] 3.3 Start the scanner and set `needsInputScanAsync` in `Run`. Keep inline scanning when it isn't set.
- [ ] 3.4 In async mode, have `refreshTasksWithIDs` submit a snapshot on the tick path and apply the last result on every other path. Test that a non-tick refresh does no log read and advances no counter.
- [ ] 3.5 Log `[scan]` on panic and on scans over 1 second.

## 4. Proof it works

- [ ] 4.1 Smoke test on a SimulationScreen with the real event loop and a seam that blocks the scan on a channel: a key is handled and a frame is drawn while the scan is blocked.
- [ ] 4.2 Add a `-race` test that mutates task records and swaps the runner on the UI goroutine during a scan.
- [ ] 4.3 Add a test that a background scan and a synchronous call return the same result over the real-bytes fixtures.
- [ ] 4.4 Run the `internal/tui` package repeatedly with `-race` to surface timing-dependent failures.

## 5. Measure

- [ ] 5.1 The coordinator arranges a relaunch of a binary from this branch, without deploying it to the shared daemon (see "Before capture" in `repaint-panes-on-output/design.md`).
- [ ] 5.2 Repeat the capture and record it in `measurements.md`. Goroutine 1's stack still shows `onTick` for the remaining closure work, so the stall method stays comparable.
- [ ] 5.3 Compare against the target of a median stall below 30ms and report to the coordinator. If needs-input flags are visibly late in normal use, stop and report.

## 6. Documentation and ship

- [ ] 6.1 Update `gotchas/ui-threading.md`: keep the history of the two reverted attempts and add how this one avoids them. Add gotchas on scan-owned fields, the carry-forward trap and why synchronous mode stays, and update `context/knowledge/index.md`.
- [ ] 6.2 Confirm `internal/daemon/surface.go` and the files in its path lists are untouched.
- [ ] 6.3 Run `make pre-pr` in full.
- [ ] 6.4 Archive this change in the same PR (`openspec archive move-needs-input-scan-off-ui-thread`).
- [ ] 6.5 Push with `iris_push` and open the PR with `iris_gh_pr_create`, stating that web and macOS are unaffected and that no supervisor restart is needed.
