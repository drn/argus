**Design doc:** `openspec/changes/batch-pr-poll-graphql/design.md`

## 1. Tests

- [x] 1.1 Write failing tests in `internal/gitutil` for `FetchPRStatesBatch`: builds one aliased query for N branches, parses per-branch results, maps `nodes:[]` → `none`, resolves a deleted-merged-branch PR to `merged-closed` (table test with a fake `gh api graphql` runner seam, mirroring the existing `prRunner` injection in `pr.go`)
- [x] 1.2 Write failing tests for repo resolution: `owner/name` parsed from a cached `pr`/`url`; fallback to worktree default repo when no url; grouping multiple branches that share a repo into one group
- [x] 1.3 Write failing tests in `internal/daemon/pr_poll_test.go` for `pollPRStatesOnce` batched path: one batch call per repo group; terminal (`merged-closed`) tasks excluded before grouping; keep-stale on group-query error; authoritative write (incl. `none`) on success; chunking when a group exceeds the alias cap
- [x] 1.4 Confirm every `it should X` criterion in `design.md` has a corresponding failing test (Prove-It Pattern) before implementing

## 2. Batch fetcher in gitutil

**Depends on:** Stage 1

- [x] 2.1 Add an alias-safe query builder: one `repository(owner,name)` block with `tN:` aliases (N = sanitized task id) of `pullRequests(headRefName:…, first:1, orderBy:{field:CREATED_AT,direction:DESC}){ nodes{ state isDraft reviewDecision url } }`, plus a top-level `rateLimit { cost remaining }` field
- [x] 2.2 Implement `FetchPRStatesBatch(ctx, repo, branches) (map[branch]PRResult, error)`: write the query to a temp file, invoke `gh api graphql -F query=@file` via an injectable runner seam (default real `gh`), parse JSON, map each alias back to its branch, reuse the existing state-mapping logic from `FetchPRState`/`prstate.go`
- [x] 2.3 Implement the per-query alias cap (default 100) with sequential chunking — chunking lives daemon-side (Stage 3, `d.prAliasCap`); `FetchPRStatesBatch` issues exactly one query per call
- [x] 2.4 Keep `FetchPRState` (single) in place as a fallback / non-poller helper; do not delete it in this change

## 3. Repo resolution + poller wiring

**Depends on:** Stage 1

- [x] 3.1 Add a repo-resolution helper: parse `owner/name` from a cached `pr`/`url`; fall back to the worktree's default GitHub repo (the repo `gh` targets for that dir), resolved off the UI thread like existing git ops
- [x] 3.2 Rework `pollPRStatesOnce` to: filter eligible tasks (unchanged terminal-skip), resolve each task's repo, group by `(owner,name)`, call `FetchPRStatesBatch` once per group, then apply per-branch results to `task_meta` with the existing keep-stale (group error) / authoritative-write (success, incl. `none`) semantics
- [x] 3.3 Extend the uxlog: keep the per-cycle `[pr] poll: eligible/skipped/written/errored` summary; add a per-repo `[pr] poll: repo=o/n branches=K cost=C` line (cost from the query's `rateLimit`)
- [x] 3.4 Inject the batch runner into the daemon the same way `prFetch: gitutil.FetchPRState` is bound today, so tests can substitute a fake

## 4. Verify + document

**Depends on:** Stage 2, Stage 3

- [ ] 4.1 `make pre-pr` green (build, vet, fmt-check, lint-pr, vuln, test-cover-gate); coverage on touched packages ≥95%
- [x] 4.2 Add a gotcha to `context/knowledge/gotchas/` (daemon-rpc.md or misc.md): `gh pr view`/`pullRequests` hit the GraphQL bucket not REST; batch by repo; `pullRequests(headRefName:)` survives branch deletion where `ref(...)` returns null; cost = ceil(nodeCount/100)
- [ ] 4.3 Dogfood: deploy onto the running daemon (restart required), watch `gh api rate_limit` graphql bucket drop and `[pr] poll: repo=… cost=…` lines confirm ~1 point/cycle

## 5. Pre-req (user-gated, outside execution)

- [ ] 5.1 Confirm with the user whether to `openspec archive add-pr-poll-terminal-skip` first so `pr-status` enters base specs; if deferred, this change's delta applies against the change-folder definition as the effective base
