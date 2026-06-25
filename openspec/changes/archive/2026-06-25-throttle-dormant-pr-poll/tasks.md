# Tasks: throttle-dormant-pr-poll

## 1. Tests (TDD — write failing first)

- [x] 1.1 `prPollCadence` tier helper: table test mapping age buckets (within 1h, 1h–24h, 24h–7d, >7d) to strides (1, 5, 15, 30) from `max(ended_at, started_at, created_at)`.
- [x] 1.2 Open-PR hot floor: a >7d-old task whose cached `pr`/`state` is `awaiting-review`/`draft`/`changes-requested`/`approved` resolves to stride 1.
- [x] 1.3 Selection gate: given a poll-cycle counter, a stride-30 task is selected on exactly one of any 30 consecutive cycles; a stride-1 task on every cycle.
- [x] 1.4 Spread: two stride-30 tasks with different ids are selected on different cycles (not both every 30th-from-zero).
- [x] 1.5 `pollPRStatesOnce` integration: a dormant unselected task issues no `prBatchFetch` call and its cached state is untouched; an active/open-PR task is fetched.
- [x] 1.6 Kill-switch (already landed `25827d8b`): sentinel present → zero fetches, nothing written; removed → resumes. (regression-guard the existing test)

## 2. Implement

**Depends on:** Stage 1

- [x] 2.1 Add `pollCycle uint64` to the `Daemon` struct; increment once per tick in `runPRPoller` and pass the current value into `pollPRStatesOnce` (or read a field) so selection is deterministic per cycle.
- [x] 2.2 Add `prPollCadenceStride(t *model.Task, prState string, now time.Time) int` — pure function returning the stride from dormancy tier with the open-PR floor.
- [x] 2.3 In `pollPRStatesOnce`, after terminal-skip filtering, drop tasks where `(pollCycle + hash(taskID)) % stride != 0` (stride 1 ⇒ always kept). Count/log skipped-by-cadence distinctly from terminal skips.
- [x] 2.4 Keep the `eligible == written + errored + skipped` cycle invariant intact (cadence-skipped tasks counted as skipped).

## 3. Verify

**Depends on:** Stage 2

- [x] 3.1 `make pre-pr` green (build + vet + fmt-check + lint-pr + vuln + test-cover-gate).
- [x] 3.2 Dogfood-deploy to the live daemon; remove the `pr-poller.disabled` sentinel; confirm via daemon GraphQL pace that steady-state cost drops to ~10 lookups/cycle while open PRs still refresh each cycle.

## 4. Docs

- [x] 4.1 Add the cost-based-budget + dormancy-cadence gotcha to `context/knowledge/gotchas/daemon-rpc.md` (PR poller section).
- [x] 4.2 `openspec archive throttle-dormant-pr-poll` once shipped.
