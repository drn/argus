## Why

**The file diff view shows files that are not part of the current task's changes, especially right after a rebase.**

`findMergeBase` (`internal/gitutil/gitcmd.go`) resolves the branch-diff comparison base by trying `HEAD@{upstream}`, then falling back to the **local** `master`/`main` branch. Argus-created task branches never get upstream tracking configured (`CreateWorktree` never runs `git branch --set-upstream-to`), so in practice nearly every task's diff falls through to that local-branch fallback.

Git worktrees share one repository's refs — `refs/heads/master` is the exact same ref in the primary checkout and in every task worktree; only `HEAD` is per-worktree. If any process rewrites that shared local `master` (e.g. the user's primary checkout does its own `git pull --rebase` or interactive rebase, unrelated to any task), the ref now points at all-new commit objects. Every task worktree's `git merge-base HEAD master` is recomputed fresh on the next 3-second refresh tick (there is no caching), and since the pre-rebase history is no longer reachable from the new `master`, merge-base silently resolves to a much older common ancestor. The subsequent `git diff <base>..HEAD` then includes every file changed between that stale ancestor and the task's HEAD — files the task never touched.

Remote-tracking refs (`origin/master`, `origin/main`) are shared across worktrees too — they are not worktree-isolated any more than local branches are. But they only ever move via an explicit `git fetch`, a deliberate sync to the real upstream state (normally fast-forward), rather than an arbitrary history rewrite (rebase, reset, amend) that some other process might perform on a checked-out local branch. That makes them a safer fallback, not an immune one.

## What Changes

- **`findMergeBase` prefers remote-tracking branches over local ones in its fallback chain.** After `HEAD@{upstream}` fails, it now tries `upstream/master`, `origin/master`, `upstream/main`, `origin/main` (the same priority order already used by `ListRemoteBranches`) before falling back to the local `master`/`main` branches. This avoids diffing against a ref that can be arbitrarily rewritten out from under the worktree by an unrelated process, while preserving the local fallback for repositories with no remote configured (e.g. local-only projects, and the existing no-remote test fixtures).

## Capabilities

### Modified Capabilities

- `git-integration`: merge-base resolution now tries remote-tracking branches (`upstream/master`, `origin/master`, `upstream/main`, `origin/main`) before falling back to local `master`/`main`, so the branch diff is no longer vulnerable to another process arbitrarily rewriting the shared local branch ref.

## Impact

- **Modified code:** `internal/gitutil/gitcmd.go` (`findMergeBase`).
- **No new key, no new dependency, no schema change, no daemon RPC.** Pure read-only diff computation — behavior only changes which ref is chosen as the comparison base.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build wiring is added or changed. The quality gate stays `make pre-pr`.
