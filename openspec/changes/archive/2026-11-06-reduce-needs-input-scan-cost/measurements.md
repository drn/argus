# Needs-input scan measurements

## Builds

Both binaries use the repository's normal Go 1.26.8 toolchain and identical `go build -buildvcs=false` flags. Neither binary was deployed; Aaron alone relaunches the TUI, choosing Skip on the skew modal.

| Build | Source | Path | SHA-256 |
| --- | --- | --- | --- |
| Base | Master `1136f0ba`, freshly built | `/tmp/argus-paint-pr2-1793957947116133000/argus-base` | `67b85f37ba2817bd94b77ed64a9649813a463f660abf5e8bdd51f72250e3118a` |
| Scan reduction | This change | `/tmp/argus-paint-pr2-1793957947116133000/argus-scan` | `98711f758e742d905ee76f35f87bc476307d8fd4eac2a9a60d13ccd255006f2a` |

## Capture procedure

Capture Base busy, Base idle, scan busy, scan idle back to back on the coordinator's go, recording load average and running-task count for each. Verify the executable listening on port 6061 before each capture.

- **Busy:** Projects split view, coordinator left, worker terminal right; the 24-second, 600-line producer during a 20-second CPU profile.
- **Idle:** Tasks tab, groups collapsed, nothing selected, Preview empty, no producer.
- **Audit:** Check the capture window's UX log for tab changes, agent-view enter/exit, fullscreen width changes, PTY and after-draw resizes, rerender stops, bind/selection changes, and `[repaint]` FPS.
- **Comparison:** Report cumulative CPU rows for `refreshTasksWithIDs`, `detectNeedsInputSticky`, `readSessionLogRawTail`, and `os.Open` side by side, plus the stall and per-pane typing latency figures. Single-build comparisons against an earlier baseline are not valid under this machine's variable load.

## Results

### Baseline captures

Both captures independently verified port 6061's listener as PID 92918, executable `/private/tmp/argus-paint-pr2-1793957947116133000/argus-base`. The normal Go 1.26.8 toolchain was used; no tests, builds or lint ran during either capture.

| Capture | Native start (PST, 2026-11-06) | Duration | CPU samples | One-core CPU | Start load (1/5/15 min) | End load |
| --- | --- | --- | --- | --- | --- | --- |
| Base busy | 09:05:28 | 20.13s | 3.52s | 17.49% | 3.22 / 3.86 / 4.23, setup snapshot at 09:05:17 | 4.82 / 4.19 / 4.33 at 09:05:51 |
| Base idle | 09:21:53 | 20.06s | 870ms | 4.34% | 3.68 / 4.31 / 5.01 | 5.94 / 4.75 / 5.15 at 09:22:13 |

The busy producer was the same `controlled-output.py` used in PR 1: 600 lines over 24 seconds. Idle had no producer and began 17 seconds after the Tasks-tab transition. Both audits are **valid**: no view, selection, bind, fullscreen, PTY-size, after-draw resize or rerender-stop changes within their capture windows.

| Nested cumulative CPU row | Base busy | Base idle |
| --- | --- | --- |
| `Application.draw` | 2.69s | 260ms |
| `tScreen.Show` | 2.35s | 110ms |
| `refreshTasksWithIDs` | 420ms | 500ms |
| `detectNeedsInputSticky` | 370ms | 410ms |
| `readSessionLogRawTail` | 200ms | 170ms |
| `os.Open` | 210ms | 210ms |

These rows are nested and must not be added. `lazyScreen.Show` has the same cumulative value as its nested `tScreen.Show` row.

The 30-second repaint window ending 09:05:49.361 contained 1,408 output frames (**46.93fps**) and brackets the busy profile; it is not an exact 20-second rate. Idle has no repaint record within its profile. The preceding window ending 09:21:49.365 includes the view transition, so its 45 frames are not an idle-window FPS measurement. Neither typing report contains samples; typing medians are unavailable.

Evidence is retained in the scratch directory listed above: `base-busy{,-cum}.txt`, `base-idle{,-cum}.txt`, both `*-ux-window.log` and `*-typing.txt` files, and `pprof.argus-base.samples.cpu.001.pb.gz` / `.002.pb.gz` respectively.

### PR2 busy comparison

The listener was independently verified as PID 7518, executable `/private/tmp/argus-paint-pr2-1793957947116133000/argus-scan`. The same producer and first/final output-poll cadence completed; no heavy jobs ran. Native start was **09:24:39 PST**, duration **20.12s**, CPU samples **3.05s**, or **15.16% of one core**. Start load at 09:24:38 was **4.58 / 5.01 / 5.22**; end load at 09:25:02 was **4.50 / 4.96 / 5.20**.

Busy CPU fell **2.33 percentage points** from Base's 17.49%. It passes the coordinator's **27.49%** ceiling (Base +10 points). This is a paired observation under the recorded loads, not a measurement of keystroke latency.

