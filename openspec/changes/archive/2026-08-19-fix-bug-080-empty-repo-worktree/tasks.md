## 1. Detect and handle an unborn repo in CreateWorktree

- [x] 1.1 Add an unborn-`HEAD` check (`git rev-parse --verify --quiet HEAD`
  failing) in `internal/agent/worktree.go`, computed once near the top of
  `CreateWorktree`, before the remote-fetch and `resolveStartPoint` calls
  (BUG-080).
- [x] 1.2 Skip the remote fetch and `resolveStartPoint` call when the repo is
  unborn — there is no ref to fetch or resolve against.
- [x] 1.3 When unborn, build the `git worktree add` command as `git worktree
  add --orphan -b <branch> <dir>` (no start-point argument) instead of `git
  worktree add -b <branch> <dir> <baseBranch>`.
- [x] 1.4 Leave the post-checkout-hook-failure guard and the
  attach-to-existing-branch fallback unchanged; both already omit a
  start-point and continue to work once the orphan branch exists.
- [x] 1.5 Scope the unborn check to the no-explicit-base-branch case
  (`baseBranch == "HEAD"`, the post-default value) — found in review — so an
  explicit base branch (e.g. a sibling task's already-committed branch, used
  for git-stacking) is still resolved and honored normally even while the
  project's own checkout remains unborn, instead of being silently forced
  through the orphan path.

## 2. Tests

- [x] 2.1 `CreateWorktree` on a repo with `git init` only (zero commits, no
  config beyond `user.email`/`user.name`) succeeds, producing a `.git` entry
  in the worktree and a `git rev-parse --verify --quiet argus/<task>`
  failure (branch exists but has no commits — matches the source repo's own
  state before this first commit).
- [x] 2.2 A non-empty repo (existing tests) is unaffected — no behavior change
  when `HEAD` already resolves.
- [x] 2.3 An explicit base branch that resolves to a valid, already-committed
  branch is honored (not forced to orphan) even while the project's own
  checkout remains unborn (`TestCreateWorktree_StackedOnUnbornProjectHEAD`).

## 3. Docs

- [x] 3.1 Add a gotcha bullet to `context/knowledge/gotchas/worktree.md`
  documenting the unborn-`HEAD` case, the `--orphan` fix, and the git ≥ 2.42
  requirement it implies for that path (BUG-080).
