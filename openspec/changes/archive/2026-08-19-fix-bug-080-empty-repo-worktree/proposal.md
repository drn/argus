## Why

**BUG-080 — creating a task in a brand-new, zero-commit repo fails.**

A repo that has been `git init`'d but never committed to (an "unborn" `HEAD` —
e.g. `recovery-ai-dashboard` right after `git init`) cannot have its first task
created. `CreateWorktree` (`internal/agent/worktree.go`) defaults an unset base
branch to the literal string `"HEAD"`, and `resolveStartPoint` short-circuits
with "HEAD is always valid" — but on an unborn branch, `HEAD` is not a
resolvable ref. The resulting `git worktree add -b argus/<task> <dir> HEAD`
fails with `fatal: invalid reference: HEAD` (exit 128), and the existing
attach-to-existing-branch fallback fails identically since the branch was
never created either. Task creation errors out for every task in that
project, including the first one — the project itself registers fine (adding a
project never runs a git command), so the failure is silent until someone
actually tries to use it.

## What Changes

- **`CreateWorktree` detects an unborn `HEAD`** (`git rev-parse --verify --quiet
  HEAD` failing) before resolving the start point, and in that case creates the
  worktree with `git worktree add --orphan -b <branch> <dir>` (no start-point
  argument) instead of basing it on `HEAD` or any other ref. This mirrors what a
  plain `git init` + first commit would look like, just staged inside the
  worktree instead of the origin project directory.
- **Remote fetch and start-point resolution are skipped** when the repo is
  unborn — there is nothing to fetch or resolve against yet.
- The existing hook-failure and existing-branch fallbacks are unchanged; they
  already omit a start-point and keep working once the initial `--orphan`
  branch exists.
- Non-empty repos (the common case) are completely unaffected — the unborn
  check is a cheap `git rev-parse` that returns immediately for any repo with
  at least one commit.

## Capabilities

### Modified Capabilities

- `worktree-management`: worktree creation now succeeds for a project whose
  repo has zero commits (an unborn `HEAD`), by creating the new branch as an
  orphan instead of basing it on a nonexistent start point.

## Impact

- **Modified code:**
  - `internal/agent/worktree.go` — `CreateWorktree` gains an unborn-repo check
    that skips fetch/`resolveStartPoint` and switches `git worktree add` to
    `--orphan` mode.
- **No new config, no schema change, no daemon RPC, no new key.** Pure fix to
  an existing worktree-creation code path.
- **Requires git ≥ 2.42** (`--orphan` support on `git worktree add`) for the
  empty-repo path specifically; every other path is untouched and has no such
  requirement. Per this repo's breaking-changes policy (single author, no
  back-compat shims), an older git simply surfaces its own "unknown option"
  error rather than silently degrading.
- **Specs are LOCAL DOCS only** (`openspec/project.md`): no CI / Make / Go-build
  wiring is added or changed. The quality gate stays `make pre-pr`.
