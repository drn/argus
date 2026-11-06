# Typing latency measurements

## Baseline relaunch procedure

Always choose **Skip** (or Esc) in a scratch binary's skew modal. Never select either restart action: during the first attempt, choosing **Restart supervisor** also repointed `~/.argus/argusd` at the temporary baseline binary, changing the shared deployment. The coordinator restored the daemon, supervisor and installed TUI to hotfix `2349aead` ([PR #1111](https://github.com/drn/argus/pull/1111)) before requesting a new Before capture; keep that deployment fixed through the replacement Before and After runs.

## Earlier idle measurements (09:31 PST)

The replacement timing-only binary includes hotfix `2349aead` and was built at `c0ed1989` with Go 1.26.8. Its first observed timing line is 2026-11-05 09:30:58.505 PST. Both binaries and all capture files are under `/tmp/argus-paint-baseline-v2.CyRaje/`.

These idle measurements were collected while Aaron was in meetings. On 2026-11-05 the coordinator authorized repaint implementation while the v2 baseline binary stayed frozen; the later completed paired Before capture is recorded below, separately from this earlier two-task status snapshot.

- **Machine snapshot at 09:31:55 PST:** uptime 17 days, 19:33; load 3.20 / 3.27 / 4.88. MCP reported 2 tasks with `in_progress` status, not a verified live-process count.
- **Deployment:** daemon, supervisor and installed TUI were restored to `2349aead` by the coordinator before this capture. The worker made no process or deployment changes.
- **Idle CPU:** request bounds 09:31:55–09:32:17 PST, with the keyboard hands-off while Aaron was in meetings; the screen content was not independently identified. The native profile reports duration 20.15s and 1.42s total CPU samples, or **7.05% of one core**. Text: `before-idle-cpu.txt`; raw: `pprof.argus-baseline.samples.cpu.001.pb.gz`.
- **Idle stalls:** separate 30-second sampler, 09:32:39–09:33:09 PST, started after CPU profiling ended. It reports **30 runs**, median **14.977ms**, p90 **164.966ms**, maximum **240.040ms**, and **5.70%** stalled. Raw text: `before-stalls.txt`. As in the prescribed method, repeated goroutine dumps add overhead.
- **Continuation:** busy CPU and three-pane typing were collected later in the paired Before window below. The earlier interim report in `before-interim-report.txt` is retained but is not the final baseline table.

## Paired capture procedure

### Paired Before capture history (2026-11-05)

- **Process/window:** the baseline v2 TUI started at 11:10:10.024 PST and successfully bound pprof port 6061. First timing sample: 11:10:11.528. Screen size recorded at startup: 212×52.
- **Setup snapshot at 11:10:43:** uptime 17 days, 21:12; load 4.79 / 5.01 / 5.67. The session-state API reported 7 running and 5 idle; MCP separately reported 7 tasks with `in_progress` status.
- **Busy CPU:** native profile starts at 11:12:41 PST, duration 20.04s, total samples 1.69s, **8.43% of one core**. Request began at 11:12:40. The session-state snapshot during profiling showed 7 running and 6 idle. Text is `paired-before-busy-cpu.txt`, raw profile `pprof.argus-baseline.samples.cpu.002.pb.gz`, both in the frozen v2 scratch directory.
- **Known restart confound:** coordinator task `1793856717985375000` was stopped for width repair at 11:11:23.329, restarted at 11:11:23.778, and its pane re-resolved at 11:11:24.214. Samples overlapping that interruption must be excluded from final echo numbers.
- **Reported resize:** the coordinator reported a terminal resize around 11:13–11:14. No additional `afterDraw resize` marker or actual restart was observed during the native CPU interval ending about 11:13:01; a fullscreen pane width change was logged earlier at 11:12:10. The final typing table conservatively excludes the reported nominal range 11:13:00–11:14:00 despite the absence of an exact resize marker.
- **Sample completeness:** the initial report bounded at the first busy signal (11:12:15) had only 59 coordinator samples and none for Tasks or the worker pane. A later open-window snapshot has 344 Tasks and 175 coordinator samples, still no worker samples. These are unfiltered interim counts, not a completed three-pane baseline. Paste/dictation is not counted by key timing; the coordinator was notified that manual-key coverage remains incomplete.
- **Preservation:** raw typing metadata is saved as `paired-typing-snapshot.txt`; interim reports as `paired-before-report-raw.txt` and `paired-before-report-interim.txt`. The coordinator subsequently set `BEFORE DONE` at 12:07:00; the final report below supersedes these interim counts. After measurements remain pending.

