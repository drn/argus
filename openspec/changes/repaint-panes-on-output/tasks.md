## 1. Timing log (before-baseline commit)

- [x] 1.1 Read `context/knowledge/gotchas/ui-threading.md`, the repaint bullets in `gotchas/pty-terminal.md`, and `context/knowledge/testing.md`.
- [x] 1.2 Add the latency tracker in `internal/tui` with a failing test first. Cover total and part calculations, `stall_ms` intersection with tick closures, `no-echo` after 2 seconds, `overlap`, `quiet_ms`, and that no key content or length is logged. Read `ARGUS_TYPING_LOG` once at startup and pass the result in, so tests don't depend on the environment. Test that a nil tracker takes no timestamps and writes no lines.
- [x] 1.3 Add an output probe: a non-blocking `io.Writer`, attached through `AddWriterFrom` at the current total via an optional interface, that only records an atomic timestamp in this commit. Attach it only when the timing log is on.
- [x] 1.4 Record in `TerminalPane.Draw` that the pane painted, and how far it has fed its emulator. Reconcile probe subscriptions in `afterDraw` on the UI goroutine.
- [x] 1.5 Override `lazyScreen.Show` to delegate and then notify the tracker. Add a SimulationScreen smoke test because this touches the screen wrapper.
- [x] 1.6 Call the tracker from `handleAgentKey` and `HeraPage.forwardKey`, using `EventKey.When()` as the start time.
- [x] 1.7 When the timing log is on, time the tick closure in `onTick`, feed closure intervals to the tracker, and log `[tick]` at 16ms or longer.

## 2. Latency tool and baseline

- [x] 2.1 Add `internal/latencyreport` with tests: parse `[typing]` lines, compute per-pane percentiles for all and quiet samples, count `no-echo`, filter by time range, and detect stall runs from goroutine dumps.
- [x] 2.2 Add `cmd/argus-latency` with `report` (`--since`, `--until`, `--last`, `--log`) and `stalls` (`--addr`, `--duration`, `--interval`). Add its `main.go` to `coverage-ignore.txt` with the reason.
- [x] 2.3 Run `make pre-pr`, commit the timing log and tool as their own commit, and push with `iris_push`.
- [x] 2.4 Follow "Before capture" and "Capture window" in `design.md`: build the baseline binary outside the repository, send `hera_send(status="blocked")` with its path, and wait. Do not use `iris_set_dogfood`, `iris_publish` or `iris_reload`.
- [x] 2.5 Complete the Before results in `measurements.md` and commit before opening the PR. Per the 2026-11-05 coordinator amendment, group 3 may proceed while the frozen v2 baseline awaits its remaining typing and busy CPU capture; Before and After will be measured back-to-back later.

Baseline verification: `make pre-pr` was run in full; Go 1.26.8's 11 standard-library vulnerabilities stopped its advisory-in-CI `vuln` step. Per coordinator approval, build/vet/format/lint and the complete race/coverage gate were rerun on Go 1.26.8 with `GOFLAGS=-p=3` and inherited `CODEX_HOME`/`CODEX_SQLITE_HOME` removed so tests use fixture homes. All passed, with 0 lint issues and 88.9% filtered coverage. `openspec validate --all --strict` passed all 78 items. No repository toolchain setting changed.

## 3. Output-driven repaint (repaint commit)

- [x] 3.1 Add the repaint pump with tests using `testing/synctest`: immediate leading-edge draw, frame cap, coalescing, the draw-during-output re-wake, one queued draw at most, and exit on `tickDone`.
- [x] 3.2 Connect a writer to the pump's wake channel, always attached whether or not the timing log is on, so only sessions drawn in the last frame can wake it. Wake once when subscribing. Record the atomic output timestamp only when the timing log is on.
- [x] 3.3 Add the `Done` watcher per subscription, cancelled on unsubscribe, with a test that a session ending wakes the pump and that no goroutine outlives its subscription.
- [x] 3.4 Remove the 16ms follow-up goroutine from `handleAgentKey`.
- [x] 3.5 Rename `startAgentRedrawLoop` to `startAgentPTYSyncLoop`, rename `redrawLoopGen` and `redrawLoopShouldExit` to match, and remove its `QueueUpdateDraw` calls while keeping `SyncPTYSize`, the cadence and the generation guard. Update all four call sites (`app.go`, `artifacts.go`, `jumpswitcher.go`) and `TestApp_RedrawLoopGenSupersedesStaleLoop`.
- [x] 3.6 Add writer fan-out to `apiclient.Session` mirroring `RemoteSession` (ring first, snapshot under the lock, write outside it, drop failed writers), replace its stale comment, and test it.
- [x] 3.7 When the timing log is on, log `[repaint] wakes=N frames=N window_s=30` every 30 seconds while active, and stay silent when idle. Log pump failures and shutdown through `uxlog` regardless, per the logging requirements in `AGENTS.md`.
- [x] 3.8 Add smoke tests on a SimulationScreen with the real event loop: output on a visible Tasks pane repaints, output on a visible Hera pane repaints, output on a hidden session does not, and an idle pane produces no pump draws.
- [x] 3.9 Confirm by diff that `internal/agent/session.go`, `internal/agent/ringbuffer.go`, `internal/daemon/client/` and `internal/daemon/surface.go` are untouched and `TestSupervisorSurfaceDigest` passes without re-recording.

