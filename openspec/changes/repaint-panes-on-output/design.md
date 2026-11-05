## Context

Typing lag has two causes. This change handles repaint timing and installs the measurements. The scan stall is handled by `reduce-needs-input-scan-cost` and `move-needs-input-scan-off-ui-thread`.

Evidence from the live TUI on 2026-11-04, with 23 tasks in progress and high machine load, so worst-case figures may be inflated:

- 30 stalls in 30 seconds, one per tick: median 143ms, p90 268ms, max 305ms.
- The UI goroutine was stalled about 16% of the time.
- TUI CPU was 16% of one core over 20 seconds.

### What the code does today

All line numbers were checked at `d44efd3c`, which was `origin/master` when these specs were written.

| Handoff claim | Finding |
| --- | --- |
| Tasks view has a one-shot 16ms check after a keystroke (`app.go:4740-4753`) | Confirmed. |
| If the echo misses that check, nothing repaints until the 200ms poll (`app.go:6461`) | **Partly wrong.** `spinnerLoop` also repaints every 100ms in the agent view whenever any session in the fleet is running and not idle, because `computeVisibleActiveSpinner` is only scoped on the Tasks list. The usual worst case is 100ms. It is 200ms only when the whole fleet is idle. |
| Hera panes have no follow-up check and no output-driven repaint (`hera/panes.go:622`) | Confirmed. Repaints come from `spinnerLoop` (same fleet-wide gate) or the 1s tick. |
| `startAgentRedrawLoop` has four call sites | Confirmed: `app.go:5448`, `app.go:6453`, `artifacts.go:126`, `jumpswitcher.go:230`. |
| The redraw loop also keeps PTY size in sync | Confirmed. `SyncPTYSize` runs in it, off the UI goroutine because it can issue a resize RPC. |
| A signal needs adding to three session implementations | **Not needed for two of them.** See decision 1. |

Other facts this design relies on:

- **Key timestamps:** tcell's `NewEventKey` stamps `time.Now()` when its input parser creates the event, before the event enters tview's queue. `EventKey.When()` therefore includes any wait behind a stalled UI goroutine.
- **Draw order:** tview's `draw()` calls `afterDraw` before `screen.Show()`. `lazyScreen` wraps the screen, so overriding `Show` gives a true after-flush timestamp.
- **Ordering in the stream reader:** `RemoteSession` and `agent.Session` both write to the ring before calling attached writers. A wake therefore never arrives before the bytes are readable.
- **pprof listener:** `127.0.0.1:6061` (`cmd/argus/main.go:149`) binds in one TUI process only. A second running TUI fails to bind and logs it, so the old TUI must be closed before measuring.
- **Plugin views:** they use a separate pane in `internal/tui/terminalpane`, which already repaints per chunk through `OnNeedRedraw`. They are not part of this change.

## Goals / non-goals

**Goals:**

- A visible pane's echo paints as soon as its bytes arrive, on all three panes.
- Every later change can be measured the same way against a captured before baseline.
- Only a TUI relaunch is required.

**Non-goals (named follow-ups, not part of this work):**

- **Scan stall:** handled by `reduce-needs-input-scan-cost` and `move-needs-input-scan-off-ui-thread`. Until those land, a keystroke can still wait behind the tick about once per second.
- **TUI memory:** 3.9 GB RSS after 4 days, about 1 GB of heap in `Scrollback.Push`, and 75 goroutines blocked in `Emulator.Read`. Possibly another leak like #959/#963; unconfirmed.
- **Daemon profiling:** the daemon has no pprof listener, so its CPU could not be broken down.
- **Spinner scope:** `spinnerLoop` stays fleet-wide outside the Tasks list. This is the existing named follow-up in `computeVisibleActiveSpinner`'s comment.
- **MySQL dev-stack CPU load:** a separate Hera coordinator owns it.

## Decisions

### 1. Use the existing writer API as the output signal

The handoff proposed a new coalescing signal on the session handle. The pump instead registers a small non-blocking `io.Writer` through the existing `AddWriterFrom(w, currentTotal)`.