### Before (final)

- **Window:** 2026-11-05 11:10:11 through 12:07:00 PST, inclusive; binary frozen at `c0ed1989`, including deployed hotfix `2349aead`.
- **Samples:** 1,029 raw, 957 retained: Tasks 437, coordinator 229, worker 291. All three retained populations exceed 200; `no-echo=0` in every pane.
- **Exclusions:** key-to-flush interval overlap is calculated from the log timestamp minus `total_ms`. The reported global resize interval 11:13:00–11:14:00 removes 70 coordinator and 2 Tasks samples. Three task-specific width-repair intervals remove zero overlapping samples: coordinator 11:11:23.329–11:11:24.214; Tasks session `1793901924745179000` 11:48:46.440–11:48:47.230 (stream reconnected); coordinator 12:04:01.855–12:04:02.978. No sample is excluded merely for being slow.
- **CPU and load:** busy CPU is 8.43% of one core over 20.04s at 11:12:41, with 7 running sessions. Setup load was 4.79 / 5.01 / 5.67. The earlier idle CPU 7.05% and idle stall window above are separate context, taken earlier with a smaller fleet.
- **Evidence:** `paired-before-typing-final.txt`, `paired-before-typing-clean.txt`, `paired-before-exclusions.json`, `filter-before.py` and `paired-before-report-clean.txt` in the v2 scratch directory. The final table is generated from the filtered metadata with the existing report command.

All table values are milliseconds; `n` is the number of retained samples. Quiet means at least 50ms of prior quiet and no overlapping pending key.

| Pane | Population | Metric | n | p50 | p90 | p99 | Max |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| hera-agent | all | total_ms | 291 | 6.818 | 37.557 | 225.128 | 290.391 |
| hera-agent | all | queue_ms | 291 | 0.019 | 7.591 | 204.938 | 238.652 |
| hera-agent | all | arrive_ms | 291 | 0.978 | 3.081 | 35.247 | 36.379 |
| hera-agent | all | paint_ms | 291 | 5.694 | 13.031 | 56.561 | 66.676 |
| hera-agent | all | stall_ms | 291 | 0.000 | 0.000 | 196.220 | 210.844 |
| hera-agent | all | quiet_ms | 291 | 19.408 | 76.462 | 489.928 | 23638.063 |
| hera-agent | quiet | total_ms | 47 | 7.290 | 23.249 | 259.975 | 259.975 |
| hera-agent | quiet | queue_ms | 47 | 0.019 | 1.171 | 238.652 | 238.652 |
| hera-agent | quiet | arrive_ms | 47 | 1.017 | 2.529 | 8.291 | 8.291 |
| hera-agent | quiet | paint_ms | 47 | 6.070 | 17.718 | 47.712 | 47.712 |
| hera-agent | quiet | stall_ms | 47 | 0.000 | 0.000 | 210.844 | 210.844 |
| hera-agent | quiet | quiet_ms | 47 | 96.995 | 279.037 | 23638.063 | 23638.063 |
| hera-coord | all | total_ms | 229 | 52.315 | 92.766 | 644.132 | 1936.830 |
| hera-coord | all | queue_ms | 229 | 0.018 | 2.441 | 211.689 | 251.295 |
| hera-coord | all | arrive_ms | 229 | 3.151 | 4.915 | 587.158 | 1904.445 |
| hera-coord | all | paint_ms | 229 | 42.491 | 79.498 | 96.571 | 97.801 |
| hera-coord | all | stall_ms | 229 | 0.000 | 0.000 | 203.998 | 239.847 |
| hera-coord | all | quiet_ms | 229 | 77.810 | 240.220 | 6386.433 | 19475.520 |
| hera-coord | quiet | total_ms | 152 | 53.725 | 92.766 | 644.132 | 1936.830 |
| hera-coord | quiet | queue_ms | 152 | 0.019 | 0.886 | 216.040 | 251.295 |
| hera-coord | quiet | arrive_ms | 152 | 3.191 | 5.025 | 587.158 | 1904.445 |
| hera-coord | quiet | paint_ms | 152 | 48.155 | 77.637 | 96.571 | 97.801 |
| hera-coord | quiet | stall_ms | 152 | 0.000 | 0.000 | 205.055 | 239.847 |
| hera-coord | quiet | quiet_ms | 152 | 98.269 | 297.741 | 8695.441 | 19475.520 |
| tasks | all | total_ms | 437 | 20.670 | 21.339 | 38.498 | 180.122 |
| tasks | all | queue_ms | 437 | 0.009 | 0.026 | 7.155 | 21.921 |
| tasks | all | arrive_ms | 437 | 1.968 | 2.362 | 4.260 | 27.918 |
| tasks | all | paint_ms | 437 | 18.553 | 19.172 | 21.303 | 152.185 |
| tasks | all | stall_ms | 437 | 0.000 | 0.000 | 15.335 | 27.549 |
| tasks | all | quiet_ms | 437 | 98.068 | 276.569 | 2797.115 | 945140.315 |
| tasks | quiet | total_ms | 370 | 20.692 | 21.339 | 38.498 | 180.122 |
| tasks | quiet | queue_ms | 370 | 0.009 | 0.022 | 6.239 | 21.921 |
| tasks | quiet | arrive_ms | 370 | 1.974 | 2.380 | 4.302 | 27.918 |
| tasks | quiet | paint_ms | 370 | 18.545 | 19.172 | 24.822 | 152.185 |
| tasks | quiet | stall_ms | 370 | 0.000 | 0.000 | 16.128 | 27.549 |
| tasks | quiet | quiet_ms | 370 | 110.967 | 354.015 | 3847.254 | 945140.315 |

