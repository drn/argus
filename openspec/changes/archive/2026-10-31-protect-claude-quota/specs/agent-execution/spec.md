## ADDED Requirements

### Requirement: Routed tier account participates in task creation precedence

CreateAndStart SHALL resolve and validate a backend/account pair before side effects, with account precedence explicit account (including inherited pins), project_accounts, routed tier account, default_account, default. It SHALL persist the chosen account and never reroute it during resume. Explicit backend selection SHALL still permit account tier selection when no higher-priority account applies. Existing validation and unsupported-account fallback rules SHALL remain in force. Spawn routing decisions SHALL log chosen backend/account and reason through both uxlog and slog.

#### Scenario: Explicit account wins

- **WHEN** a task explicitly requests default and a personal tier has more headroom
- **THEN** the task stays on default

#### Scenario: Project account wins

- **WHEN** project_accounts pins work and a personal tier is available
- **THEN** the task stays on work

#### Scenario: Routed account beats the global default

- **WHEN** routing chooses personal and no explicit/project account is supplied
- **THEN** the task records personal even if default_account is work

#### Scenario: Resume remains pinned

- **WHEN** usage changes after a task is created
- **THEN** restarting it retains its persisted backend and account

#### Scenario: Blank parent permits routing

- **WHEN** a worker is spawned under a coordinator with a blank stored account
- **THEN** the inherited blank is not a pin and the worker receives the routed account

## MODIFIED Requirements

### Requirement: Account resolved once at task creation and immutable

The system SHALL record an `account` on each new task, resolved at the single creation call site (`CreateAndStart`) as: explicit selection, else the project's `project_accounts` entry, else the routed tier account, else `default_account`, else `default`. An explicit `"default"` SHALL be honored as `default` and never re-resolved through the project or global default; an empty value SHALL mean no selection was made. `CreateAndStart` SHALL resolve the backend before creating the worktree. Creating a task with an unknown account name, or with an explicitly selected account that does not support the resolved backend (`Config.AccountSupports`), SHALL be rejected before any side effect. A project/global default that does not support the resolved backend SHALL fall back to `default` and be logged under `[account]`; an inherited account (hera, MCP `task_create`) that does not support it SHALL be rejected. Hera workers, sub-coordinators and freelancers SHALL inherit the spawning coordinator's stored account as-is. A nonblank named account or explicit `default` SHALL pin that account; a blank parent account SHALL permit routing. Explicit `default` SHALL remain nonblank when stored, while an implicit default SHALL remain blank. A spawn whose coordinator account cannot be read SHALL fail rather than resolve the project default. Fork-created tasks SHALL keep the source task's account. The stored value SHALL never be re-resolved on resume or restart.

#### Scenario: Changing the global default does not move existing tasks
- **WHEN** `default_account` changes after a task exists
- **THEN** the task still spawns and resumes under its stored account

#### Scenario: Worker inherits coordinator account
- **WHEN** a coordinator on account `personal` spawns a hera worker
- **THEN** the worker task's `account` is `personal`

#### Scenario: Explicit-default coordinator pins default
- **WHEN** a coordinator with a nonblank explicit `default` account spawns a worker in a project whose `project_accounts` entry is `work`
- **THEN** the worker runs on `default`

#### Scenario: Explicit default is not re-resolved
- **WHEN** a task is created with `account: "default"` in a project whose default is `work`
- **THEN** the task runs on `default`

#### Scenario: Explicit account lacking the backend's tool
- **WHEN** a task is created on a codex backend with an explicitly selected account that defines only `claude_config_dir`
- **THEN** creation fails with an error naming the account and backend, and no worktree or row is left behind

#### Scenario: Inherited account lacking the backend's tool
- **WHEN** a coordinator on a Claude-only account spawns a Codex worker
- **THEN** the worker creation is rejected before side effects, preserving the inherited account pin