## 4. Measure and tune

- [ ] 4.1 Repeat the capture with the `argus-repaint` binary, per "After capture" in `design.md`, and record under "After" in `measurements.md`.
- [ ] 4.2 Compare busy-agent CPU. If it rises more than 10 points of one core on the same workload, apply the tuning order in `design.md` and re-measure.
- [ ] 4.3 Check `arrive_ms` and the `[repaint]` lines against the two unmeasured assumptions (relay hop cost, Claude's redraw rate). If relay hops matter, tell the coordinator, because scope changes.

## 4a. Bound terminal-size query cost (coordinator approved)

- [x] 4a.1 Add failing fake-TTY tests for successful-query reuse, exact 1-second expiry, resize invalidation before callback, error passthrough, Start/resume/Stop and concurrent invalidation. Add a SimulationScreen test for production screen setup.
- [x] 4a.2 Implement a cached tcell.Tty wrapper, preserve 8ms repainting and all normal Clear/Draw/Show behavior, document gotchas and confirm protocols/supervisor surface are unchanged.
- [ ] 4a.3 Run both review lenses and full local gates, commit locally without pushing, then build a fresh scratch binary and report its path and SHA-256. Remeasure BUSY with typing diagnostics on; IDLE follows only if BUSY is at most 17.38% of one core.

Cache review: `hera-review` and `hera-review-test-adversary` identified and resolved test gaps in production factory integration and actual paste-handler delivery; the SimulationScreen setup test also observes mouse/paste enablement after Init, because injected paste events alone cannot detect broken initialization order. Real tcell Show tests exercise the production factory, exact TTL expiry, and notified/missed resizes. Final 1-second focused race tests pass. Full `make pre-pr` passes build/vet/format/lint (0 issues), then stops at the same 11 standard-library vulnerabilities covered by the coordinator's advisory exception; its exit status is preserved. The separate full race/coverage gate passes at 89.0% (40,622/45,658 statements; 13 excluded files). All 78 strict OpenSpec validations pass. ProtocolVersion remains 11, and `TestSupervisorSurfaceDigest` passes without changing its expected digest. The controlled remeasurement remains pending.

## 5. Documentation and ship

- [x] 5.1 Update `gotchas/ui-threading.md` (fix-redraw-loop-leak bullet) and `gotchas/pty-terminal.md` (the 200ms loop and 16ms follow-up bullets) to describe the pump, keeping the history. Add new gotchas for the writer-based signal and why `session.go` must stay untouched, and update `context/knowledge/index.md`.
- [x] 5.2 Run `make pre-pr` in full.

Repaint verification (2026-11-05): the full gate passes build/vet/format/lint (0 issues), then stops at the same 11 Go 1.26.8 standard-library vulnerabilities covered by the coordinator's advisory exception. The separate full `test-cover-gate` passes with fixture environment, `GOFLAGS=-p=3`, and 88.9% filtered coverage. Focused race tests, `TestSupervisorSurfaceDigest` without re-recording, and all 78 strict OpenSpec validations pass. `hera-review` and `hera-review-test-adversary` findings were fixed and rechecked; measurement, tuning, archive, push and PR remain pending.
- [ ] 5.3 Archive only this change in the same PR (`openspec archive repaint-panes-on-output`). The other two change folders stay active.
- [ ] 5.4 Push with `iris_push`, open the PR with `iris_gh_pr_create` including both measurement tables, and state in the description that web and macOS are unaffected and that no supervisor restart is needed.