| Nested cumulative CPU row | Base busy | PR2 busy | Delta |
| --- | --- | --- | --- |
| `Application.draw` | 2.69s | 2.49s | -200ms |
| `tScreen.Show` | 2.35s | 1.59s | -760ms |
| `refreshTasksWithIDs` | 420ms | 120ms | -300ms (-71.4%) |
| `detectNeedsInputSticky` | 370ms | 40ms | -330ms (-89.2%) |
| `readSessionLogRawTail` | 200ms | 10ms | -190ms (-95.0%) |
| `os.Open` | 210ms | 10ms | -200ms (-95.2%) |

The 10ms tail/open rows require `-nodefraction=0`: the default report drops cumulative nodes at or below 20ms. Rows remain nested and must not be added.

The UX audit from 09:24:38 to 09:25:02 is **valid**, with no view, resize, restart, bind, selection or preview events. The 30-second repaint window ending 09:25:08.654 contains 1,411 frames (**47.03fps**), close to Base's bracketing 46.93fps. The scan summary at 09:24:38.664 reports 18 running/active sessions, 15 idle, **3 tail reads and 3 renders**. No typing samples are present.

Evidence: `scan-busy.txt`, `scan-busy-cum.txt`, `scan-busy-cum-all.txt` (unfiltered rows), `scan-busy-ux-window.log`, `scan-busy-typing.txt`, and `pprof.argus-scan.samples.cpu.001.pb.gz` in the scratch directory above.

### PR2 idle comparison and four-capture summary

The listener remained PID 7518 at `argus-scan`. Native start was **09:30:18 PST**, 18 seconds after the Tasks-tab transition, duration **20.14s**, samples **250ms**, or **1.24% of one core**. Start load was **3.68 / 5.28 / 5.48**; end load at 09:30:38 was **4.14 / 5.28 / 5.47**. No producer or heavy jobs ran. The 09:30:18–39 UX audit is **valid**, with no view/resize/restart/bind/selection/preview events. No repaint record falls within the capture; the preceding record at 09:30:08.657 is outside it. Typing samples are absent.

| View | Base CPU | PR2 CPU | Delta | Verdict |
| --- | --- | --- | --- | --- |
| Busy split | 17.49% | 15.16% | -2.33 percentage points | Passes Base +10-point ceiling |
| Idle Tasks | 4.34% | 1.24% | -3.10 percentage points (-71.4%) | Below Base |

| Nested cumulative CPU row | Base idle | PR2 idle | Delta |
| --- | --- | --- | --- |
| `Application.draw` | 260ms | 60ms | -200ms |
| `tScreen.Show` | 110ms | 60ms | -50ms |
| `refreshTasksWithIDs` | 500ms | 120ms | -380ms (-76.0%) |
| `detectNeedsInputSticky` | 410ms | 50ms | -360ms (-87.8%) |
| `readSessionLogRawTail` | 170ms | 10ms | -160ms (-94.1%) |
| `os.Open` | 210ms | 10ms | -200ms (-95.2%) |

Evidence: `scan-idle.txt`, `scan-idle-cum-all.txt`, `scan-idle-ux-window.log`, `scan-idle-typing.txt`, and `pprof.argus-scan.samples.cpu.002.pb.gz`. Small rows are retained with `-nodefraction=0`. All four CPU captures have been reported to the coordinator with status `working`.

CPU sampling shows a cheaper scan under the recorded loads. It does **not** measure keystroke-to-echo latency. The coordinator accepted the existing tick/typing analysis below and authorized archiving and PR creation in message 21553, after Aaron saw the results. No additional live stall/typing session was required for this PR.

### Comparable tick evidence and remaining rail stalls

`typingTracker.tick` is byte-identical between Base and PR2 (Git blob `ba6cb202f585f30da9003f6d1ff2a29e271e61d7`). **Both builds log only closures of at least 16ms.** Percentiles of the logged lines alone are therefore censored and cannot be compared as whole-population percentiles. The new `[needsinput]` summary reports counts, not durations.

For a same-view comparison, use Base **09:04:23–09:07:46 PST** (before the Tasks switch at 09:07:47) and PR2 **09:24:23–09:27:46**. Each window has 204 `readManagedTasks` refresh records in 204 unique consecutive seconds, one per periodic tick. No zero values were substituted for unlogged closures.

| Matched Projects split window | Base | PR2 |
| --- | --- | --- |
| Observed refreshes | 204 | 204 |
| Logged closures ≥16ms | 204 | 32 |
| Closures below 16ms, inferred from the unchanged threshold | 0 (0%) | 172 (84.3%) |
| Whole-window p50 | 22.704ms | **<16ms**, exact value censored |
| Whole-window p90 | 111.885ms | 101.486ms |
| Whole-window p99 | 121.228ms | 115.166ms |
| Closures ≥80ms | 31 (15.2%) | 31 (15.2%) |

The PR2 p90/p99 ranks lie within the logged tail and can be recovered exactly; its median lies within the unlogged sub-16ms population. Within the actual idle CPU windows, each build has 20 periodic refresh records: Base logged all 20 (p50 **25.506ms**, p90 **29.965ms**, max **30.239ms**), while PR2 logged none, bounding every observed idle tick below 16ms.