### CPU view controls and rate candidates (in progress)

The initial CPU captures did not hold the visible workload fixed. Before's 8.43% profile began at 11:12:41 and overlapped a Ctrl+Z fullscreen transition at 11:12:46.091 (coordinator pane width 89→178). The first 8ms After busy profile used the Hera split with coordinator and worker output visible. These numbers are preserved as exploratory evidence and do not determine the like-for-like CPU gate.

| Capture | Native start PST | Duration | CPU samples | One-core CPU | View |
| --- | --- | ---: | ---: | ---: | --- |
| Initial Before busy | 11:12:41 | 20.04s | 1.69s | 8.43% | Mixed split/fullscreen during profile |
| Initial 8ms After busy | 12:27:29 | 20.13s | 5.72s | 28.41% | Hera split, coordinator + worker output |
| 8ms After hands-off | 12:32:00 | 20.14s | 4.94s | 24.53% | Hera split, worker output continuing |
| Controlled BASE busy | 12:39:02 | 20.05s | 1.48s | 7.38% | Fixed Hera split, coordinator + 2a worker |
| Controlled BASE idle | 12:45:10 | 20.04s | 1.91s | 9.53% | Tasks, groups collapsed, no selection, empty Preview |
| Controlled 8ms busy | 12:46:50 | 20.14s | 4.65s | 23.09% | Fixed Hera split, identical producer |
| Controlled 8ms idle | 12:50:21 | 20.02s | 0.50s | 2.50% | Same collapsed Tasks/empty Preview view |
| Controlled 16ms busy | 13:38:11 | 20.13s | 4.43s | 22.01% | Fixed Hera split, identical producer |

The coordinator's replacement protocol fixes the terminal at 212×52 with no Ctrl+Z or resizing. For BUSY, the same Hera coordinator and 2a worker are visible, and the worker runs an identical lightweight 24-second producer (600 lines at 25Hz) around each 20-second profile, with the same output-poll cadence. For IDLE, the Tasks task list is visible, no agent view is open, the keyboard is hands-off and the producer is stopped. The order is BASE, 8ms, then 16ms. The +10-point CPU guard is applied only within these matched BUSY and IDLE views.

The 16ms tuning candidate was built from an uncommitted change at `/tmp/argus-paint-tune16.Sux2wK/argus-repaint-16ms`; its focused race tests pass. Its busy result is 14.63 points above BASE and exceeds the guard. The coordinator canceled IDLE 16 and requested CPU attribution before further implementation, then approved restoring the 8ms cap with the TTY cache below. The first attempted 16ms relaunch actually listened from the 8ms executable (PID 36473); no profile was taken. The successful retry verified PID 37793's executable as `/private/tmp/argus-paint-tune16.Sux2wK/argus-repaint-16ms`, then waited 15 seconds before starting the identical producer and profile.

