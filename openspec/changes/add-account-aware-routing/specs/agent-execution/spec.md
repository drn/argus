## MODIFIED Requirements

### Requirement: Account resolved once at task creation and immutable

The system SHALL record an `account` on each new task, resolved at the single creation call site (`CreateAndStart`) as: explicit selection, else the account chosen by `[backend_routing]` tier resolution when that resolution chose the task's backend, else an inherited account, else the project's `project_accounts` entry, else `default_account`, else `default`. Tier resolution chooses the backend only when neither an explicit backend nor a project backend applies; in every other case it supplies no account and the remaining precedence is unchanged. When an explicit account is given without an explicit backend, tier resolution SHALL consider only tiers on that account; if none qualifies, the backend falls through to the default-backend precedence. An explicit `"default"` SHALL be honored as `default` and never re-resolved through the project or global default; an empty value SHALL mean no selection was made. `CreateAndStart` SHALL resolve the backend before creating the worktree. Creating a task with an unknown account name, or with an explicitly selected account that does not support the resolved backend (`Config.AccountSupports`), SHALL be rejected before any side effect. A project/global default that does not support the resolved backend SHALL fall back to `default` and be logged under `[account]`; an inherited account (hera, MCP `task_create`) that does not support it SHALL be rejected. Hera workers, sub-coordinators and freelancers SHALL inherit the spawning coordinator's account, a coordinator on `default` pinning `default`, except where a routed account applies. A spawn whose coordinator account cannot be read SHALL fail rather than resolve the project default. Fork-created tasks SHALL keep the source task's account. The stored value SHALL never be re-resolved on resume or restart.

#### Scenario: Changing the global default does not move existing tasks
- **WHEN** `default_account` changes after a task exists
- **THEN** the task still spawns and resumes under its stored account

#### Scenario: Worker inherits coordinator account
- **WHEN** a coordinator on account `personal` spawns a hera worker with an explicit backend
- **THEN** the worker task's `account` is `personal`

#### Scenario: Default-account coordinator pins default
- **WHEN** a coordinator on `default` spawns a worker with an explicit backend in a project whose `project_accounts` entry is `work`
- **THEN** the worker runs on `default`

#### Scenario: Explicit default is not re-resolved
- **WHEN** a task is created with `account: "default"` in a project whose default is `work`
- **THEN** the task runs on `default`

#### Scenario: Explicit account lacking the backend's tool
- **WHEN** a task is created on a codex backend with an explicitly selected account that defines only `claude_config_dir`
- **THEN** creation fails with an error naming the account and backend, and no worktree or row is left behind

#### Scenario: Inherited account lacking the backend's tool
- **WHEN** a coordinator on a Claude-only account spawns a worker with an explicit `codex` backend
- **THEN** creation fails with an error naming the inherited account and backend, and no worktree or row is left behind

#### Scenario: Routed account replaces the coordinator's account
- **WHEN** a coordinator on `default` spawns a worker with no explicit backend or account, and tier resolution chooses `claude` on `personal`
- **THEN** the worker task's backend is `claude` and its `account` is `personal`

#### Scenario: Routed Codex worker under a Claude-only coordinator
- **WHEN** a coordinator on a Claude-only account spawns a worker with no explicit backend or account, and tier resolution chooses the `codex` tier
- **THEN** the worker is created on `codex` with the Codex tier's account, rather than failing on the coordinator's account

#### Scenario: Explicit account beats a routed account
- **WHEN** a worker is spawned with `account: "personal"` and no explicit backend, and unrestricted tier resolution would have chosen `codex`
- **THEN** tier resolution considers only the `personal` tier, and the worker runs on `claude` with account `personal`

#### Scenario: Project backend suppresses routing
- **WHEN** a worker is spawned with no explicit backend in a project whose configured backend is `claude`, by a coordinator on `personal`
- **THEN** the tier list is not consulted and the worker inherits `personal`
