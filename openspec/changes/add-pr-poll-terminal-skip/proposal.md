## Why

The daemon PR poller re-polls **every** non-archived task with a branch every 60s, with no regard for whether the branch's PR has already reached a terminal state. In production this drains the entire GitHub GraphQL API budget: a live `gh api rate_limit` shows graphql at 5000/5000 used while REST `core` sits at 0/5000, confirming the drain is the poller's `gh pr view` (GraphQL) calls, not CI. With 125 eligible tasks that is 125 × 60 ticks/hr = 7,500 GraphQL calls/hr against a 5,000/hr ceiling — and most of those 125 are old completed tasks whose PRs are long since merged. A merged or closed PR can never change state again, so re-polling it can never return anything new.

## What Changes

- Make the poller's eligibility set in `pollPRStatesOnce` SKIP any task whose last-known cached PR state is terminal (merged or closed). Terminal states never change, so once observed they stick and the task is never polled again.
- Tasks with an OPEN/draft PR, with no cached state yet, or with no PR at all MUST still be polled (an open PR can merge; a branch with no PR yet may get one).
- The skip reads the SAME persisted `task_meta` namespace `pr` cache the poller writes, so the skip survives a daemon restart — a bounce does not re-poll the whole backlog.
- Centralize terminal-state detection as a single `PRState.IsTerminal()` helper rather than scattering string compares.
- Emit a uxlog line when a task is skipped for terminal state.

## Capabilities

### New Capabilities

<!-- None. -->

### Modified Capabilities

- `pr-status`: The "Cached, non-blocking polling" requirement gains a terminal-state eligibility filter — tasks whose cached state is `merged-closed` are excluded from the poll set, while open/no-state/no-PR tasks remain eligible.

## Impact

- **Modified code:** `internal/model/prstate.go` (add `IsTerminal()` helper), `internal/daemon/daemon.go` (`pollPRStatesOnce` reads `ListMetaByNamespace("pr")` once and skips terminal-state tasks, with a uxlog skip line).
- **New tests:** extend `internal/daemon/pr_poll_test.go` (terminal skip; open/no-state/no-PR still polled; persistence-backed skip survives a simulated restart) and `internal/model/prstate_test.go` (`IsTerminal` table).
- **Dependencies:** none added.
- **Data:** no schema change — the cache already lives in `task_meta` namespace `pr` (durable, survives restart).
- **Behavior preserved:** poll cadence (60s) and concurrency cap (4) are unchanged; only the eligible set is trimmed.
