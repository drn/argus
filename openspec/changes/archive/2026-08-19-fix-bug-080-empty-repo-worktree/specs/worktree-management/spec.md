## MODIFIED Requirements

### Requirement: Worktree creation with argus branch

The system SHALL create a git worktree at the deterministic path on a new branch named `argus/<task-name>`, basing it on the requested start point (defaulting to `HEAD` when no base branch is given). On success it SHALL return the worktree path, the final task name, and the branch name.

When no explicit base branch is given and the repository's checked-out `HEAD` is unborn (the repository has zero commits, so `HEAD` resolves to nothing), the system SHALL create the branch as an orphan (no start point) instead of attempting to base it on `HEAD`. This SHALL NOT apply when an explicit base branch is given: the system SHALL still resolve and honor an explicit base branch normally — including a branch that only exists because a different task already committed to it — even while the repository's own checked-out `HEAD` remains unborn.

#### Scenario: Fresh worktree created on a new branch

- **WHEN** a worktree is created for a task named "fix-bug" with no explicit base branch
- **THEN** the returned branch name is `argus/fix-bug`, the returned final name is "fix-bug", a `.git` entry exists inside the worktree directory, and the `argus/fix-bug` branch exists in the repository

#### Scenario: Worktree based on a custom start point

- **WHEN** a worktree is created with an explicit base branch that resolves to a valid commit
- **THEN** the worktree HEAD matches the commit of that base branch

#### Scenario: Worktree created in a repo with zero commits

- **WHEN** a worktree is requested with no explicit base branch, for a project whose repository has just been `git init`'d and has no commits (an unborn `HEAD`)
- **THEN** the worktree is created successfully as an orphan branch, a `.git` entry exists inside the worktree directory, and the `argus/<task-name>` branch has no commits yet

#### Scenario: Explicit base branch honored despite the project's own unborn HEAD

- **WHEN** a worktree is requested with an explicit base branch that resolves to a valid commit (e.g. a sibling task's already-committed `argus/<task>` branch), even though the project's own checked-out `HEAD` is unborn
- **THEN** the worktree is based on that resolved branch, not created as an orphan — its `HEAD` matches the commit of the explicit base branch