Controlled profile text and raw profiles are in `/tmp/argus-paint-after.GB4lCf/` with `controlled-<variant>-<state>` prefixes. `controlled-output.py` records the producer. Controlled BASE relaunch marker: 12:38:00.793, with successful pprof bind and a 212×52 frame. The controlled 8ms busy profile is 15.71 percentage points above BASE, exceeding the +10-point busy guard; idle is 7.03 points below BASE and passes. Its listener PID 20051 resolves through `lsof` to `/private/tmp/argus-paint-after.GB4lCf/argus-repaint`; both native profiles identify `argus-repaint`. IDLE 8 began after the coordinator's requested 20-second screen-settling delay, without a producer.

### Controlled busy CPU attribution

The BASE and 8ms busy profiles contain 1.48s and 4.65s of CPU respectively. Their native durations differ by only 0.09s; the following are absolute cumulative seconds, without normalization. Rows are nested call paths and must not be summed.

| Function/path | BASE CPU | 8ms CPU | Delta |
| --- | ---: | ---: | ---: |
| tview Application.draw | 0.62s | 3.76s | +3.14s |
| lazyScreen.Show / tcell Show | 0.46s | 3.17s | +2.71s |
| tScreen.resize / devTty.WindowSize / IoctlGetWinsize | 0.30s | 2.97s | +2.67s |
| TerminalPane.Draw | 0.11s | 0.58s | +0.47s |
| Root Flex/Pages.Draw | 0.16s | 0.59s | +0.43s |
| TerminalPane.paintEmu | 0.06s | 0.19s | +0.13s |
| tScreen.draw (cell diff and write) | 0.16s | 0.20s | +0.04s |

The terminal-size ioctl accounts for about 84% of the 3.17s CPU increase. In the pinned tcell v2.13.8 source, every `Show()` calls `resize()`, which queries `WindowSize()` even when dimensions are unchanged. Drawing only a pane would retain this cost. Pump/channel functions do not appear among sampled CPU hotspots; the 2.99s attributed to the pump's queued draw is downstream drawing and flushing.

Exact controlled-window repaint counts are unavailable: the controlled relaunches did not enable `ARGUS_TYPING_LOG`, and no `[repaint]` records follow the original After shutdown. Earlier, separately labeled 8ms busy-split telemetry reports 1,177 output-driven frames in 30 seconds (39.23/s) at 12:27:46, with other busy windows around 38–44/s. The producer's 25 lines/s does not establish the number of actual redraws.

The proposed next experiment is a small `tcell.Tty` wrapper caching `WindowSize` for up to 100ms, invalidating before resize callbacks and on start/resume. A bounded fallback retains missed-notification recovery; normal Clear/Draw/Show and the existing repair Sync calls remain intact. Saving 1.3s of ioctl CPU would put the observed total near 16.6% of one core, within the guard; approaching BASE's ioctl cost suggests roughly 10–11%. These are estimates, not measurements or an approved implementation. Coordinator direction is required before code changes.

Coordinator approval added this cache to PR 1, retaining the 8ms cap. Aaron then amended the proposed TTL from 100ms to 1 second: the TTL is only a missed-notification backstop, and the estimated 3.3ms ioctl cost would otherwise retain roughly three CPU points at ten queries per second. Invalidation before resize callbacks and Start/Stop/resume remains mandatory. The approved candidate requires a fresh controlled BUSY measurement with `ARGUS_TYPING_LOG=1`, then an IDLE check only if BUSY is at most 17.38%.

### One-second terminal-size cache capture (invalid: workload not on screen)

**Excluded from the busy CPU decision.** The coordinator identified `cross-tab arrow: hera -> tasks` at 20:15:39.923 and entry into task `1793905725940955000` (`ai-strategy`) at 20:15:41.893. The producer pane was off-screen for the last approximately eight seconds of the 20:15:28–48 profile. Its measured CPU and repaint rates below are preserved for diagnosis only; they do not establish a passing busy result. Hold the producer until the coordinator requests a rerun on the unchanged Hera split.

