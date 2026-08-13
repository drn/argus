## Context

`heraReclaimAndArchiveTask` (`internal/tui/heraactions.go:434`) — the single path behind every Hera nuke (`heraNukeRole`, `heraDoCascadeNuke`, `heraNukeArchivedRole`) — unconditionally runs `agent.RemoveWorktreeAndBranch` / `agent.DeleteBranch` + `agent.DeleteRemoteBranch`, deleting the worktree and BOTH the local and remote git branch, with no check of whether that branch's work ever landed anywhere. A prior historical-sweep investigation (this same workstream) manually classified 737 already-stuck tasks and found the two things that make this hard to get right by hand:

1. **Branch absence is not evidence either way.** Nuke deletes the branch regardless of merge status, so "the branch is gone" tells you nothing — of the 737 backlog tasks, the majority (e.g. 221/271 for ARGUS, 171/278 for Sketch) had already lost their branch to a prior nuke, yet a large fraction of those were later confirmed merged via a squash-merge whose head branch GitHub still remembers by name even after deletion.
2. **Branch names get reused.** The same short branch name (e.g. `argus/2a-db`) can belong to two unrelated tasks created months apart. A name-based GitHub lookup that doesn't guard against this can silently attribute one task's merged PR to a different, unmerged task.

This change builds the classifier that gets both of these right, as a standalone, independently-tested library — no caller yet (see `add-nuke-merge-warning`, `add-hera-cleanup-ui`).

Existing infrastructure this design builds on rather than duplicates:

- `internal/gitutil/pr_batch.go` (`FetchPRStatesBatch`): the daemon's existing PR-status poller already does per-repo-batched, aliased `gh api graphql` lookups by `headRefName`, with a swappable `prGraphQLRunner` test seam, a temp-file query (avoiding argv limits), and rate-limit-cost parsing off `data.rateLimit.cost`. It shells to the `gh` CLI everywhere in this codebase — there is no Go GitHub HTTP client to instead build on.
- `internal/gitutil/gitcmd.go`: has `runGit` (unexported, 5s-timeout `exec.CommandContext` wrapper) and `findMergeBase`, but no exported `IsAncestor`, no default-branch resolver, and no `git fetch` wrapper.
- The daemon-owns-`gh`, TUI-only-reads-cache boundary already established for PR status (`app.go` comment: "*The TUI NEVER invokes gh/gitutil.FetchPRState itself*"). This change's Tier B is a library call, not a standing loop, so it doesn't itself violate that boundary — but it constrains where `add-nuke-merge-warning` and `add-hera-cleanup-ui` are allowed to call it from (see those changes' own designs).
- A documented incident (`context/knowledge/gotchas/daemon-rpc.md`): 125 eligible tasks × 60 polls/hr once burned ~7,500 GraphQL points/hr against a 5,000/hr ceiling. GitHub's GraphQL cost is point-based (~1 point per branch resolved), not request-based — batching reduces round-trips but not points. Any new caller of this classifier's Tier B must be deliberate about call frequency.

## Goals / Non-Goals

**Goals:**

- A pure, synchronously-callable Go function per tier: Tier A takes no network I/O and is safe to call from any goroutine (though per the codebase's UI-thread rule, still never inline on the tview goroutine); Tier B is explicitly network-bound and batchable across many (repo, branch) pairs in one call, mirroring `FetchPRStatesBatch`'s shape.
- Fail closed: the classifier returns "not confirmed" for every case it cannot positively prove, including branch-name reuse ambiguity, an unresolvable repo, and any error. It never guesses toward "safe."
- Testable without a network or a real GitHub repo: Tier A tests build real local git fixtures (temp repos via `os/exec git init` or similar, matching how other `gitutil` tests already fixture git state); Tier B tests swap the `gh` seam exactly like `pr_batch_test.go` already does.

**Non-Goals:**

- No caller. This change does not touch `heraReclaimAndArchiveTask`, any TUI code, or any REST endpoint — that's `add-nuke-merge-warning` and `add-hera-cleanup-ui`.
- No `git fetch` in Tier A. Tier A reads whatever local/remote-tracking refs already exist; it does not fetch first (see Decision below).
- No independent re-verification of a GitHub-reported merge via a local ancestor check on the merge commit SHA. Tier B trusts GitHub's `state`/`baseRefName`/`mergedAt` fields directly, at the same trust level the existing PR-status feature already places in `gh`'s output — no local `git fetch <sha>` + `merge-base --is-ancestor` round-trip. A caller that wants that extra rigor can add it; the classifier doesn't require it.
- No new standing background poll loop, cache table, or `task_meta` namespace. This is a called-on-demand library.

## Decisions

**Decision: two tiers, evaluated in order, each independently sufficient — never blend partial evidence into a probabilistic score.**

A branch is either confirmably safe or it isn't; there is no partial credit. Tier A short-circuits Tier B (if the branch still exists and is a local ancestor, there's no need to touch the network at all). This keeps the common case — a task nuked while its branch still exists locally, the majority of "plain task-archive" cases found in the historical audit — completely free of network calls.

**Decision: Tier B's batched query returns up to N=5 most-recent PRs per head ref name (`first: 5, orderBy: {field: CREATED_AT, direction: DESC}`), not `first: 1` like the existing PR-badge query, and not an unbounded `is:merged` search.**

Alternatives considered:

- *Reuse `FetchPRStatesBatch`'s exact `first: 1` shape.* Rejected: `first: 1` only ever sees the SINGLE most recent PR for that head ref name. If a branch name was reused — task A's (unmerged, abandoned) branch name later reused by task B, which merged — a `first: 1` lookup for task A's branch returns task B's merged PR and would falsely mark task A safe. This is a real, observed pattern in this codebase's branch-naming (duplicate head ref names were found across unrelated tasks in several projects during the historical audit, most heavily in `drn/argus` itself). `first: 1` is fine for the PR-status badge's use case (which only cares about the CURRENT task's still-open PR, not a retrospective merge claim about a since-abandoned one) but not safe for a merge-safety claim.
- *A full `is:merged` search across the repo's entire PR history, indexed by head ref name (the approach the manual historical sweep used).* Rejected as the library's default: correct, but costs one `search(type:ISSUE)` call whose result set scales with the repo's total merged-PR count (hundreds to low thousands), independent of how many branches are actually being checked — a bad fit for a function meant to be called per-nuke or per-batch-of-N-candidates. `first: 5` per head ref name, batched by repo exactly like the existing PR-badge query, bounds the cost to the number of branches actually being asked about.
- *`first: 5` with a plausibility filter.* Chosen. The classifier requires exactly one of the returned candidates to satisfy ALL of: `state == MERGED`, `baseRefName == <project default>`, and `createdAt` no earlier than the task's own `created_at` (with a small slack for clock skew) — the same timing guard the manual sweep validated (it demoted 4 of 247 initially-classified-safe tasks in `drn/argus` once this guard was added). Zero matches or more than one plausible match both classify as "not confirmed" (ambiguous), never "assume the first/best one."

**Decision: extend `pr_batch.go`'s query-execution machinery with a second, sibling query-builder rather than a wholly separate GraphQL client.**

`FetchPRStatesBatch` couples three concerns: (1) per-repo grouping + aliasing, (2) executing one `gh api graphql` call via a temp file with a swappable runner seam, (3) parsing `data.rateLimit.cost` and the aliased `repo` map back into per-branch results. Concerns (2) and (3) are identical for a merge-candidate query; only the field list and node-shape (`state isDraft reviewDecision url` vs. `state baseRefName mergedAt createdAt url`, and `first: 1` vs `first: 5`) differ. This change extracts (2)+(3) into a shared unexported helper (`runAliasedRepoQuery` or similar) that both `FetchPRStatesBatch` (existing, field-shape unchanged) and the new merge-candidate fetch call, rather than either duplicating the temp-file/runner/rate-limit-parsing logic wholesale or bolting merge-candidate fields onto `PRResult` (which is a public type already consumed by the PR-badge feature and shouldn't grow fields irrelevant to it).

**Decision: default-branch resolution prefers the `projects` table's configured `branch` column, falling back to the remote's HEAD when that column is blank.**

The historical audit found `AI-SWOT`'s `projects.branch` is an empty string even though its repo has a perfectly normal `origin/main`. `ResolveDefaultBranch(repoDir, configured string) (short, remoteTrackingRef string, err error)` takes the configured value as a hint; when blank, it resolves `origin/HEAD` (already fetchable without a network round-trip if the remote-tracking `HEAD` ref exists locally, which it does for any repo that's been cloned normally) and falls back further to `gitutil`'s existing `priorityBranches` list (`master`/`main` local branches) only if even that is unavailable. This mirrors `findMergeBase`'s existing fallback chain (`gitcmd.go:53`) rather than inventing a new one.

**Decision: no `git fetch` inside Tier A.**

Fetching before every ancestor check would mean every Tier A call — including ones made synchronously-adjacent to a nuke confirmation in `add-nuke-merge-warning` — does a network round-trip, which is exactly the "no gh/network on the interactive nuke path" property that change needs to preserve. Skipping the fetch means Tier A can occasionally under-report a VERY recently merged branch as "not an ancestor" (stale local remote-tracking ref) — but staleness only ever produces a false "not confirmed," never a false "safe." That's the correct failure direction for a warn-but-allow feature: an occasional unnecessary warning is a tolerable cost; a missed one is not. A caller that wants fresher data (e.g. `add-hera-cleanup-ui`'s batch classification, which is already an explicit, occasional, spinner-backed action) can fetch first itself before calling Tier A — that's the caller's call, not baked into the library.

## Risks / Trade-offs

- **[Risk]** Tier B's `first: 5` bound could still miss the right PR if a branch name was reused more than 5 times. → **Mitigation**: this compounds toward "not confirmed," never toward a false safe — the classifier only accepts a match it can find among the 5, and finding zero or an implausible set among 5 already-recent candidates for an obscure reused name is itself a reasonable trigger for a human to look closer, which is exactly what "needs review" means.
- **[Risk]** A project whose row has been deleted from the `projects` table (e.g. the `Hera` project found in the historical audit — its worktree paths still exist in old task rows, but the project itself is gone) has no `projects.branch` to seed default-branch resolution, and no worktree to resolve a repo directory from if the task is already archived and its worktree reclaimed. → **Mitigation**: `ResolveDefaultBranch`/repo resolution take an explicit `repoDir` parameter rather than looking up `projects` internally — resolving "what repo directory does this task belong to" is the CALLER's problem (a live task has a worktree; the cleanup UI's historical case needs its own resolution strategy, addressed in `add-hera-cleanup-ui`'s design). The classifier itself has no dependency on the `projects` table surviving.
- **[Risk]** `runGit`/`prRunner`-style calls in this codebase don't currently guard branch-name arguments with a `--` separator before passing them to git/gh subprocesses, which is an argument-injection gap (a branch starting with `-` could be parsed as an option) — not exploitable today since branch names are argus-generated, but this classifier is a new subprocess-calling surface. → **Mitigation**: every new subprocess call this change adds (`IsAncestor`, the merge-candidate query's branch arguments) adds a `--` separator before positional branch-name arguments, closing the gap for new code without needing to touch the existing (lower-risk, argus-controlled-input) call sites.

## Migration Plan

Additive only — new package, new `gitutil` functions, no changes to existing call sites or schemas. No rollback concerns beyond reverting the commit; nothing consumes this yet.

## Open Questions

None for this stage. `add-nuke-merge-warning` and `add-hera-cleanup-ui` each carry their own open questions (notably: what "remove" means in the cleanup UI, and exact UI placement) and will be sent for approval separately, after this foundational change.