- **Why:** `internal/agent/session.go` is listed in `SupervisorStreamPaths`. Adding a method there would change the stream digest and force a judgment call on the surface version, even though the supervisor would never use the method. The writer API already exists on `agent.Session` and `RemoteSession`, with the non-blocking contract this needs, so neither file changes.
- **Remote:** `apiclient.Session` is the exception, because its writer methods are no-ops. It gains a real writer fan-out that mirrors `RemoteSession`.
- **Interface:** `TerminalPane` holds an `agentview.TerminalAdapter`, which has no writer methods. The pump type-asserts for an optional interface (`AddWriterFrom`, `RemoveWriter`, `Done`). All three real handles satisfy it, and test mocks don't need to change.
- **Alternative rejected:** a new `OutputSignal()` method. It adds a stream digest change and churn in every mock, and gains nothing.

### 2. One pump with one wake channel

A single goroutine owns a wake channel with a buffer of one. Every subscribed writer does a non-blocking send to it. This coalesces by construction and replaces per-session goroutines.

The loop is:

1. Wait for a wake.
2. If less than the frame cap has passed since the last draw, sleep the remainder. This is where bursts collapse.
3. Drain the wake channel, so that output arriving during the draw wakes the pump again.
4. Call `QueueUpdateDraw`, which blocks until the draw finishes. This gives natural backpressure and at most one draw queued.

The next frame interval starts after that draw completes, so an event-loop stall cannot allow two immediate frames. Controlled quit closes `tickDone` first and leaves the event loop alive until the pump's one queued draw returns; only then does it stop tview, whose synchronous queue API has no cancellation operation.

The first byte after a quiet period draws immediately, which is the case that matters for echo. The cap is a constant of about 8ms, tuned against the timing log. It is not a config key.

### 3. Pane visibility comes from what was actually drawn

`TerminalPane.Draw` records that it painted, past its empty-rect guard. `afterDraw` then checks the three panes on the UI goroutine and reconciles subscriptions against the sessions drawn in that frame.

This is more accurate than reasoning from mode and tab. It handles Hera details mode and Ctrl+Z fullscreen, where only one pane draws, without any extra logic. It also needs no lock, so the pump never waits on `a.mu`. That matters because the tick holds `a.mu` for the whole scan today.

On subscribing, the pump wakes once, so output that arrived during the swap isn't missed. Subscribing does no RPC.

### 4. Session end is covered by `Done`

Writers aren't told when a session ends. Each subscription has a small goroutine that waits on `Done()` or cancellation and then wakes the pump, which replaces the redraw loop's final repaint.

### 5. Keep the loop for PTY size only

`startAgentRedrawLoop` becomes `startAgentPTYSyncLoop`, with the same four call sites, cadence, generation guard and exit rules. It no longer calls `QueueUpdateDraw`.

Merging it into `spinnerLoop` would be a smaller diff later, but would have to re-create the "only while this task is on screen" gate. Keeping it separate leaves the fix for the redraw-loop goroutine leak (fix-redraw-loop-leak) and its test intact.

### 6. Timing log design

**The log is off unless a measurement asks for it.** The TUI reads `ARGUS_TYPING_LOG=1` once at startup. When it isn't set, the tracker is nil, and each call site is a nil check. No timestamps are taken, no `[typing]`, `[tick]` or `[repaint]` lines are written, and no probe writer is attached for timing.

The cost when it's on is several timestamps and one synchronous log write per keystroke, on the UI goroutine. That is small next to a repaint, but it does nothing useful outside a measurement. It also keeps the 20MB `ux.log` from filling with keystroke lines.

An environment variable fits better than a config key, because this is a per-launch diagnostic with no user-facing behaviour, and it needs no daemon or Settings change. Code reads the flag once and passes the result in, so tests never depend on the process environment. The pump's own writer is separate and is always attached after the repaint commit, because it drives the repaint rather than the log.

The tracker runs on the UI goroutine, apart from one atomic timestamp set by the output writer. All intervals are measured from these points:

| Point | Where |
| --- | --- |
| Key pressed | `EventKey.When()` |
| Handler entered | `handleAgentKey` or `forwardKey` |
| Write queued | after `WriteInput` returns |
| First output after the key | the writer's atomic timestamp |
| Echo in emulator | `TerminalPane` has fed past the session total recorded at the key |
| Echo flushed | `lazyScreen.Show` returns |

The log line is:

```text
[typing] task=<id> pane=<tasks|hera-coord|hera-agent> total_ms=N queue_ms=N arrive_ms=N paint_ms=N stall_ms=N quiet_ms=N overlap=0|1
```

- `total_ms`: key pressed to echo flushed.
- `queue_ms`: key pressed to handler entered. This is the wait behind a stalled UI goroutine.
- `arrive_ms`: write queued to first output. This covers the relay hops and the agent. It also tests the unmeasured assumption that each hop costs well under 1ms.
- `paint_ms`: first output to echo flushed. This is what the repaint change should shrink.
- `stall_ms`: the part of `total_ms` spent inside a tick closure. Closure start and end times go in a short ring, and a key's interval is intersected with them. This gives each fix the right credit.
- `quiet_ms`: time since the last output before the key. Without this, a busy agent's unrelated output looks like an instant echo.
- `overlap=1`: another key was still waiting for its echo.

Keys with no output within 2 seconds log `outcome=no-echo` and are not paired with later output. No key content or length is logged.

This measures up to tcell flushing to the terminal. It can't see the outer terminal's own rendering delay.

A tick closure taking 16ms or longer logs `[tick] closure_ms=N`. The pump logs `[repaint] wakes=N frames=N window_s=30` once per 30 seconds while active, which also tests the unmeasured assumption that Claude Code redraws about 30 times a second.

### 7. No setting for the repaint change

Earlier guidance is that features which materially change the experience get an on/off setting, and pure logic moves don't. This adds no new UI or interaction path: panes look the same and only update sooner. So there is no setting and the frame cap stays a constant. If the busy-agent CPU check fails, the response is tuning (see risks).

## Baseline procedure

**Coordinator amendment, 2026-11-05:** the v2 timing-only binary at `/tmp/argus-paint-baseline-v2.CyRaje/` is frozen with hotfix `2349aead`. Implementation may proceed before the remaining Before typing and busy CPU capture. Aaron will complete Before on that binary and After on a separately built repaint binary in one sitting, recording load and task count for both. The recorded idle Before figures remain valid. No deployment, push or PR is authorized until the coordinator resumes those steps; always choose Skip/Esc in scratch-binary skew modals, since a restart action can repoint the shared deployment at the scratch binary.

The timing log has to run in a TUI without the repaint change, and a TUI change only takes effect when Aaron relaunches. Workers must not deploy: the dogfood daemon is shared with the MySQL coordinator and `iris_set_dogfood` overwrites it.

This procedure touches nothing shared. It runs a separate TUI binary against the existing daemon.

### Before capture

1. **Timing commit.** The worker implements task groups 1 and 2 only. `make pre-pr` passes. The worker commits and pushes with `iris_push`.
2. **Build.** The worker runs `go build -o <scratchpad>/argus-baseline ./cmd/argus` with an absolute path outside the repository. No `iris_set_dogfood`, `iris_publish` or daemon reload.
3. **Hand off.** The worker sends `hera_send(status="blocked")` to the coordinator with the absolute binary path. The coordinator confirms timing with Aaron.
4. **Relaunch by Aaron.** Aaron quits the running TUI, which leaves agents running because the daemon and supervisor own them, then runs `ARGUS_TYPING_LOG=1 <path to binary>` at the same terminal size.
5. **Skew modal.** The TUI's hash differs from the daemon's, so the blocking skew modal opens. **Aaron picks Skip or presses Esc.** Restart daemon would put this build on the shared daemon, and restart supervisor would interrupt every agent. A status bar notice afterwards is expected.
6. **Check it's live.** After the first keystroke, `go run ./cmd/argus-latency report --last 1m` shows a sample.

### Capture window

About 10 minutes, at a time when Aaron is actually working, so the load is real. The worker records `uptime` and the count of running tasks at the start.

7. **Tasks agent view.** Aaron opens a session parked at its prompt and types at least 200 keystrokes without submitting, then clears the composer.
8. **Hera coordinator pane.** Same again, at least 200 keystrokes.
9. **Hera worker pane.** Same again, at least 200 keystrokes.
10. **Stalls.** During one of those windows, the worker runs `go run ./cmd/argus-latency stalls --duration 30s`.
11. **Idle CPU.** With nothing typed, the worker runs `go tool pprof -top "http://127.0.0.1:6061/debug/pprof/profile?seconds=20"` and records the `Total samples` percentage.
12. **Busy CPU.** With a streaming agent on screen, the same 20-second profile.