- **Binary:** local commit `780d83c5a52f10c4667768ffbe9dedd9d524ac49`, Go 1.26.8, 8ms repaint cap and 1-second TTY-size cache. Path `/tmp/argus-paint-sizecache.pZpzAk/argus-repaint-8ms-cache`; SHA-256 `e708becd4b85292eb00f75786a60060982476021337e16cb36ea99bbba487dbd`. Launched with `ARGUS_TYPING_LOG=1`; the worker independently verified listener PID 64021's executable before profiling.
- **Setup:** startup 20:14:37.389 PST. Terminal resized from 143×37 to 214×52 at 20:14:48.972, before the profile; the coordinator reported a coordinator pane of 88×48. No rerender stop/restart or pane re-resolve appears in the surrounding 20:14–20:16 log interval. The first profile is retained. Load at capture was 7.04 / 5.62 / 5.17, uptime 18 days, 6:16.
- **Invalid profile:** the identical 24-second/600-line producer and 20-second profile both completed. Native profile starts at **20:15:28 PST**, duration **20.06s**, CPU samples **1.88s**, or **9.37% of one core**. Do not compare this changing-view workload against BASE's busy guard.
- **Comparability:** this capture occurred about six hours after the earlier variants and used 214×52 rather than 212×52. Its repaint rate below is substantially lower than the earlier, separately measured busy context; controlled 8ms/16ms rates were not logged. Retain these limits when attributing the entire CPU reduction to the cache.
- **Repaint telemetry:** the 30-second window ending 20:15:37.502 contains 121 wakes and 114 output-driven frames (**3.80/s**); the next window ending 20:16:07.501 contains 147 wakes and 147 frames (**4.90/s**). These windows bracket the producer/profile and include time outside it. They do not provide an exact 20-second repaint rate, and exclude the existing spinner/tick draws.
- **Interim typing:** the report from startup contains Tasks n=248, total median 6.223ms, and coordinator n=42, total median 6.472ms (quiet n=15, median 5.614ms), with zero no-echo samples. Tasks exceeds 200; these counts do not yet complete a three-pane latency capture on the cache binary.
- **Evidence:** `controlled-cache-busy.txt`, `controlled-cache-busy-cum.txt`, `controlled-cache-busy-start.txt`, `controlled-cache-busy-load.txt`, raw `pprof.argus-repaint-8ms-cache.samples.cpu.001.pb.gz`, `cache-repaint-windows.txt` and `cache-typing-interim.txt`, all in `/tmp/argus-paint-sizecache.pZpzAk/`. BUSY must be repeated; IDLE follows only after a valid busy pass.

Top cumulative CPU is Application.draw 0.91s, refreshTasksWithIDs 0.64s, TerminalPane.Draw 0.46s and tcell.Show 0.31s. Top flat entries are syscall.rawsyscalln 0.53s, runtime.memclrNoHeapPointers 0.32s, spanInlineMarkBits.init 0.15s and pthread_cond_wait 0.08s. WindowSize/resize/ioctl contributes **0.01s**, down from **2.97s** in the uncached 8ms busy profile; it remains in the call path but is no longer its CPU hotspot.

### One-second terminal-size cache busy rerun (valid, CPU guard failed)

- **Binary and view:** the same frozen cache binary, listener PID 64021 independently verified again. Coordinator signal `BUSY rerun go` at 21:48:51 requested the fixed Hera split, coordinator plus 2a worker, hands off. The previous terminal size remains 214×52.
- **Capture:** identical 24-second/600-line producer and 20-second pprof both completed. Native start **21:49:18 PST**, duration **20.14s**, CPU samples **3.85s**, or **19.12% of one core**. This is **+11.74 percentage points** versus BASE 7.38%, exceeding the **17.38%** pass bar by **1.74 points**. No IDLE capture follows this failed busy result.
- **View audit:** no `cross-tab`, `entering agent view`, `exiting agent view`, `fullscreen width change`, `pty resize`, `afterDraw resize`, `rerender: stopping`, pane bind/selection or selected marker occurs in the inclusive 21:49:18–21:49:39 audit. Earlier pane bind activity ended at 21:48:23.543, before capture. The run is retained as valid rather than repeated without a new instruction.
- **Painted workload:** the 30-second counter ending **21:49:37.543** records **1,193 wakes and 1,130 output-driven frames**, or **37.67 frames/s**. That window overlaps most of the profile and confirms the visible output workload was painted. The preceding counter ending 21:49:07.543 records 688 wakes and 609 frames (20.30/s). These counters do not establish an exact 20-second frame rate and do not count spinner/tick draws.
- **Machine:** load **4.32 / 3.94 / 3.60**, uptime 18 days, 7:50. This remains later than the BASE capture and two columns wider; the frozen TUI process has stayed running since 20:14:37.
- **CPU attribution:** flat `spanInlineMarkBits.init` **1.92s** (49.87%), `memclrNoHeapPointers` **0.56s**, `rawsyscalln` **0.45s**. Cumulative `Application.draw` **2.68s**, `mallocgc` **2.66s**, `HeraPage.Draw` **1.36s**, `baseScreen.SetContent` **1.32s**, `slicerunetostring` **1.23s**, `Show` **0.93s** and `TerminalPane.Draw` **0.88s**. These cumulative paths overlap. `WindowSize`/`IoctlGetWinsize` matches **no CPU samples**: the prior size-query hotspot is removed, while allocation work dominates this run.
- **Evidence:** `/tmp/argus-paint-sizecache.pZpzAk/pprof.argus-repaint-8ms-cache.samples.cpu.002.pb.gz`, `controlled-cache-rerun-busy.txt`, `controlled-cache-rerun-busy-cum.txt`, `controlled-cache-rerun-busy-start.txt`, `controlled-cache-rerun-busy-load.txt` and `cache-rerun-repaint-windows.txt`. Producer and profiler stopped; further tuning, push and PR await coordinator direction.

