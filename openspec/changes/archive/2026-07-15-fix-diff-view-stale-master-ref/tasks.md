## 1. Fix merge-base fallback order

- [x] 1.1 In `internal/gitutil/gitcmd.go`, `findMergeBase`: after `HEAD@{upstream}`, try `priorityBranches` (`upstream/master`, `origin/master`, `upstream/main`, `origin/main`) before falling back to local `master`/`main`.

## 2. Tests

- [x] 2.1 Add a test: a feature branch with no upstream but an `origin/master` remote-tracking ref resolves its merge-base against `origin/master`, not a diverged local `master`.
- [x] 2.2 Existing no-remote fallback tests (`falls back to master branch`, `falls back to main branch when master absent`) still pass unchanged.

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/worktree.md` documenting that local branch refs are shared across all worktrees of a repo, so diff-base resolution must prefer remote-tracking refs.

## 4. Archive

- [x] 4.1 Run `openspec archive fix-diff-view-stale-master-ref` (or apply by hand) to fold this delta into `openspec/specs/git-integration/spec.md` and move this change folder to `openspec/changes/archive/`, committed on this branch before opening the PR.
