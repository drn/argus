## 1. Timing log (before-baseline commit)

- [ ] 1.1 Read `context/knowledge/gotchas/ui-threading.md`, the repaint bullets in `gotchas/pty-terminal.md`, and `context/knowledge/testing.md`.
- [ ] 1.2 Add the latency tracker in `internal/tui` with a failing test first. Cover total and part calculations, `stall_ms` intersection with tick closures, `no-echo` after 2 seconds, `overlap`, `quiet_ms`, and that no key content or length is logged. Read `ARGUS_TYPING_LOG` once at startup and pass the result in, so tests don't depend on the environment. Test that a nil tracker takes no timestamps and writes no lines.
- [ ] 1.3 Add an output probe: a non-blocking `io.Writer`, attached through `AddWriterFrom` at the current total via an optional interface, that only records an atomic timestamp in this commit. Attach it only when the timing log is on.
- [ ] 1.4 Record in `TerminalPane.Draw` that the pane painted, and how far it has fed its emulator. Reconcile probe subscriptions in `afterDraw` on the UI goroutine.
- [ ] 1.5 Override `lazyScreen.Show` to delegate and then notify the tracker. Add a SimulationScreen smoke test because this touches the screen wrapper.
- [ ] 1.6 Call the tracker from `handleAgentKey` and `HeraPage.forwardKey`, using `EventKey.When()` as the start time.
- [ ] 1.7 When the timing log is on, time the tick closure in `onTick`, feed closure intervals to the tracker, and log `[tick]` at 16ms or longer.

## 2. Latency tool and baseline

- [ ] 2.1 Add `internal/latencyreport` with tests: parse `[typing]` lines, compute per-pane percentiles for all and quiet samples, count `no-echo`, filter by time range, and detect stall runs from goroutine dumps.
- [ ] 2.2 Add `cmd/argus-latency` with `report` (`--since`, `--until`, `--last`, `--log`) and `stalls` (`--addr`, `--duration`, `--interval`). Add its `main.go` to `coverage-ignore.txt` with the reason.
- [ ] 2.3 Run `make pre-pr`, commit the timing log and tool as their own commit, and push with `iris_push`.
- [ ] 2.4 Follow "Before capture" and "Capture window" in `design.md`: build the baseline binary outside the repository, send `hera_send(status="blocked")` with its path, and wait. Do not use `iris_set_dogfood`, `iris_publish` or `iris_reload`.
- [ ] 2.5 Write the results to `measurements.md` under "Before" and commit. Do not start group 3 until this is committed.

## 3. Output-driven repaint (repaint commit)

- [ ] 3.1 Add the repaint pump with tests using `testing/synctest`: immediate leading-edge draw, frame cap, coalescing, the draw-during-output re-wake, one queued draw at most, and exit on `tickDone`.
- [ ] 3.2 Connect a writer to the pump's wake channel, always attached whether or not the timing log is on, so only sessions drawn in the last frame can wake it. Wake once when subscribing. Record the atomic output timestamp only when the timing log is on.
- [ ] 3.3 Add the `Done` watcher per subscription, cancelled on unsubscribe, with a test that a session ending wakes the pump and that no goroutine outlives its subscription.
- [ ] 3.4 Remove the 16ms follow-up goroutine from `handleAgentKey`.
- [ ] 3.5 Rename `startAgentRedrawLoop` to `startAgentPTYSyncLoop`, rename `redrawLoopGen` and `redrawLoopShouldExit` to match, and remove its `QueueUpdateDraw` calls while keeping `SyncPTYSize`, the cadence and the generation guard. Update all four call sites (`app.go`, `artifacts.go`, `jumpswitcher.go`) and `TestApp_RedrawLoopGenSupersedesStaleLoop`.
- [ ] 3.6 Add writer fan-out to `apiclient.Session` mirroring `RemoteSession` (ring first, snapshot under the lock, write outside it, drop failed writers), replace its stale comment, and test it.
- [ ] 3.7 When the timing log is on, log `[repaint] wakes=N frames=N window_s=30` every 30 seconds while active, and stay silent when idle. Log pump failures and shutdown through `uxlog` regardless, per the logging requirements in `AGENTS.md`.
- [ ] 3.8 Add smoke tests on a SimulationScreen with the real event loop: output on a visible Tasks pane repaints, output on a visible Hera pane repaints, output on a hidden session does not, and an idle pane produces no pump draws.
- [ ] 3.9 Confirm by diff that `internal/agent/session.go`, `internal/agent/ringbuffer.go`, `internal/daemon/client/` and `internal/daemon/surface.go` are untouched and `TestSupervisorSurfaceDigest` passes without re-recording.

## 4. Measure and tune

- [ ] 4.1 Repeat the capture with the `argus-repaint` binary, per "After capture" in `design.md`, and record under "After" in `measurements.md`.
- [ ] 4.2 Compare busy-agent CPU. If it rises more than 10 points of one core on the same workload, apply the tuning order in `design.md` and re-measure.
- [ ] 4.3 Check `arrive_ms` and the `[repaint]` lines against the two unmeasured assumptions (relay hop cost, Claude's redraw rate). If relay hops matter, tell the coordinator, because scope changes.

## 5. Documentation and ship

- [ ] 5.1 Update `gotchas/ui-threading.md` (fix-redraw-loop-leak bullet) and `gotchas/pty-terminal.md` (the 200ms loop and 16ms follow-up bullets) to describe the pump, keeping the history. Add new gotchas for the writer-based signal and why `session.go` must stay untouched, and update `context/knowledge/index.md`.
- [ ] 5.2 Run `make pre-pr` in full.
- [ ] 5.3 Archive only this change in the same PR (`openspec archive repaint-panes-on-output`). The other two change folders stay active.
- [ ] 5.4 Push with `iris_push`, open the PR with `iris_gh_pr_create` including both measurement tables, and state in the description that web and macOS are unaffected and that no supervisor restart is needed.
