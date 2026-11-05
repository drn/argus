# Typing latency measurements

## Confound (2026-11-05 08:34-08:41)

This capture is **excluded from the Before baseline**. The coordinator paused it at 08:41:44 PST after identifying synchronous daemon usage RPCs on the UI thread, introduced by `a6d36e54` ([PR #1110](https://github.com/drn/argus/pull/1110)). The separate `0a-routing-stall` hotfix owns that regression; repaint implementation has not started.

- **Window:** approximately 2026-11-05 08:34:00 through 08:41:44 PST. The report uses those inclusive timestamp bounds.
- **Binary:** timing-only commit `dbf2f5ea`, built with Go 1.26.8; launched with `ARGUS_TYPING_LOG=1` from `/tmp/argus-paint-baseline.SzmlNF/argus-baseline`.
- **Machine context:** resumed from dead-battery deep sleep, without rebooting. The coordinator observed load around 170 at 08:29 while `fseventsd` caught up, then 1-minute load 5.67 at 08:36 with the longer averages still decaying.
- **Worker snapshot at 08:36:52:** uptime 17 days, 18:38; load averages 5.31 / 47.99 / 56.21. MCP reported 19 tasks with `in_progress` status around 08:37; this is a task-status count, not a verified live-process count.
- **Process context:** daemon and supervisor were older than the installed `~/go/bin/argus`; Aaron skipped the skew modal. Neither process was restarted. The replacement Before and After captures must use the same daemon/supervisor deployment.
- **Coverage of this attempt:** coordinator pane only, 680 samples, including 291 quiet samples and zero `no-echo` samples. The planned three-pane capture did not complete.

### Recorded latency

All values below are milliseconds. Quiet means `quiet_ms >= 50` and `overlap=0`. The large difference between all and quiet populations must remain visible: queued bursts after long UI stalls are not independent quiet echoes.

| Population | Metric | n | p50 | p90 | p99 | Max |
| --- | --- | ---: | ---: | ---: | ---: | ---: |
| All | Total | 680 | 48.009 | 21621.280 | 28686.728 | 35135.611 |
| All | Queue | 680 | 23.186 | 21614.413 | 28679.793 | 32108.128 |
| All | Arrival | 680 | 1.959 | 2.491 | 10.193 | 24.106 |
| All | Paint | 680 | 5.093 | 5.719 | 84.883 | 4502.468 |
| All | Tick stall | 680 | 1.019 | 20990.103 | 28336.088 | 34612.209 |
| All | Prior quiet | 680 | 31.049 | 155.364 | 1018.726 | 40144.740 |
| Quiet | Total | 291 | 7.094 | 98.192 | 4262.230 | 4603.494 |
| Quiet | Queue | 291 | 0.016 | 64.194 | 2882.598 | 4288.776 |
| Quiet | Arrival | 291 | 2.083 | 2.479 | 11.367 | 24.106 |
| Quiet | Paint | 291 | 4.977 | 5.517 | 280.979 | 4502.468 |
| Quiet | Tick stall | 291 | 0.000 | 72.884 | 3736.349 | 4282.181 |
| Quiet | Prior quiet | 291 | 98.144 | 330.239 | 1574.147 | 40144.740 |

### Stall sampler and stack evidence

The prescribed 30-second `argus-latency stalls` capture completed and exited; no sampler remained running during the later diagnostic snapshots. It reported 9 runs, median 3629.930ms, p90/max 3824.964ms, and 99.50% of the window stalled. This method repeatedly requests goroutine dumps at a nominal 5ms interval, briefly stopping the process for each dump and adding measurement overhead; these are comparative estimates, not unperturbed timings.

Three later single goroutine snapshots were saved at 08:40:01, 08:40:15 and 08:40:34 PST. The first and third show goroutine 1 waiting on this path:

```text
daemon/client.(*Client).callWithTimeout
daemon/client.(*Client).RoutingUsage
backendtier.currentReadings
backendtier.ResolveChoice
agent.resolveTaskChoice -> agent.ResolveBackend
tui.(*App).resolveHeraTier
hera/model.(*Model).AnnotateRoles
hera.(*HeraPage).doRefresh
tui.(*App).refreshTasksWithIDs
tui.(*App).onTick.func1
tview.(*Application).Run
```

The middle snapshot shows the normal event-loop select, so the UI did recover between stalls. `callWithTimeout` has a 2-second deadline, but these snapshots do **not** establish timeouts: nearby `[backendtier] using daemon usage readings` lines show successful replies roughly 30ms apart. The supported finding is repeated synchronous per-role usage RPCs on the UI thread, not a claim that the daemon lacks the method or that every call times out.

At 08:39 the coordinator's last-minute report showed 94 samples with queue p50 about 22089ms, queue max about 26390ms and tick-stall p50 about 21721ms while machine load was around 5.7. This independent window and the later stacks identify a UI-thread stall beyond the sampling window and the initial wake-up load.

Idle and busy CPU profiles were cancelled by the coordinator before collection; no CPU comparison can be drawn from this attempt.

### Preserved evidence

- **Scratch directory:** `/tmp/argus-paint-baseline.SzmlNF/`.
- **Bounded report:** `confound-report.txt`.
- **Completed stall capture:** `before-stalls.txt`.
- **Single goroutine snapshots:** `before-goroutine-1.txt`, `before-goroutine-2.txt`, `before-goroutine-3.txt`.
- **Next capture:** wait for the separate hotfix to merge, rebase onto fresh `origin/master`, rebuild the scratch binaries and repeat Before before starting repaint implementation.