### Allocation follow-up benchmarks

The valid cache profile's `-peek` attribution places 1.22s under `SetContent`'s rune-slice-to-string conversion. Callers account for tview Box background fill 0.67s, our FillArea 0.29s, terminal cache replay 0.21s and fresh terminal paint 0.08s; smaller border/text/rail callers account for 0.07s. The local follow-up targets FillArea and terminal painting through `Put` with reused immutable strings. Pinned tview v0.42.0 exposes no public setter for its private `dontClear` flag, so Box fill suppression was explicitly skipped; normal Clear/Draw/Show and blank fills remain intact.

Fixed-viewport benchmarks use 88×48 cells, Go 1.26.8 on Apple M5 Max, three non-race runs per case. The fresh terminal fixture contains ASCII, accented text, CJK and ANSI styling; replay reuses the populated cache. Setup and emulator writes are outside the timed loop. These measure cell-paint work without terminal Show, not end-to-end CPU or echo latency.

| Benchmark | Before allocs/op | After allocs/op | Before B/op | After B/op | Before ns/op | After ns/op |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| FillArea | 4,224 | 1 | 16,896–16,898 | 4 | 180,058–187,724 | 120,774–125,104 |
| Fresh terminal paint | 4,224 | 0 | 17,152–17,155 | 0 | 361,698–361,792 | 283,619–294,550 |
| Terminal cache replay | 4,224 | 0 | 17,152 | 0 | 203,696–206,481 | 138,718–145,074 |

Reproduce with `go test ./internal/tui/widget ./internal/tui/terminal -run '^$' -bench 'BenchmarkFillArea|BenchmarkTerminalCellPainting' -benchmem -count=3`. Raw files: `/tmp/argus-paint-sizecache.pZpzAk/cell-alloc-before.txt` and `cell-alloc-after.txt`. Allocation tests fail against the previous implementation and pass after the change. SimulationScreen tests compare legacy SetContent output against the new fills and terminal glyphs/widths/styles, and restore the same frame from cache after clearing and changing the emulator. A fixture-only emulator drain/Close race was removed by using an emulator with no response reader; production emulator lifecycle is unchanged. A new live busy profile is still required before claiming the CPU guard passes.

### Cell-string build busy capture (valid but uncontrolled)