If the sandbox can't reach port 6061, the worker asks the coordinator to have Aaron run steps 10 to 12 with the `!` prefix.

### Reading results back

13. **Report.** The worker runs `go run ./cmd/argus-latency report --since <start> --until <end>` straight away, because `ux.log` rotates at 20MB.
14. **Record.** The tables, `uptime`, task count, stall output and CPU figures go into `openspec/changes/repaint-panes-on-output/measurements.md` under a "Before" heading, and get committed.
15. **Back to normal.** Aaron can return to the normal `argus` binary at any point, which also turns the timing log off.

### After capture

16. **Repaint commit.** The worker implements task group 3 on the same branch.
17. **Repeat.** Steps 2 to 14 run again with the binary named `argus-repaint`, recording under an "After" heading.
18. **Compare.** The worker reports both to the coordinator and includes them in the PR description.

## Stall and latency measurement used after each change

The same two measurements apply after this change and after both later ones.

**Stalls (`argus-latency stalls`).** This is the method from the original investigation:

- Poll `/debug/pprof/goroutine?debug=2` about every 5ms.
- Find runs where goroutine 1's stack contains `onTick` or `refreshTasksWithIDs`.
- Report the count, median, p90, max and percentage of time stalled.

CPU profiles can't measure keystroke-to-echo delay. Each goroutine dump briefly stops the process, so the absolute figures carry some overhead. They are valid for comparing runs, not as true values.

**Latency (`argus-latency report`).** Per pane, this reports n, p50, p90, p99 and max for `total_ms` and each part. It reports all samples and also "quiet" samples, meaning `quiet_ms` of at least 50 with `overlap=0`, and counts `no-echo`. Quiet samples are the honest echo figures.

Compare medians rather than maximums, and record load, since the original worst case was measured under high load.

## Frontend parity

TUI-internal. The web and macOS clients have their own rendering and are unaffected, and no REST endpoint, event or field changes. The `--remote` TUI gets the same pump through its `apiclient` handle.

## Supervisor compatibility

No `ProtocolVersion` bump and no change to either surface digest.

- **Spawn digest (`SupervisorSpawnPaths`):** no listed file changes.
- **Stream digest (`SupervisorStreamPaths`):** this was the real risk, because `internal/agent/session.go` and `ringbuffer.go` are listed. Decision 1 leaves both untouched.
- **`internal/daemon/client`:** compiled into the daemon but not listed in either path list, and unchanged anyway. `AddWriterFrom` already exists there.
- **Wire:** no RPC or stream format changes.

The only process that needs the new code is the TUI, so only a relaunch is needed.

## Risks / trade-offs

- **[A busy agent raises CPU]** Repaints could go from 5 to 10 a second up to Claude's own output rate, or up to about 125 a second at the cap. → Steps 11 and 12 measure this. An increase of more than 10 points of one core on the same workload counts as a failure. The response is to raise the cap to 16ms, then to use 8ms only for 250ms after a keystroke and about 33ms otherwise.
- **[Repaints crowd out keystrokes]** tview picks between ready events and updates at random. → The pump holds at most one draw in the queue, so keys still get their turn. `queue_ms` would show if this happens.
- **[False echo pairing]** Any output after a key counts as its echo, including a cursor blink. → `quiet_ms` and `overlap` let the report exclude unreliable samples. Reported figures are a close estimate, not exact.
- **[Stale subscription]** A pane rebinds to a new session. → A rebind always triggers a draw on the UI goroutine, and `afterDraw` reconciles there. Subscribing wakes the pump once.
- **[Baseline binary prompts]** A new unsigned binary may trigger a macOS privacy prompt. → Aaron can approve or deny it. The TUI doesn't spawn agents when connected to the daemon, so denying doesn't affect them.
- **[Rendering regressions]** → No new `screen.Sync()` and no writes to stderr or stdout. `Show` only timestamps and delegates.

## Migration plan

Merging and a relaunch are the whole rollout, and deployment goes through the coordinator. To roll back, revert the repaint commit. The timing log can stay on its own, and stays off unless a launch asks for it.

## Open questions

None blocking. The frame cap and the 2-second no-echo cutoff are starting values to adjust against the log.
