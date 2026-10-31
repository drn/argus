## MODIFIED Requirements

### Requirement: task_create account selection and inheritance

`task_create` SHALL accept optional `account`, `caller_id` and `cwd` parameters. An explicit `account` (`"default"` included) SHALL be used as given; when the task store exposes configuration, an unknown account SHALL return a tool error. Without `account`, a caller identified by `caller_id` or `cwd` SHALL pass its own stored account (a default-account caller pinning `default`) to the new task as an inherited account, except that a routed caller SHALL pass no account so the new task is routed; an inherited account that cannot run the backend SHALL fail the call. When the caller cannot be resolved, the account SHALL resolve through the project/global default and the miss SHALL be logged.

#### Scenario: Worker spawned by an agent inherits its account
- **WHEN** a task on account `personal` calls `task_create` with its `caller_id` and no `account`
- **THEN** the new task runs on `personal`

#### Scenario: Unknown explicit account
- **WHEN** `task_create` is called with `account: "nope"`
- **THEN** the response is a tool error and no task is created

#### Scenario: Routed caller lets the child route
- **WHEN** a routed task on `personal` calls `task_create` with its `caller_id` and no `account`
- **THEN** the new task's account comes from routing and it is marked routed

## ADDED Requirements

### Requirement: task_move_account tool

The server SHALL expose a `task_move_account` tool taking `task_id`, `account` (a configured account, `default`, or `auto`), an optional `force` (interrupt a busy session), and optional `caller_id` / `cwd`. It SHALL perform the account-move operation and resume the task's session, returning the source account, the target account, and whether the conversation was resumed. Every rejection of the account-move operation SHALL be returned as a tool error naming the reason. A call whose `caller_id`, or whose `cwd` resolved to a task, equals `task_id` SHALL be rejected with an error saying a task cannot move itself and that the user or its coordinator must move it. The tool description SHALL say that moving copies the conversation into the target account and that `GET /api/accounts` (via `argus_api_get`) shows each account's usage.

#### Scenario: Coordinator moves a stuck worker
- **WHEN** a coordinator calls `task_move_account` with a worker's `task_id`, `account: "auto"`, and its own `caller_id`, and `personal` has the lowest usage among the other accounts
- **THEN** the worker resumes on `personal` and the result names the source account and `personal`

#### Scenario: Self-move rejected
- **WHEN** a task calls `task_move_account` with its own ID as both `task_id` and `caller_id`
- **THEN** the response is a tool error and nothing changes

#### Scenario: Busy target rejected
- **WHEN** the named task's session is actively producing output and `force` is not set
- **THEN** the response is a tool error and nothing changes
