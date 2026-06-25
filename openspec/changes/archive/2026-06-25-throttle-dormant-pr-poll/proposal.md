## Why

The daemon PR-status poller re-queries every eligible branch every 60s. On a real working set (208 tasks, 82 non-terminal branches) that is ~82 GraphQL branch-lookups per minute ≈ ~4,900 GitHub GraphQL points/hour — essentially the entire 5,000/hr budget — leaving the account's GraphQL bucket exhausted for everything else.

The earlier fixes (`#773` terminal-state skip, `#782` per-repo batching) helped but did **not** solve it: GitHub's GraphQL budget is **cost-based** (≈ 1 unit per branch resolved), not request-based, so collapsing 82 lookups into one HTTP request still costs ~82 units. Worse, **78 of the 82 eligible branches have no PR at all** (`none` state, which is non-terminal) and **62 have had no activity in over a week** — the poller spends ~95% of the budget re-confirming that dormant branches still have no PR.

## What Changes

- Make the poller's per-task cadence **tiered by dormancy** instead of polling every eligible branch every cycle. Cadence is derived from the task's most recent lifecycle timestamp (`max(ended_at, started_at, created_at)`):
  - within 1h → every cycle (60s)
  - 1h–24h → every 5th cycle (~5m)
  - 24h–7d → every 15th cycle (~15m)
  - older than 7d → every 30th cycle (~30m)
- **Open-PR hot floor:** a branch whose cached state is an open PR (`draft` / `awaiting-review` / `changes-requested` / `approved`) is polled every cycle regardless of age, so a reviewer merging/approving externally still surfaces within ~60s.
- **Spread** selection across cycles by task-id hash so each cycle polls a roughly constant slice rather than all dormant branches landing together.
- Add an operator **kill-switch**: a `pr-poller.disabled` sentinel file under the data dir pauses the poller (zero GitHub queries) with no daemon restart; remove it to resume.

Expected steady-state cost on the current working set: ~10 branch-lookups/cycle (≈600 points/hr) instead of ~82 (≈4,900/hr) — and it improves as dormant tasks accumulate.

## Capabilities

### Modified Capabilities

- `pr-status`: the polling cadence is now per-task and dormancy-tiered (was a single fixed interval over all eligible tasks), with an open-PR hot floor and an operator kill-switch.

## Impact

- **Modified code:** `internal/daemon/daemon.go` (per-task cadence gate in `pollPRStatesOnce`, poll-cycle counter in `runPRPoller`, kill-switch sentinel check).
- **Tests:** `internal/daemon/pr_poll_test.go` (tier selection, open-PR floor, stride spread, kill-switch pause/resume).
- **Dependencies:** none added.
- **Data:** no schema change — cadence is derived from existing `tasks` timestamps and the existing `task_meta` `pr`/`state` cache. The kill-switch reads a sentinel file, writes nothing.
