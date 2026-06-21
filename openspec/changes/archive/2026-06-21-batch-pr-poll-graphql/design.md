## Context

The daemon PR-status poller (`runPRPoller` → `pollPRStatesOnce`, `internal/daemon/daemon.go`) refreshes each task's PR review state on a fixed 60s interval. Today it shells out `gh pr view <branch> --json state,isDraft,reviewDecision,url` once per eligible task, bounded by `prPollConcurrency=4`, via `gitutil.FetchPRState`.

`gh pr view` is GraphQL-backed (verified: it issues `POST /graphql`), so the poller consumes the GitHub **GraphQL** rate budget, not the REST `core` bucket. The GraphQL bucket is **5,000 points/hour**.

This was traced from a live exhaustion: `gh api rate_limit` showed `graphql` burning ~80–100 points/min while `core` sat at 26/5000. With ~101 non-archived branched tasks polled every 60s, that is ~6,000 points/hr — over the ceiling, exhausting the budget ~40 min into each hourly window.

PR #773 (`add-pr-poll-terminal-skip`, merged, capability `pr-status`) already excludes tasks whose cached PR state is terminal (`merged-closed`). It is necessary but not sufficient here: of Aaron's 101 eligible tasks only 8 were `merged-closed`; **86 are branch-with-no-PR (`none`)**, which are non-terminal by design and polled in perpetuity. Terminal-skip alone leaves the poller at ~5,600 points/hr — still over.

The structural fix is to stop scaling API cost with task count. GitHub GraphQL bills by query **complexity points** (`ceil(nodeCount / 100)`, minimum 1), not by request count. A single query can resolve many branches at once via field aliases, so the whole per-cycle cost collapses to ~1 point per repo regardless of task count.

## Goals / Non-Goals

**Goals:**

- Cut the poller's GraphQL budget consumption from ~O(tasks) points/cycle to ~O(repos) points/cycle (~50x reduction at current scale).
- Keep PR-state freshness identical: same fields (`state`, `isDraft`, `reviewDecision`, `url`), same 60s cadence, same `task_meta` cache.
- Preserve every behavioral contract from #773: terminal-state skip, keep-stale-on-transient-error, cache survives restart, UI never shells out, clean shutdown, meta cleanup on delete/archive.
- Resolve each task's PR repo correctly across Aaron's multi-remote worktrees (PRs live on `drn`, origin is `anutron`).

**Non-Goals:**

- Changing the poll interval or concurrency model as the primary fix (batching makes interval tuning unnecessary; leaving `prPollInterval` at 60s).
- Adding a Go GraphQL/HTTP client or handling GitHub tokens directly — invocation stays through `gh`.
- Changing how PR state is consumed by the TUI / REST API (they remain pure `task_meta` cache reads).
- Backfilling or migrating cached state — the first post-deploy cycle simply repopulates via the batched path.
- Touching the retired `depends_on` / DAG machinery or any non-poller GitHub call site (the user-triggered `gh pr view --web` / `gh repo view --web` openers are one-off and out of scope).

## Decisions

### Decision 1: One batched GraphQL query per repo per cycle, via `gh api graphql`

Replace per-task `gh pr view` with a single aliased query per distinct PR repo:

```graphql
query {
  rateLimit { cost remaining }
  repo: repository(owner: "<owner>", name: "<name>") {
    t<id0>: pullRequests(headRefName: "<branch0>", first: 1, orderBy: {field: CREATED_AT, direction: DESC}) {
      nodes { state isDraft reviewDecision url }
    }
    t<id1>: pullRequests(headRefName: "<branch1>", first: 1, orderBy: {field: CREATED_AT, direction: DESC}) { ... }
    ...
  }
}
```

Issued as `gh api graphql -F query=@<tmpfile>` (or `-f query=...`), reusing `gh`'s existing auth — no token handling, consistent with the current shell-out pattern and the security policy (no credential management in argus).

