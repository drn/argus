## MODIFIED Requirements

### Requirement: Caller identity resolved by id or cwd

Tools invoked by an agent that does not know its own task ID (archive, rename, complete, set-result, clipboard, messaging, artifacts, and the native `hera_*` tools) SHALL resolve the task from an explicit `id` or, failing that, from a `cwd` matched against task worktree paths using longest-prefix-wins with separator guarding so sibling worktrees do not collide. A `worktree_path` is NOT a stable unique key across a task's full lifecycle — argus reuses a worktree directory when a task name/branch is reused after the prior task moved to in_review/complete/archived without its worktree being cleared (BUG-059) — so when two or more tasks tie for the longest matching worktree path (which, since prefixes of one string at a fixed length are identical, means those tasks share the exact same worktree value), resolution SHALL disambiguate rather than silently pick whichever task was listed first: archived matches are dropped; if exactly one non-archived match remains, that task resolves; otherwise, if exactly one non-archived match is `in_progress` (the running session making the call), that task resolves; otherwise the call MUST fail with a tool error naming the candidate tasks rather than guess. When neither an `id` nor a matching `cwd` is provided, the tool MUST return a tool error.

Derived from: `internal/mcp/server.go:1620` (`resolveTask`), `internal/mcp/server.go:1668` (`disambiguateCwdMatches`), `internal/mcp/server.go:1708` (`CwdAmbiguousError`).

#### Scenario: Resolve by exact worktree cwd

- **WHEN** a tool is called with a `cwd` equal to or nested inside a task's worktree
- **THEN** the operation targets that task

#### Scenario: Sibling worktree with shared prefix does not match

- **WHEN** a `cwd` shares a string prefix with a worktree but is not that worktree or a child of it
- **THEN** that task is not selected

#### Scenario: Neither id nor matching cwd

- **WHEN** a tool is called with no `id` and a `cwd` that matches no worktree
- **THEN** the response is a tool error

#### Scenario: A stale task sharing a worktree does not shadow the live task

- **WHEN** a `cwd` matches the worktree of both an archived (or in_review/complete) task and a single `in_progress` task
- **THEN** resolution selects the `in_progress` task, regardless of which task the underlying list returns first

#### Scenario: Two live tasks sharing a worktree is refused, not guessed

- **WHEN** a `cwd` matches the worktree of two or more tasks that are all non-archived and none, or more than one, is uniquely `in_progress`
- **THEN** the response is a tool error naming the candidate task IDs, and no task is silently selected
