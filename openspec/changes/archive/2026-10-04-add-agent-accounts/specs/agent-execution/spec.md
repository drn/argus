## ADDED Requirements

### Requirement: Account resolved once at task creation and immutable

The system SHALL record an `account` on each new task, resolved at the single creation call site (`CreateAndStart`) as: explicit selection, else the project's `project_accounts` entry, else `default_account`, else `default`. An explicit `"default"` SHALL be honored as `default` and never re-resolved through the project or global default; an empty value SHALL mean no selection was made. `CreateAndStart` SHALL resolve the backend before creating the worktree. Creating a task with an unknown account name, or with an explicitly selected account that does not support the resolved backend (`Config.AccountSupports`), SHALL be rejected before any side effect. A project/global default that does not support the resolved backend SHALL fall back to `default` and be logged under `[account]`; an inherited account (hera, MCP `task_create`) that does not support it SHALL be rejected. Hera workers, sub-coordinators and freelancers SHALL inherit the spawning coordinator's account, a coordinator on `default` pinning `default`. Fork-created tasks SHALL keep the source task's account. The stored value SHALL never be re-resolved on resume or restart.

#### Scenario: Changing the global default does not move existing tasks
- **WHEN** `default_account` changes after a task exists
- **THEN** the task still spawns and resumes under its stored account

#### Scenario: Worker inherits coordinator account
- **WHEN** a coordinator on account `personal` spawns a hera worker
- **THEN** the worker task's `account` is `personal`

#### Scenario: Default-account coordinator pins default
- **WHEN** a coordinator on `default` spawns a worker in a project whose `project_accounts` entry is `work`
- **THEN** the worker runs on `default`

#### Scenario: Explicit default is not re-resolved
- **WHEN** a task is created with `account: "default"` in a project whose default is `work`
- **THEN** the task runs on `default`

#### Scenario: Explicit account lacking the backend's tool
- **WHEN** a task is created on a codex backend with an explicitly selected account that defines only `claude_config_dir`
- **THEN** creation fails with an error naming the account and backend, and no worktree or row is left behind

#### Scenario: Inherited account lacking the backend's tool
- **WHEN** a coordinator on a Claude-only account spawns a Codex worker
- **THEN** the worker is created on `default` and the fallback is logged

### Requirement: Spawn exports the task account's directory

`BuildCmd` SHALL export `CLAUDE_CONFIG_DIR=<account claude_config_dir>` for Claude backends when the task's account is not `default`, and SHALL NOT export it for any other backend. For Codex backends on a non-default account it SHALL export `CODEX_HOME` (the account's Argus overlay home, or the account home itself if the overlay could not be built) and `CODEX_SQLITE_HOME=<account codex_home>`. On first use the account's `codex_home` SHALL be created with mode 0700 and nothing copied into it. If the stored account no longer exists in configuration, or no longer defines the directory the backend needs, the spawn SHALL fail with an error naming the account rather than falling back to `default`. A session started through the session-supervisor SHALL spawn under the same account and per-task sandbox override as an in-process spawn.

#### Scenario: Removed account fails loudly
- **WHEN** a task's stored account was deleted from config and the task is resumed
- **THEN** the spawn errors with the account name and no session starts under another account

#### Scenario: Non-Claude backend
- **WHEN** a codex task has a non-default account recorded
- **THEN** no `CLAUDE_CONFIG_DIR` is exported, and `CODEX_HOME` / `CODEX_SQLITE_HOME` point at that account

#### Scenario: Supervisor spawn carries the account
- **WHEN** the supervisor starts, kicks or recycles a session for a task on account `work`
- **THEN** the child environment carries `work`'s `CLAUDE_CONFIG_DIR`

### Requirement: Spawn environment strips inherited auth overrides

The spawned child's base environment SHALL always drop an inherited `CLAUDE_CONFIG_DIR`. For a Claude task on an explicit account it SHALL also drop `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN` and `CLAUDE_CODE_OAUTH_TOKEN`; for a Codex task on an explicit account it SHALL also drop `OPENAI_API_KEY` and `CODEX_API_KEY`. Backend `env_vars` mappings and the op bootstrap SHALL be applied after this filter, so an explicit mapping still wins. Only variable names SHALL be logged, never values.

#### Scenario: Inherited API key does not override the account login
- **WHEN** the daemon's environment has `ANTHROPIC_API_KEY` set and a Claude task runs on account `work`
- **THEN** the child environment has no `ANTHROPIC_API_KEY`

### Requirement: Session discovery, resume and reaping use the task's account

Claude session listing and resume-time session-ID recapture SHALL read transcripts from the task account's config dir. Codex session-ID capture SHALL read the task account's `state_5.sqlite`. The background-session reaper SHALL run `claude agents` / `claude stop` under the `CLAUDE_CONFIG_DIR` the session was spawned with. When the stored account is unknown, recapture and reaping SHALL be skipped with a log and the stored session ID left unchanged.

#### Scenario: Resume recapture on a named account
- **WHEN** a task on account `work` is resumed and its transcript exists only under `work`'s `projects/` dir
- **THEN** the session ID is recaptured from that dir
