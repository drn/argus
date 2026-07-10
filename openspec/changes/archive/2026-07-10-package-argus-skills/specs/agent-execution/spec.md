## MODIFIED Requirements

### Requirement: Command construction with prompt and worktree isolation

The system SHALL build the agent command as a shell invocation whose working directory is the task's worktree. The prompt, when present, SHALL be passed using the backend's configured prompt flag, or — when no prompt flag is configured — as a positional argument guarded by a `--` end-of-options separator so prompts beginning with `-` are not parsed as flags. The system SHALL refuse to build a command for a task with no worktree, and SHALL refuse when the worktree directory does not exist or is unreachable, returning an actionable error and a nil command.

For Claude backends, command construction SHALL make argus's embedded builtin skills available to the session by ensuring they are materialized (via `EnsureBuiltinSkills`) and appending `--add-dir <skills-root>`, where `<skills-root>` is the managed `~/.argus/skills` workspace directory. This flag SHALL be appended only for Claude backends (the flag is Claude-only) and SHALL be omitted when materialization fails or returns no root, so a materialization failure never blocks task start. The flag SHALL be injected before any resume/session-id/prompt suffix, and SHALL be additive to any `--add-dir` already present in the backend command template.

#### Scenario: Prompt passed via configured prompt flag
- **WHEN** the backend defines a prompt flag and the task has a prompt
- **THEN** the command appends the prompt flag followed by the safely quoted prompt

#### Scenario: Prompt passed positionally with separator when no flag
- **WHEN** the backend defines no prompt flag and the task has a prompt
- **THEN** the command appends `--` followed by the safely quoted prompt

#### Scenario: Missing worktree is rejected
- **WHEN** a task has no worktree set
- **THEN** command construction fails with an error stating no worktree is set

#### Scenario: Nonexistent worktree directory is rejected pre-launch
- **WHEN** the task's worktree path does not exist
- **THEN** command construction fails with an error naming the missing path, and returns a nil command and nil cleanup

#### Scenario: Working directory and task environment are set
- **WHEN** a command is built for a task with an ID
- **THEN** the command's working directory is the worktree and the environment exports the task ID for sub-agent tooling

#### Scenario: Claude backend receives the builtin-skills add-dir flag
- **WHEN** a command is built for a Claude backend and builtin skills materialize successfully
- **THEN** the command includes `--add-dir` pointing at the `~/.argus/skills` workspace root, positioned before any resume/session-id/prompt suffix

#### Scenario: Non-Claude backend does not receive the add-dir flag
- **WHEN** a command is built for a codex, pi, or opencode backend
- **THEN** the command contains no argus builtin-skills `--add-dir` flag

#### Scenario: Materialization failure does not block launch
- **WHEN** a command is built for a Claude backend and builtin-skills materialization fails
- **THEN** the command omits the builtin-skills `--add-dir` flag and is still built successfully
