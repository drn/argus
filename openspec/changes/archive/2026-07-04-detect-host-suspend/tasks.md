## 1. Detection + broadcast primitives (TDD)

- [x] 1.1 New file `internal/daemon/hostwatch.go`: constants (`hostSuspendInterval` 30s, `hostSuspendThreshold` 3m, `hostSuspendMessageType` `ARGUS_HOST_SUSPENDED`); pure `detectSuspendGap(prev, now, threshold) (gap, bool)`.
- [x] 1.2 `hostSuspendBody(gap)` / `hostSuspendNote(gap)`: JSON body carrying `type` + `approx_gap` + `approx_gap_seconds` + a human-readable `note` that tells a coordinator not to treat spanning silence as staleness.
- [x] 1.3 Free function `sendHostSuspendSignals(database, ids, gap) int` mirroring `sendBounceSignals` (SystemTaskID / KindNote / InsertSystemMessage; skip archived + missing).

## 2. Watchdog loop wiring

- [x] 2.1 `(*Daemon).hostSuspendTick(prev, now) time.Time`: detect via `detectSuspendGap`; on a hit, broadcast to `d.runner.Running()` via `sendHostSuspendSignals`; always return `now` as the new baseline (one-shot, no dedup).
- [x] 2.2 `(*Daemon).runHostSuspendWatcher()`: ticker loop gated by `d.done`; baseline stamped with monotonic stripped (`time.Now().Round(0)`) before the loop so the first tick compares against a real baseline.
- [x] 2.3 Wire `go d.runHostSuspendWatcher()` in `Serve` beside `runPRPoller`, UNCONDITIONALLY (not under `cfg.Hera.Enabled`).

## 3. Tests

- [x] 3.1 `detectSuspendGap` table test: normal gap → false; large gap → true; exact-threshold boundary; zero/negative → false.
- [x] 3.2 `sendHostSuspendSignals` (in-memory DB, explicit IDs, mirroring `bounce_test.go`): note lands for a live task with correct `From`/`Kind` and a body carrying the type + gap; archived + missing skipped; empty IDs → 0; multiple tasks each get exactly one.
- [x] 3.3 `hostSuspendTick` via `testDaemon(t)` + `SetPendingRestartForTest`: a large gap posts one note per running task; the next normal-cadence tick posts nothing (one-shot); a first normal tick posts nothing (baseline).

## 4. Docs & gates

- [x] 4.1 Add a gotcha bullet to `context/knowledge/gotchas/daemon-rpc.md` (the ARGUS_BOUNCED home) capturing the invariant; bump the count in `context/knowledge/index.md`.
- [x] 4.2 `make pre-pr` green; `openspec validate --all --strict` passes (if the CLI is present).
- [x] 4.3 Archive this change within the branch (base `daemon-lifecycle` spec updated atomically) before opening the PR.