- **Binary:** commit `17caea8c16695e3b94a96f1ea30daa1ccfa521c3`, `/tmp/argus-paint-cellstrings.BjC0vj/argus-repaint-cellstrings`, SHA-256 `888fb572cd02f56cebed59efaae9fa8dbeb70801f7ae43e68b9beb40f607678d`. `ARGUS_TYPING_LOG=1` is active. The worker independently verified listener PID 4393's executable before capture.
- **Protocol and result:** the same 24-second/600-line producer and 20-second profile completed. Native start **2026-11-06 00:42:56 PST**, duration **20.06s**, CPU samples **7.03s**, or **35.05% of one core**. The coordinator classified this as **valid but uncontrolled** because load and refresh costs differ substantially from the earlier BASE; the old 17.38% threshold is not applied as a pass/fail verdict. A fresh back-to-back comparison is required before IDLE.
- **View audit:** the inclusive 00:42:56–00:43:17 log window contains no cross-tab, entering/exiting agent view, fullscreen width change, PTY resize, afterDraw resize, rerender stop, bind, selection or selected marker. The terminal had resized to 214×52 at 00:41:15.281, before capture; coordinator-reported pane bind at 00:41:25 is also outside it. The visible split workload remained painted, so the run is retained as valid for the view audit.
- **Repaint rate:** the 30-second counter ending **00:43:14.806** contains **1,195 wakes / 1,052 output-driven frames**, or **35.07 frames/s**, and overlaps most of the CPU profile. The preceding counter ending 00:42:44.806 has 778 wakes / 653 frames (**21.77/s**). These counters include time outside the profile and exclude spinner/tick draws.
- **Machine context:** uptime 18 days, 10:44; load **16.37 / 18.17 / 14.59**, compared with **4.32 / 3.94 / 3.60** for the earlier valid cache run. This is a different TUI process, roughly three hours later, still two columns wider than BASE. Retain these workload/time/machine differences rather than attributing the total CPU increase solely to the allocation change.
- **Targeted costs:** `slicerunetostring` falls **1.23s→0.36s**, and `mallocgc` **2.66s→0.67s** relative to the prior valid cache profile. Remaining `SetContent` CPU is **1.10s**, chiefly the unmodified tview Box fill (**1.02s**); FillArea and terminal paint/cache no longer appear among its callers. `WindowSize`/`IoctlGetWinsize` has no samples.
- **Other costs:** cumulative Application.draw **4.08s**, Show **2.01s** (prior 0.93s), refreshTasksWithIDs **2.45s** (prior 0.80s), Hera doRefresh **1.33s**; flat rawsyscalln **2.38s**. Cumulative paths overlap. The higher tick/refresh and flush costs require coordinator direction before another tuning change or a new matching BASE measurement.
- **Evidence:** `/tmp/argus-paint-cellstrings.BjC0vj/pprof.argus-repaint-cellstrings.samples.cpu.001.pb.gz`, `controlled-cellstrings-busy.txt`, `controlled-cellstrings-busy-cum.txt`, `controlled-cellstrings-busy-start.txt`, `controlled-cellstrings-busy-load.txt` and `cellstrings-repaint-windows.txt`. Producer/profiler stopped; no push or PR.

### Fresh BASE/cellstrings comparison (coordinator directed)

Following the valid-but-uncontrolled run, the coordinator directed a back-to-back A/B on 2026-11-06. Aaron first relaunches frozen BASE `/tmp/argus-paint-baseline-v2.CyRaje/argus-baseline`, then the unchanged cellstrings binary in the identical split view. Each capture requires an explicit go, executable verification, the same 24-second/600-line producer, a 20-second CPU profile, load averages and the full view/resize/restart/bind audit. The acceptance bar is **cellstrings CPU ≤ fresh BASE CPU +10 percentage points**, replacing the stale absolute limit for this comparison. Report draw/flush CPU separately from refreshTasksWithIDs and Hera.doRefresh; cumulative nested paths must not be added or subtracted twice. No code changes, push or PR are authorized during the A/B.

### Operator steps

The coordinator approved completing the remaining Before and After typing and busy CPU captures back-to-back. Keep the same terminal size and daemon/supervisor deployment, and choose **Skip/Esc** in both skew modals. Close only the TUI between runs.

1. Launch frozen Before: `ARGUS_TYPING_LOG=1 /tmp/argus-paint-baseline-v2.CyRaje/argus-baseline`.
2. Record the start time, load and task count. In each of Tasks, Hera coordinator and Hera worker, type at least 200 keystrokes without submitting, then clear the composer. With an agent streaming on screen, tell the coordinator `BEFORE BUSY NOW` and hold that screen for 60 seconds so the worker can capture 20 seconds of CPU.
3. Record the end time and save the bounded latency report immediately. Quit this TUI, then launch After: `ARGUS_TYPING_LOG=1 /tmp/argus-paint-after.GB4lCf/argus-repaint`.
4. Repeat the same three-pane typing and streaming workload, reporting `AFTER BUSY NOW`. Record start/end times, load and task count again; save the bounded report. An additional hands-off After window permits the matching idle profile; the earlier idle Before numbers above remain retained.

The matching report tool is `/tmp/argus-paint-after.GB4lCf/argus-latency`. The worker captures CPU from `http://127.0.0.1:6061/debug/pprof/profile?seconds=20` in each busy window. CPU comparison, latency tables, any frame-cap tuning, archive, push and PR await this paired capture and the coordinator's next instruction.

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
