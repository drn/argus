## RENAMED Requirements

- FROM: `### Requirement: Account resolved once at task creation and immutable`
- TO: `### Requirement: Account resolved once at task creation and changed only by an explicit move`

## MODIFIED Requirements

### Requirement: Account resolved once at task creation and changed only by an explicit move

The system SHALL record an `account` on each new task, resolved at the single creation call site (`CreateAndStart`) as: explicit selection, else the project's `project_accounts` entry, else `default_account`, else `default`. When that resolves to `auto`, the system SHALL apply the auto account selection rule, store the chosen concrete account, and mark the task auto-routed; a task resolved any other way SHALL NOT be marked auto-routed. When the account resolves to `auto`, tiered backend routing SHALL read Claude usage for the account auto selection would choose for a Claude backend. An explicit `"default"` SHALL be honored as `default` and never re-resolved through the project or global default; an empty value SHALL mean no selection was made. `CreateAndStart` SHALL resolve the backend before creating the worktree. Creating a task with an unknown account name, or with an explicitly selected account that does not support the resolved backend (`Config.AccountSupports`), SHALL be rejected before any side effect. A project/global default that does not support the resolved backend SHALL fall back to `default` and be logged under `[account]`; an inherited account (hera, MCP `task_create`) that does not support it SHALL be rejected. Hera workers, sub-coordinators and freelancers SHALL inherit the spawning coordinator's account, a coordinator on `default` pinning `default`, except that a coordinator whose task is auto-routed SHALL pass `auto`, so the new task is auto-selected and auto-routed too. A spawn whose coordinator account cannot be read SHALL fail rather than resolve the project default. Fork-created tasks SHALL keep the source task's account and auto-routed flag. The stored value SHALL never be re-resolved on resume or restart; it SHALL change only through the account-move operation.

#### Scenario: Changing the global default does not move existing tasks
- **WHEN** `default_account` changes after a task exists
- **THEN** the task still spawns and resumes under its stored account

#### Scenario: Worker inherits coordinator account
- **WHEN** a coordinator on account `personal` spawns a hera worker
- **THEN** the worker task's `account` is `personal`

#### Scenario: Default-account coordinator pins default
- **WHEN** a coordinator on `default` spawns a worker in a project whose `project_accounts` entry is `work`
- **THEN** the worker runs on `default`

#### Scenario: Auto-routed coordinator's worker is auto-routed
- **WHEN** an auto-routed coordinator on `default` spawns a worker while `default` is over its 5-hour threshold and `personal` has headroom
- **THEN** the worker runs on `personal` and is marked auto-routed

#### Scenario: Project pin beats auto
- **WHEN** `default_account` is `auto` and the task's project maps to `personal` in `project_accounts`
- **THEN** the task runs on `personal` and is not marked auto-routed

#### Scenario: Explicit default is not re-resolved
- **WHEN** a task is created with `account: "default"` in a project whose default is `work`
- **THEN** the task runs on `default`

#### Scenario: Explicit account lacking the backend's tool
- **WHEN** a task is created on a codex backend with an explicitly selected account that defines only `claude_config_dir`
- **THEN** creation fails with an error naming the account and backend, and no worktree or row is left behind

#### Scenario: Inherited account lacking the backend's tool
- **WHEN** a coordinator on a Claude-only account spawns a Codex worker
- **THEN** the worker is created on `default` and the fallback is logged

#### Scenario: Moved task keeps its new account
- **WHEN** a task is moved from `default` to `personal` and later restarted
- **THEN** it resumes under `personal`
