## Why

A live audit found 737 tasks across every project permanently stuck at `status=in_review, archived=1` with no Hera binding — invisible to `PruneCompleted` (`fix-hera-archive-status` stops new ones going forward, but doesn't touch the historical backlog). Investigating a one-time cleanup sweep for that backlog surfaced the real root cause: `heraReclaimAndArchiveTask` (the shared code path behind every Hera nuke — Ctrl+D on a role, Ctrl+D cascading a subtree, `C` clearing a hidden archive) deletes a task's worktree AND both its local and remote git branch unconditionally, with zero visibility into whether that branch's work was ever actually merged. Nuking with no merge-status awareness is exactly how tasks end up stuck/orphaned in the first place, and it will keep happening regardless of any one-time historical sweep.

Per direction: the fix must be a permanent, tested, code-level capability — not an LLM computing an answer once at request time. This change introduces the shared classifier only; consuming it at nuke time and in a new cleanup UI are separate follow-on changes (`add-nuke-merge-warning`, `add-hera-cleanup-ui`) that both depend on this one landing first.

## What Changes

- New `internal/mergesafety` package: given a task's repo directory, branch, and the project's default branch, determines whether that branch's work is confirmed to have landed in the default branch. Two tiers of evidence, most-trusted first:
  - **Tier A (local, no network):** the branch ref still resolves (locally or via a remote-tracking ref) and `git merge-base --is-ancestor <branch> <default>` succeeds.
  - **Tier B (network, batched GraphQL via `gh`):** the branch is gone, but a merged PR with that exact head ref name is found whose base ref matches the project's default branch — covering the common case where a squash-merge deletes the head branch.
  - Anything else (branch gone with no matching merged PR, an ambiguous/reused branch name, an unresolvable repo) classifies as NOT confirmed-safe. The classifier fails closed by design — it never claims "safe" without direct evidence.
- New `internal/gitutil` primitives the classifier depends on: `IsAncestor`, `ResolveDefaultBranch` (project-configured branch first, falling back to the remote's HEAD), and a merge-candidate batched GraphQL lookup that extends the existing per-repo-batched, aliased `gh api graphql` machinery already used for PR-status polling (`internal/gitutil/pr_batch.go`) rather than introducing a second, parallel GitHub API client.
- No UI changes and no behavior changes in this stage — the classifier is a standalone, independently tested library with no callers yet.

## Capabilities

### New Capabilities

- `merge-safety`: defines the classifier's evidence tiers, its fail-closed default, and the batched/rate-limit-conscious contract for the network tier.

### Modified Capabilities

(none — this stage adds no caller-visible behavior; `hera-view` and `pr-status` are consumed as read-only reference points but neither's requirements change here)

## Impact

- New package `internal/mergesafety` (classifier + types), fully unit tested (local git fixtures for Tier A, a swappable test seam for the `gh` call in Tier B, mirroring the existing `prGraphQLRunner` seam).
- `internal/gitutil`: adds `IsAncestor`, `ResolveDefaultBranch`, and a new batched merge-candidate GraphQL query, extracted alongside `pr_batch.go`'s existing query-execution machinery (temp-file `gh api graphql` invocation, per-repo aliasing, rate-limit-cost parsing) so the two batched query shapes (PR-badge state vs. merge-candidate) share the same execution primitive instead of duplicating it.
- No schema change, no new REST endpoint, no new MCP tool, no new TUI surface. This change is inert until `add-nuke-merge-warning` and `add-hera-cleanup-ui` call it.
- Must respect the documented GitHub GraphQL budget (~5,000 points/hour shared ceiling; a past incident already exhausted it) — the classifier's network tier is caller-invoked on demand, not a new standing poll loop, so it adds no steady-state background cost by itself; each consumer change is responsible for its own call frequency.
