# Worktree Management

## Purpose

Argus isolates every task in its own git worktree and branch so concurrent agent sessions never collide in a shared working tree. This capability owns the deterministic worktree path layout, branch naming, name-conflict resolution, transactional task creation (with full rollback on any failure), and the removal/pruning of worktrees and branches when tasks finish or go orphaned.
## Requirements
### Requirement: Deterministic worktree path layout

The system SHALL place each task's worktree at a deterministic path under the Argus data directory, namespaced by project and task name, so the same project/task pair always resolves to the same location.

#### Scenario: Path is composed from project and task name

- **WHEN** a worktree path is requested for project "myproject" and task "fix-bug"
- **THEN** the path is `<data-dir>/worktrees/myproject/fix-bug`, ending in `<project>/<task>` under the `.argus/worktrees` directory

### Requirement: Worktree creation with argus branch

The system SHALL create a git worktree at the deterministic path on a new branch named `argus/<task-name>`, basing it on the requested start point (defaulting to `HEAD` when no base branch is given). On success it SHALL return the worktree path, the final task name, and the branch name.

#### Scenario: Fresh worktree created on a new branch

- **WHEN** a worktree is created for a task named "fix-bug" with no explicit base branch
- **THEN** the returned branch name is `argus/fix-bug`, the returned final name is "fix-bug", a `.git` entry exists inside the worktree directory, and the `argus/fix-bug` branch exists in the repository

#### Scenario: Worktree based on a custom start point

- **WHEN** a worktree is created with an explicit base branch that resolves to a valid commit
- **THEN** the worktree HEAD matches the commit of that base branch

### Requirement: Base branch resolution against remotes

When the requested base branch is not `HEAD`, the system SHALL fetch remotes before resolving the start point, and SHALL fall back to remote-tracking branches (`origin/<ref>` or `upstream/<ref>`) when no matching local ref exists, so a worktree can be based on a branch the local repo has not yet seen.

#### Scenario: Local branch missing, remote-tracking branch used

- **WHEN** a worktree is requested on a base branch that exists only as `origin/<branch>` after the local default branch was deleted
- **THEN** the worktree is created successfully based on the remote-tracking branch

#### Scenario: Unfetched remote branch is fetched first

- **WHEN** a worktree is requested on a branch that was pushed to the remote after the local clone and is not yet known locally
- **THEN** the system fetches the remote and creates the worktree whose HEAD matches the upstream branch's commit

### Requirement: Task name sanitization

The system SHALL sanitize task names before using them as branch names or directory components, replacing characters that are invalid in git refs or hostile to shell navigation (including slashes, control characters, quotes, and shell metacharacters) with hyphens, collapsing repeated hyphens, trimming leading/trailing hyphens and dots, and capping length. A name that sanitizes to empty SHALL fall back to "task".

#### Scenario: Invalid and shell-hostile characters replaced

- **WHEN** a task name contains characters such as spaces, `?`, `~`, `:`, brackets, quotes, or shell metacharacters
- **THEN** those characters are replaced with single hyphens (e.g. "hello world" becomes "hello-world", "a:b:c" becomes "a-b-c")

#### Scenario: Slashes are stripped

- **WHEN** a task name contains `/` (e.g. "a/b/c")
- **THEN** the slashes are replaced with hyphens so the safe name introduces no extra directory depth (e.g. "a-b-c")

#### Scenario: Entirely-invalid name falls back

- **WHEN** a task name is empty or consists only of invalid characters (e.g. "???")
- **THEN** the sanitized name is "task"

#### Scenario: Overlong name is truncated

- **WHEN** a sanitized task name exceeds the length cap
- **THEN** it is truncated, preferring a hyphen boundary, with no trailing hyphen or dot

### Requirement: Name-conflict suffixing

When the deterministic worktree path already exists for a different worktree, the system SHALL append a numeric suffix (`-1`, `-2`, …) to the task name and branch until a free slot is found, returning the suffixed final name. If no free slot is found after exhausting the suffix range, it SHALL return an error.

#### Scenario: Duplicate name gets a numeric suffix

- **WHEN** a worktree is created for "fix-bug" when a worktree at that path already exists
- **THEN** the returned final name is "fix-bug-1" and the branch is `argus/fix-bug-1`

### Requirement: Resilient worktree creation