**Why `pullRequests(headRefName:)` and not `ref(qualifiedName:).associatedPullRequests`:** empirically, `ref(...)` returns `null` once a merged PR's branch is deleted, so terminal/merged PRs would silently read as "no PR." `pullRequests(headRefName:)` resolves from PR metadata, which persists after branch deletion — verified returning correct `MERGED` state for deleted-branch PRs. Cost measured at **1 point for a 3-branch batch**.

**Alternative considered — aliased `search(query: "repo:o/n type:pr head:b")`:** also batchable but search has fuzzier matching semantics and a separate, lower secondary limit; `pullRequests(headRefName:)` is an exact lookup and cheaper. Rejected.

**Alternative considered — Go GraphQL HTTP client:** avoids a subprocess but forces argus to source and store a GitHub token, violating the "no credential management" posture and adding a dependency. Rejected.

### Decision 2: Resolve PR repo per task — cached PR url first, then worktree git remote

A batched GraphQL query needs an explicit `owner/name`; `gh pr view` resolved this per-worktree for free. Resolution order per task:

1. If the task has a cached `task_meta` `pr`/`url` value, parse `owner/name` from the URL host/path. This is authoritative — it is exactly where the PR lives (e.g. `drn/argus`).
2. Otherwise resolve from the worktree's default GitHub repo the way `gh` would (the repo `gh` targets for that directory).

Tasks are then grouped by resolved `(owner, name)`; one query is issued per group. At Aaron's scale nearly all tasks resolve to `drn/argus`, collapsing to a single query per cycle.

**Why url-first:** worktrees carry both `anutron` (origin) and `drn` remotes, and PRs land on `drn`. Resolving purely from the git remote risks targeting `anutron`, where the head ref has no PR, yielding a false `none`. The cached url removes the ambiguity for any task that has ever had a PR observed.

**Alternative considered — single configured repo:** simplest query path but breaks multi-project/multi-repo use the moment a task targets a different repo. Rejected.

### Decision 3: New `gitutil.FetchPRStatesBatch(ctx, repo, branches) (map[branch]PRResult, error)`

