# Git Integration

## MODIFIED Requirements

### Requirement: Merge-base resolution with upstream and default-branch fallback

The system SHALL determine the comparison base for branch diffs by first trying the branch's configured upstream, then falling back to remote-tracking default branches (`upstream/master`, `origin/master`, `upstream/main`, `origin/main`, in that order), then finally to local `master`/`main` branches. Both local and remote-tracking branch refs are shared across every worktree of a repository (only `HEAD` is per-worktree), but preferring remote-tracking refs still avoids resolving the merge-base against a local branch ref that another process (e.g. a different worktree or the primary checkout) could arbitrarily rewrite via its own rebase, reset, or amend — remote-tracking refs only change via an explicit fetch, a deliberate sync to real upstream state rather than an arbitrary history rewrite. When none of these resolve (for example, the path is not a git repository, or it has neither a remote nor a local `master`/`main`), the system SHALL yield an empty base and skip branch-diff collection.

#### Scenario: No merge base available

- **WHEN** merge-base resolution runs against a path that is not a git repository
- **THEN** the resolved base is empty

#### Scenario: Falls back to a remote-tracking default branch

- **WHEN** a feature branch has no upstream configured but an `origin/master` remote-tracking branch exists
- **THEN** the resolved merge-base is computed against `origin/master` rather than any local branch

#### Scenario: Falls back to local master when no remote exists

- **WHEN** a feature branch has no upstream and no remote-tracking branches, but a local `master` branch exists
- **THEN** a non-empty merge-base is resolved against local `master`

#### Scenario: Falls back to main when master is absent

- **WHEN** a feature branch has no upstream, no remote-tracking branches, and only a local `main` branch exists
- **THEN** a non-empty merge-base is resolved via the `main` fallback