The system SHALL tolerate stale worktree references and partial git failures during creation: it SHALL prune stale worktree references before creating; SHALL treat a non-zero git exit as success when a valid worktree directory was nonetheless produced (e.g. a failing post-checkout hook); and SHALL retry attaching to an already-existing branch when the initial branch-creating add fails.

#### Scenario: Stale reference is pruned

- **WHEN** a previous worktree directory was deleted without `git worktree remove`, leaving the branch locked to a stale entry, and a new worktree is requested at the same name
- **THEN** the stale reference is pruned and the worktree is created successfully

#### Scenario: Post-checkout hook fails but worktree is valid

- **WHEN** `git worktree add` exits non-zero due to a failing post-checkout hook but a valid worktree (with a `.git` entry) was created
- **THEN** creation is treated as successful and returns the worktree path, final name, and branch

#### Scenario: Branch already exists

- **WHEN** the `argus/<name>` branch already exists but no worktree occupies the path
- **THEN** the system attaches a worktree to the existing branch and succeeds

### Requirement: Transactional task creation with LIFO rollback

The system SHALL create and start a task as a single transaction: resolve project config, create the worktree, write attachments, run an optional post-worktree hook, persist the task row, reserve a session ID, and start the agent session. Each side-effecting step SHALL register a compensating cleanup that runs in last-in-first-out order if any later step fails, leaving no orphan worktree, branch, or database row. On success the task SHALL have status InProgress, a recorded start timestamp, and the agent PID populated.

#### Scenario: Successful creation starts the session

- **WHEN** a task is created for a configured project and the session starts successfully
- **THEN** the returned task has status InProgress, a populated agent PID, an absolute worktree path, branch `argus/<name>`, a matching persisted DB row, and a worktree on disk

#### Scenario: Session-start failure unwinds everything

- **WHEN** task creation reaches session start but `runner.Start` returns an error
- **THEN** the call returns an error with a nil task and session, no DB row remains, the worktree directory is removed, and the `argus/<name>` branch is deleted

#### Scenario: Post-worktree hook failure unwinds the worktree

- **WHEN** the optional post-worktree hook returns an error
- **THEN** the session is never started, no DB row remains, and the worktree is removed

#### Scenario: Missing project rejected before side effects

- **WHEN** task creation references a project not present in config, or a project with no configured path
- **THEN** the call returns an error before any worktree, DB, or session side effect occurs

### Requirement: Attachment handling during creation

The system SHALL write user-supplied attachments into the `.context` directory inside the worktree before the session starts, de-duplicating same-named files within a batch by suffixing, rejecting path-traversal names, and appending the resulting worktree-relative paths to the task prompt. When the prompt is otherwise empty it SHALL still produce an "Attached files:" listing.

#### Scenario: Attachments written and listed in prompt

- **WHEN** a task is created with attachments
- **THEN** each attachment is written under `<worktree>/.context/`, the user's prompt text is preserved, and each attachment's `./.context/<name>` path is appended to the prompt

#### Scenario: Duplicate names de-duplicated within a batch

- **WHEN** two attachments share the same name in one batch
- **THEN** the second is written with a numeric suffix (e.g. "shot.png" and "shot-1.png"), both files persist with their distinct contents, and both paths appear in the prompt

#### Scenario: Path-traversal name rejected

- **WHEN** an attachment name attempts path traversal (e.g. "..")
- **THEN** task creation returns an error

### Requirement: Session ID reservation by backend type

The system SHALL reserve and persist a generated session ID for backends that support session pinning (Claude-style), and SHALL skip reservation for backends whose IDs are captured after the process exits (Codex, pi).

#### Scenario: Session ID generated for Claude-style backend

- **WHEN** a task is created with a Claude-style backend
- **THEN** the task is assigned a generated session ID that is also persisted to its DB row

### Requirement: Worktree and branch removal

The system SHALL remove a task's worktree (forcing removal and deleting any leftover directory contents) and delete both its local and remote branch on a best-effort basis, never failing the caller. It SHALL only act on paths recognized as worktree subdirectories, skip cleanly when the path is missing or not a worktree, and skip branch deletion when no branch is supplied. When the stored branch is not an `argus/*` branch, it SHALL infer the worktree branch from the directory name.

#### Scenario: Worktree and branch removed