Add a batch fetcher alongside (not deleting yet — Chesterton's Fence) `FetchPRState`. It builds the aliased query, invokes `gh api graphql`, parses the JSON, and returns a per-branch result map. `pollPRStatesOnce` calls it once per repo group. Alias keys derive from a sanitized task id (branches can contain characters illegal in GraphQL aliases; the id is alias-safe), and results map back via the alias→branch association the caller holds.

`FetchPRState` (single) is retained for any non-poller caller and as a fallback; the poller stops using it. If no other caller remains after wiring, removal is a follow-up, not part of this change.

### Decision 4: Preserve all caching/error semantics unchanged

- **Per-branch keep-stale:** a query-level error (network/timeout/auth) leaves every cached value in that repo group untouched — identical to today's keep-stale, just at repo granularity.
- **Per-alias authoritative write:** when the query succeeds, each branch's resolved state (including `none` when `nodes` is empty) is written to `task_meta` `pr`/`state`+`url`, exactly as today.
- **Terminal-skip (#773) stays upstream of fetch:** terminal tasks are filtered out of the eligible set *before* grouping, so they never enter a query.
- **uxlog summary preserved:** the existing `[pr] poll: eligible=N skipped=M written=X errored=Y` line still fires once per cycle; add per-repo query cost to the log for observability (`[pr] poll: repo=o/n branches=K cost=C`).

### Decision 5: Chunk aliases at a safe cap (default 100 branches/query)

`ceil(nodeCount/100)` means 100 single-node aliases ≈ 1–2 points. Cap aliases per query at 100; if a repo group exceeds the cap, split into sequential chunks. This bounds query size/complexity and stays in the cheapest point tier. At current scale chunking never triggers.

## Risks / Trade-offs

- **Wrong-repo resolution for never-had-a-PR tasks** → those fall to git-remote resolution; if a worktree's default repo differs from where its PR will eventually open, the first lookup returns `none` until a PR exists and a url is cached. Same failure mode as today's `gh pr view` when the default repo is wrong; not a regression. Mitigation: prefer the cached url, which is correct for any task that has had a PR.
- **GraphQL partial failure semantics** → a single malformed alias can fail the whole query. Mitigation: alias keys are sanitized task ids (always valid); on a whole-query error, keep-stale for the group (no worse than today losing one task's poll).
- **`gh api graphql` argv/temp-file handling** → very large queries via `-f` could hit argv limits. Mitigation: write the query to a temp file and use `-F query=@file`; the 100-alias chunk cap keeps queries small regardless.
- **Archive-ordering dependency on #773** → `pr-status` exists only in the unarchived `add-pr-poll-terminal-skip` change folder, not yet in base specs. Modifying it cleanly wants that change archived first. Mitigation: flagged as a pre-req task; if not archived, the delta still applies against the change-folder definition as the effective base. Decision deferred to the user (his PR-injection workflow governs archive timing).
- **Rate-limit reporting** → including `rateLimit { cost remaining }` in each query adds zero measurable cost and gives live budget observability in uxlog; cheap insurance against silent regressions.

## Migration Plan

1. (Pre-req, user-gated) Archive `add-pr-poll-terminal-skip` so `pr-status` enters base specs.
2. Ship the batched poller behind no flag (in-process behavior change; no config surface). First cycle repopulates `task_meta` via the batched path — no backfill needed.
3. Rollback: revert is a pure code revert; `task_meta` schema and contents are unchanged, so the old per-task poller resumes cleanly against the same cache.

## Alternatives considered

- **Interval back-off only** (60s → 180s): linear ~3x cut, leaves cost scaling with task count; creeps back over the ceiling as the task list grows. Rejected as a primary fix.
- **Stop polling long-lived `none` tasks** after N misses: helps the dominant 86-task case but adds state/heuristics and still scales with task count. Superseded by batching, which makes per-task cost irrelevant.

## Discovery findings

- `gh pr view --json` → `POST /graphql` (confirmed via `GH_DEBUG=api`). The poller burns the **GraphQL** bucket; REST `core` is idle. Always check the correct bucket with `gh api rate_limit` first.
- GraphQL cost is complexity-based: `cost:1` measured for a 3-branch aliased batch; `ceil(nodeCount/100)` min 1.
- `pullRequests(headRefName:)` returns correct state for merged PRs whose branches were deleted; `ref(...).associatedPullRequests` returns `null` for them.
- Live state at investigation: 101 non-archived branched tasks — 86 `none`, 8 `merged-closed`, 4 `awaiting-review`, 3 `draft`. Worktrees carry both `anutron` and `drn` remotes; PRs live on `drn`.
- The running daemon at investigation time predated #773 (zero `[pr] poll:` lines in `ux.log`), so deploying this change also requires a daemon restart onto a current binary.

## Acceptance criteria

**Batched fetch (Decision 1/3):**

- it should issue exactly one GraphQL query per distinct PR repo per poll cycle, not one per task
- it should resolve PR state for a branch whose merged PR's head branch has been deleted
- it should return the same fields the cache stores: state, isDraft-derived state, reviewDecision, url

**Repo resolution (Decision 2):**

- it should resolve a task's repo from its cached PR url when one exists
- it should fall back to the worktree's default GitHub repo when no PR url is cached
- it should group branches sharing a repo into a single query

**Preserved semantics (Decision 4):**

- it should exclude terminal (`merged-closed`) tasks from every query
- it should leave all cached values in a repo group unchanged when that group's query fails
- it should write state and url to task_meta for each branch the query resolves, including writing `none` when no PR exists
- it should emit the per-cycle `[pr] poll: eligible/skipped/written/errored` uxlog summary
- it should stop the poller goroutine on daemon shutdown without blocking

**Chunking (Decision 5):**

- it should split a repo group exceeding the alias cap into multiple sequential queries