All 31 ≥80ms closures in **each** matched Projects window finish within 1ms of an existing `[hera-view] rail refreshed` record. The median closure duration minus that nested rail duration drops from **23.279ms to 6.233ms**; this subtraction is diagnostic, not a separately instrumented scan timer.

The conspicuous minute bursts are the unchanged synchronous rail/board rebuild:

| PR2 completion time | Tick closure | Nested rail rebuild |
| --- | --- | --- |
| 09:28:04 | 112.856ms | 105.070ms |
| 09:28:05 | 114.359ms | 106.592ms |
| 09:28:06 | 114.218ms | 106.199ms |
| 09:29:04 | 103.086ms | 97.549ms |
| 09:29:05 | 99.392ms | 94.237ms |
| 09:29:06 | 106.891ms | 101.524ms |

Each rebuild includes 19 `[board]` snapshots. Before/after skipped-rail fingerprints move **33→38**, then **38→41**, consistent with the existing global `DataVersion` gate. The exact external writer is not identified by the log. The same minute-adjacent rebuild pattern exists on Base at 09:05:07–10, 09:06:07–10 and 09:07:08–10. Comparing Base's entire 18-minute window, much of it in Tasks/agent views, against PR2's mostly-Hera window would dilute Base's rail cost.

**Named follow-up: `reduce-hera-rail-rebuild-stalls`.** `HeraPage.doRefresh` / `BuildModel` still perform roughly 100ms of synchronous UI-thread work, including per-coordinator board reads, when the global DB fingerprint or runtime maps change. Those files are untouched by PR2. PR2 adds no minute scan/cache expiry, and the measured fleet of 18 is below the scan batch threshold of 24. This remaining typing-lag risk is accepted as separate work, owned by the coordinator, and is not fixed here.

Evidence: `base-matched-ticks.log`, `scan-matched-ticks.log` and `scan-minute-burst-evidence.log` in the scratch directory.

### Sparse typing observations

Although the CPU capture windows contain no typing samples, existing records elsewhere in each PID's window provide the following limited evidence. All samples have `overlap=0`, and all reports have `no-echo=0`.

| Window and pane | Samples | Total p50 / p90 / max | Quiet samples and p50 | Stall p50 / max |
| --- | --- | --- | --- | --- |
| Base 09:04:00–09:22:20, Hera coordinator | 35 | 5.969 / 12.348 / 40.143ms | 27, 6.059ms | 0 / 15.793ms |
| Base, Tasks | 47 | 5.530 / 15.603 / 34.157ms | 17, 5.530ms | 0 / 7.737ms |
| PR2 09:24:00–09:31:30, Hera coordinator | 14 | 7.585 / 17.708 / 48.632ms | 11, 7.786ms | 0 / 5.384ms |

Aaron's PR2 burst at **09:29:58.898–09:29:59.704** contains only six samples (five quiet): total p50 **8.420ms**, p90/max **48.632ms**; queue p50 **0.015ms**, max **0.033ms**; stall p50 **0**, max **5.384ms**. The longest sample's 45.004ms arrival component dominates its delay. There are no PR2 Tasks or worker-pane samples in these windows.

**These are small, unmatched typing samples, not a latency win or proof that the remaining 100ms rail stalls are harmless.** Evidence is retained as `base-pid-window-typing.{log,txt}` and `scan-pid-window-typing.{log,txt}`.

## Local validation

- Existing agent and TUI race suites passed with their assertions unchanged.
- New regressions cover every design work-table row's exact read/render counts, raw-hit laziness, separate stripped windows, recorded real-byte fixture equivalence, cross-session render reuse, idle transitions, archive filtering, rotation snapshots and blocked-role auto-clear reuse.
- `TestScanRotationRetainsReadSnapshot` failed against the original scan before the fix.
- `TestSupervisorSurfaceDigest` passed; neither surface manifest nor any file it names changed. No supervisor restart is needed. Web and macOS are unaffected; the REST contract is unchanged.
- `make pre-pr` passed build, vet, formatting and lint, then exited nonzero at the advisory vulnerability step for the 11 known Go 1.26.8 standard-library CVEs. The coordinator directed keeping the repository toolchain and running the coverage gate separately, matching CI's advisory vulnerability policy.
- The initial coverage run reproduced the known sandbox environment leak in `TestHandleSessionExit_Recapture` (`exit_relay_test.go:136`). The isolated race test passed with `CODEX_HOME`, `CODEX_SQLITE_HOME`, `ARGUS_TASK_ID`, `ARGUS_ARCHETYPE` and `ARGUS_MODEL` unset. The full `make test-cover-gate` with the same isolation passed, and the final shipping rerun also passed: **89.0% filtered coverage** (41,001/46,075 statements), above the 88% floor. Complete outputs are retained at `/tmp/argus-paint-pr2-1793957947116133000/cover-isolated.log` and `cover-ship.log`; the unmasked advisory `make pre-pr` rerun is in `pre-pr-ship.log`.
- Standalone `hera-review` and `hera-review-test-adversary` completed with no unresolved findings after strengthening the raw-miss render fixture and adding the exact scan work-table checks.