- **WHEN** removal is requested for an existing worktree and its `argus/<name>` branch
- **THEN** the worktree directory is removed and the branch is deleted locally and on the remote

#### Scenario: Leftover untracked files cleaned

- **WHEN** removal targets a worktree containing untracked files that `git worktree remove` leaves behind
- **THEN** the directory is fully removed including the leftover files

#### Scenario: Branch inferred when stored branch is a base branch

- **WHEN** the stored branch is a base branch such as "origin/master" rather than an `argus/*` branch
- **THEN** the system infers `argus/<dir-name>` from the worktree directory and deletes that branch

#### Scenario: Empty branch skips branch cleanup

- **WHEN** removal is requested with an empty branch name
- **THEN** the worktree is removed but no branch is deleted

#### Scenario: Non-worktree or missing path is a no-op

- **WHEN** removal targets a path that is not a recognized worktree subdirectory, or a path that does not exist
- **THEN** nothing is removed and the path is left intact

### Requirement: Orphaned worktree sweeping

The system SHALL identify worktree directories under the worktree root that are not tracked in the database, count them, and optionally remove them along with their inferred `argus/<task>` branches. It SHALL skip any directory that is an ancestor of a tracked worktree path so it never destroys a live worktree nested deeper than the standard `<project>/<task>` layout, and SHALL remove emptied project directories after sweeping.

#### Scenario: Untracked worktrees counted and swept

- **WHEN** a sweep runs over a worktree root containing directories not present in the known-paths set
- **THEN** those directories are counted as orphans and, when a projects map is supplied, removed along with their branches, and emptied project directories are removed

#### Scenario: Ancestor of a tracked worktree is preserved

- **WHEN** an orphan candidate directory is an ancestor of a tracked (known) worktree path
- **THEN** it is not counted as an orphan and is not removed, leaving the live worktree intact

### Requirement: Pruning completed tasks

The system SHALL prune completed tasks in two phases: a synchronous phase
that removes completed rows from the database, stops their sessions, removes
their session logs, and computes the remaining worktree and orphan cleanup
counts; and a slow phase that removes each task's worktree and branch in
parallel and runs the orphan sweep. The slow phase SHALL execute at most once
per plan; subsequent invocations SHALL be no-ops. A completed task that still
holds a live Hera role binding (a `hera_bindings` row with `ended_at IS NULL`)
SHALL be excluded from both phases — `hera_bindings` holds no foreign key to
`tasks`, so deleting such a task's row would leave its Hera role pointing at a
task that no longer exists instead of properly ending it. The system SHALL
report the number of completed tasks skipped for this reason.

#### Scenario: Completed tasks pruned, active tasks retained

- **WHEN** a prune runs with one completed task (with a worktree on disk), one active task, and one orphan directory under the worktree root
- **THEN** only the completed task is removed from the database, its worktree and the orphan directory are both removed, and the active task and its row remain

#### Scenario: Nothing to prune

- **WHEN** a prune runs with no completed tasks
- **THEN** the plan reports zero pruned tasks and zero worktree cleanup work and performs no removal

#### Scenario: Slow phase runs at most once

- **WHEN** the slow prune phase is invoked a second time on the same plan
- **THEN** the second invocation is a no-op and fires no progress callbacks

#### Scenario: Completed task with a live Hera binding is skipped

- **WHEN** a prune runs with a completed task that still has a live (`ended_at IS NULL`) Hera role binding
- **THEN** that task's row, worktree, and branch are NOT removed, and it is counted separately as skipped rather than pruned

#### Scenario: Completed task with only an ended Hera binding is pruned normally

- **WHEN** a prune runs with a completed task whose Hera binding(s) all have `ended_at` set
- **THEN** the task is pruned exactly as a never-bound completed task would be

### Requirement: Test-environment write guard

The system SHALL refuse worktree removal operations that target the real Argus data directory while running inside a test binary, while still permitting operations on paths under the OS temporary directory so tests using a redirected HOME can clean up their synthetic data dirs.

#### Scenario: Real data dir blocked during tests

- **WHEN** a removal targets a path under the real `~/.argus/` directory during a test run
- **THEN** the operation is blocked and no removal occurs

#### Scenario: Temp-dir path allowed during tests

- **WHEN** a removal targets a path under the OS temporary directory during a test run
- **THEN** the operation is permitted

