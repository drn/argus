## Context

The daemon's `pollPRStatesOnce` (`internal/daemon/daemon.go`) builds its eligible set as "every task that is not archived AND has a non-empty branch", then fans out a bounded pool of `gh pr view <branch>` calls (one GraphQL request each) and persists the result into `task_meta` namespace `pr` (keys `state`, `url`). It runs every 60s via `runPRPoller`.

The bug: it never checks whether a branch's PR has already reached a terminal state. A merged or closed PR cannot transition again, yet the poller keeps issuing a GraphQL call for it every minute. With 125 eligible tasks (mostly old, merged work), that is 7,500 GraphQL calls/hr against GitHub's 5,000/hr GraphQL ceiling — the budget is fully drained, and live `gh api rate_limit` confirms graphql 5000/5000 while REST `core` 0/5000.

The cached PR state is already persisted durably in `task_meta` namespace `pr` — the single writer is this poller, and `db.ListMetaByNamespace("pr")` already exists as the indexed batch read (the TUI and REST DTO use it). So the fix needs no schema change.

## Goals / Non-Goals

**Goals:**

- Exclude tasks whose cached PR state is terminal (`merged-closed`) from the poll's eligible set, so they are never re-polled.
- Make the skip survive a daemon restart by reading the same persisted `task_meta` cache (not an in-memory set).
- Keep open / draft / no-cached-state / no-PR tasks fully eligible.
- Centralize terminal-state detection in one helper.

**Non-Goals:**

- Changing poll cadence (60s) or concurrency cap (4).
- Changing the keep-stale-on-error contract or the gh-absent/unauthenticated handling.
- Adding any new persisted column, REST endpoint, keybinding, or UI surface.
- Backfilling/purging existing `task_meta` rows — the filter is read-time, so the first post-deploy tick already trims correctly.

## Decisions

**Decision: terminal = `PRMergedClosed` only, via a `PRState.IsTerminal()` helper.**
`gh pr view` collapses both `MERGED` and `CLOSED` into `model.PRMergedClosed` (see `gitutil.mapPRState`), so the single terminal sentinel is `PRMergedClosed` (stable string `"merged-closed"`). `PRNone`, `PRDraft`, `PRAwaitingReview`, `PRChangesRequested`, `PRApproved` are all non-terminal (an open PR can merge; "none" can gain a PR). `PRUnknown` (gh absent/unauthenticated) is non-terminal — we must keep trying once gh becomes available. Adding `func (s PRState) IsTerminal() bool { return s == PRMergedClosed }` keeps the rule in one place and is unit-tested over the full enum.

*Alternative considered:* inline `state == model.PRMergedClosed` in the daemon. Rejected — scatters the terminal definition and is harder to evolve if GitHub ever splits merged vs closed back out.

**Decision: read the cache via `ListMetaByNamespace("pr")` once per tick, parse `state` with `model.ParsePRState`.**
`pollPRStatesOnce` already calls `d.db.Tasks()`; we add one indexed `ListMetaByNamespace("pr")` read up front (the same single-query batch read the TUI tick uses). For each candidate task, look up `prMeta[t.ID]["state"]`; if it parses to a value whose `IsTerminal()` is true, skip it (and count + uxlog). A parse error or missing entry means "no known terminal state" → remain eligible (fail-open: we'd rather poll once too often than wrongly suppress a live PR).

*Alternative considered:* an in-memory `map[taskID]bool` of known-terminal tasks. Rejected — it would not survive a daemon bounce, so every restart would re-poll all 125 and the bug returns on each launch. Reading the persisted cache is the load-bearing correctness property.

**Decision: read-time filter, no migration.**
Because the filter reads the live cache each tick, the very first poll after deploy already trims terminal tasks. No one-off backfill script is needed.

## Risks / Trade-offs

- **A task's cached state is wrong/stale terminal (e.g. a branch reused after its PR merged).** → In practice argus branches are per-task and immutable once merged; a merged PR for `argus/<task>` does not reopen. If a brand-new PR were ever opened on the same branch, the skip would suppress it. Mitigation: the risk is purely theoretical for argus's one-branch-per-task model, and the budget win (eliminating ~120 dead polls/tick) far outweighs it. Documented as a gotcha.
- **`ListMetaByNamespace` failure.** → Treat as fail-open: if the batch read errors, log and fall back to the prior behavior (poll everything eligible) rather than skipping nothing or skipping everything. A transient meta-read failure must not silently stop all polling.

## Migration Plan

No migration. Ship the code; the next 60s tick reads the existing `task_meta` cache and trims terminal tasks. Rollback is a plain revert — no data shape changed.
