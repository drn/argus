## Context

Host suspend (laptop sleep / hibernate / VM pause) freezes the whole machine: the daemon, the coordinator's PTY session, and every worker's PTY session all pause and resume together. A hera coordinator has no way to observe that this happened — it only sees "worker X has been silent for N hours", which reads identically to a stuck agent. The observed failure mode is the coordinator spawning duplicate `-retry` / `-retry2` plan nodes for workers that were merely asleep. We want to hand the coordinator a trustworthy signal at wake time, not add machine auto-correction.

## Decision 1 — Standalone daemon watchdog, NOT the heragater

The suspend gap could be detected by piggybacking on `heragater.Watcher.Start()`, which already runs an unconditional 1-minute ticker. We chose a small standalone watchdog loop in `internal/daemon` (`runHostSuspendWatcher`, wired in `Serve` beside `runPRPoller`) instead, for four reasons:

1. **The misjudgment is not plan-DAG-specific.** A coordinator running only bare `hera_spawn_worker`s (no plan nodes) misjudges concurrent silence exactly the same way — that is precisely the shape of the OTHER bug fixed in this same orchestrator (a bare-worker coordinator). Tying detection to the plan-DAG package would leave the bare-worker case uncovered.
2. **`cfg.Hera.Enabled` gating is wrong here.** The heragater only starts when Hera is enabled. Suspend detection must run whenever the daemon runs — any agent reasoning about elapsed time can misjudge, and even a non-coordinator receiving the note is harmless.
3. **The daemon already owns this shape of work.** `runPRPoller`, the MCP idle sweep, and the scheduler are all `go`-launched background loops gated by `d.done`. A watchdog is the same pattern; the heragater is architecturally a plan-DAG resolver, not a general daemon heartbeat.
4. **The broadcast is a bounce sibling.** It reuses `sendBounceSignals`'s exact primitive (`SystemTaskID` sender, `KindNote`, `InsertSystemMessage`), so `sendHostSuspendSignals` sits naturally next to it in the daemon package rather than reaching across into `internal/heragater`.

## Decision 2 — Detect on WALL-CLOCK time, not the monotonic clock

This is the load-bearing correctness point. Go's `time.Now()` carries both a wall-clock and a monotonic reading, and `t.Sub(u)` uses the MONOTONIC reading when both operands have one. On macOS the monotonic clock does not advance while the host is asleep, so a monotonic delta between two ticks straddling a sleep would report ~the interval (the CPU time the process actually ran), NOT the real elapsed time — defeating detection entirely.

The watchdog therefore stamps each tick timestamp with the monotonic reading STRIPPED (`time.Now().Round(0)`), so `now.Sub(prev)` is computed on the wall clock and reflects real elapsed time INCLUDING the sleep window. The pure detector `detectSuspendGap(prev, now, threshold)` just does `now.Sub(prev)`; the stripping happens at the loop call site so tests can pass synthetic times directly.

A wall-clock backward jump (NTP correction) yields a negative gap, which is below threshold and correctly does not fire. A large forward NTP jump is possible but bounded well under the threshold in practice; a deliberate manual clock change could false-positive, but the consequence is only an advisory note, which is harmless.

## Decision 3 — Interval and threshold

- `hostSuspendInterval = 30s` — the watchdog does almost nothing per tick (one wall-clock comparison), so a short interval is cheap. After a wake the frozen ticker's pending tick fires immediately, so detection latency at wake is near zero regardless; the interval mainly sets the "normal gap" baseline.
- `hostSuspendThreshold = 3m` — a gap must exceed this to count as a suspend. That is 6x the interval, comfortably above any ordinary scheduler / GC jitter (sub-second to low-seconds even on a hammered machine), while far below a real laptop sleep (minutes to hours). Generous by design, per the brief, to avoid false positives.

## Decision 4 — One-shot per suspend with no dedup bookkeeping

Each tick updates the baseline to `now` regardless of whether it fired. So the tick immediately after a suspend sees a normal-cadence gap (`now - <just-stamped baseline>` ≈ interval) and stays silent. The anomalous tick is naturally a one-time event; no per-event dedup state is needed. Go's `time.Ticker` also delivers at most one pending tick after a long freeze (the channel has capacity 1 and the runtime does not queue backlog), so there is no catch-up burst to guard against.

## Decision 5 — First comparison after start never fires

The loop stamps `prev = time.Now().Round(0)` BEFORE entering the tick loop, so the first tick compares against a real, recent baseline (~interval old) rather than a zero value. This removes the "no baseline" false-positive by construction — there is no special-cased skip flag. A genuine suspend occurring in the first interval after start still fires correctly (its first-tick gap is real and large).

## Decision 6 — Inbox-durable delivery, mirroring ARGUS_BOUNCED

The note is written with `InsertSystemMessage` only (no notifier / reliable-pane push), exactly like `sendBounceSignals`. The agent reads it on its next `task_inbox` poll — which hera coordinators and workers already do as a standing order. This keeps the fix a durable signal in the agent's own channel rather than a force-injected PTY write with idle-gating caveats, and matches the one existing precedent for daemon-originated agent signals. The body is JSON carrying a machine-detectable `type` (`ARGUS_HOST_SUSPENDED`, sibling to `ARGUS_BOUNCED`), the approximate gap, and a human-readable `note` telling a coordinator specifically not to treat the spanning silence as staleness.

## Risks / trade-offs

- **A coordinator that never polls its inbox before deciding to retry could still miss the note.** This is the same limitation ARGUS_BOUNCED has; both rely on the agent's standing inbox-poll order. Accepted — the alternative (force-injecting into the PTY) has its own idle-gating failure modes and is out of scope per the brief.
- **False positive from a manual clock change** — harmless (an advisory note); not worth guarding.

## Testing

- `detectSuspendGap` is pure — table test: normal gap → false, threshold-exceeding gap → true, exact-threshold boundary, zero/negative gap → false.
- `sendHostSuspendSignals` is a free function mirroring `sendBounceSignals` — tested against an in-memory DB with an explicit ID list (no fake runner), mirroring `bounce_test.go`: notes land for live tasks, archived/missing tasks skipped, empty list → 0, body carries the type + gap.
- `hostSuspendTick` is exercised through a `testDaemon(t)` whose in-process `*agent.Runner` is populated via `SetPendingRestartForTest` (the same trick `bounce_test.go` uses to make tasks appear in `Running()`): a large synthetic gap posts one note to every running task; the following normal-cadence tick posts nothing (one-shot); a first normal tick posts nothing. No real sleep anywhere — synthetic `time.Time` values are passed directly.
