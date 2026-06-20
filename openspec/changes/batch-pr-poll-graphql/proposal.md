## Why

The daemon PR-status poller shells out `gh pr view` once per eligible task every 60s, which consumes the GitHub **GraphQL** rate budget (`gh pr view` is GraphQL-backed). At ~100 branched tasks that is ~6,000 points/hr against a 5,000/hr ceiling, exhausting the budget mid-window. #773's terminal-skip helps only marginally because most eligible tasks are branch-with-no-PR (`none`), which are polled in perpetuity. GraphQL bills by query complexity, not request count, so one batched query per repo replaces N per-task queries at ~1/50th the cost.

## What Changes

- **MODIFIED:** The poller fetches PR state with one batched GraphQL query per distinct PR repo per cycle (aliased `pullRequests(headRefName:)` lookups via `gh api graphql`), instead of one `gh pr view` per task.
- Add `gitutil.FetchPRStatesBatch(ctx, repo, branches)` returning a per-branch result map; the poller calls it once per repo group.
- Resolve each task's PR repo as `owner/name` — from its cached `task_meta` `pr`/`url` when present, else the worktree's default GitHub repo — and group branches by repo.
- Chunk aliases at a safe cap (default 100 branches/query) to stay in the cheapest GraphQL point tier.
- Extend the per-cycle uxlog with per-repo query cost for observability.
- **Preserved (no behavior change):** #773 terminal-state skip, keep-stale-on-transient-error, `task_meta` `pr`/`state`+`url` cache, restart-survival, UI-never-shells-out, clean shutdown, meta cleanup on delete/archive.

## Capabilities

**New Capabilities:** none.

**Modified Capabilities:**

- `pr-status` — the "Cached, non-blocking polling" requirement changes its fetch mechanism from per-task to batched-per-repo; a new requirement covers repo resolution + batched GraphQL fetch.

## Impact

- **Code:** `internal/daemon/daemon.go` (`pollPRStatesOnce` grouping + batch invocation), `internal/gitutil/pr.go` (new `FetchPRStatesBatch`, repo-resolution helper), `internal/model/prstate.go` (parse path unchanged; reused). Tests in `internal/daemon/pr_poll_test.go` and `internal/gitutil`.
- **External:** GitHub GraphQL usage drops from ~O(tasks) to ~O(repos) points per cycle. No new dependency; `gh` remains the only GitHub access path.
- **Deploy:** in-process behavior change, no flag, no schema/migration. Requires a daemon restart onto the new binary. Pre-req: archive `add-pr-poll-terminal-skip` (user-gated) so `pr-status` is in base specs.
- **Out of scope / unchanged:** TUI and REST API PR-state reads (pure cache), user-triggered `gh pr view --web` / `gh repo view --web` openers.
