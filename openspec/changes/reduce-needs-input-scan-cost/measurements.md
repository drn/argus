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

Pending Aaron's TUI relaunch and the coordinator's capture go. No performance improvement is claimed before the paired captures.

## Local validation

- Existing agent and TUI race suites passed with their assertions unchanged.
- New regressions cover every design work-table row's exact read/render counts, raw-hit laziness, separate stripped windows, recorded real-byte fixture equivalence, cross-session render reuse, idle transitions, archive filtering, rotation snapshots and blocked-role auto-clear reuse.
- `TestScanRotationRetainsReadSnapshot` failed against the original scan before the fix.
- `TestSupervisorSurfaceDigest` passed; neither surface manifest nor any file it names changed. No supervisor restart is needed. Web and macOS are unaffected; the REST contract is unchanged.
- `make pre-pr` passed build, vet, formatting and lint, then exited nonzero at the advisory vulnerability step for the 11 known Go 1.26.8 standard-library CVEs. The coordinator directed keeping the repository toolchain and running the coverage gate separately, matching CI's advisory vulnerability policy.
- The initial coverage run reproduced the known sandbox environment leak in `TestHandleSessionExit_Recapture` (`exit_relay_test.go:136`). The isolated race test passed with `CODEX_HOME`, `CODEX_SQLITE_HOME`, `ARGUS_TASK_ID`, `ARGUS_ARCHETYPE` and `ARGUS_MODEL` unset. The full `make test-cover-gate` with the same isolation passed: **89.0% filtered coverage** (41,002/46,076 statements), above the 88% floor. Its complete output is retained at `/tmp/argus-paint-pr2-1793957947116133000/cover-isolated.log`.
- Standalone `hera-review` and `hera-review-test-adversary` completed with no unresolved findings after strengthening the raw-miss render fixture and adding the exact scan work-table checks.
