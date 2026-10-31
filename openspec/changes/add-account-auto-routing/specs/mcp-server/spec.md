## MODIFIED Requirements

### Requirement: task_create account selection and inheritance

`task_create` SHALL accept optional `account`, `caller_id` and `cwd` parameters. An explicit `account` (`"default"` and `"auto"` included) SHALL be used as given; when the task store exposes configuration, an unknown account SHALL return a tool error. Without `account`, a caller identified by `caller_id` or `cwd` SHALL pass its own stored account (a default-account caller pinning `default`) to the new task as an inherited account, except that an auto-routed caller SHALL pass `auto`; an inherited account that cannot run the backend SHALL fail the call. When the caller cannot be resolved, the account SHALL resolve through the project/global default and the miss SHALL be logged.

#### Scenario: Worker spawned by an agent inherits its account
- **WHEN** a task on account `personal` calls `task_create` with its `caller_id` and no `account`
- **THEN** the new task runs on `personal`

#### Scenario: Auto-routed caller passes auto
- **WHEN** an auto-routed task calls `task_create` with its `caller_id` and no `account`
- **THEN** the new task is placed by the auto selection rule and marked auto-routed

#### Scenario: Unknown explicit account
- **WHEN** `task_create` is called with `account: "nope"`
- **THEN** the response is a tool error and no task is created

## ADDED Requirements

### Requirement: task_move_account tool

The server SHALL expose a `task_move_account` tool taking `task_id`, `account` (a configured account, `default`, or `auto`), an optional `force` (interrupt a busy session), and an optional `caller_id`. It SHALL perform the account-move operation and then resume the task's session, returning the source account, the target account, and whether the conversation was resumed. Every rejection of the account-move operation SHALL be returned as a tool error naming the reason. A call whose `caller_id` equals `task_id` SHALL be rejected with an error saying a task cannot move itself and that the user or its coordinator must move it. The tool description SHALL say that moving copies the conversation into the target account and that `GET /api/accounts` (via `argus_api_get`) shows each account's usage.

#### Scenario: Coordinator moves a stuck worker
- **WHEN** a coordinator calls `task_move_account` with a worker's `task_id`, `account: "auto"`, and its own `caller_id`, and `personal` has headroom
- **THEN** the worker resumes on `personal` and the result names `default` → `personal`

#### Scenario: Self-move rejected
- **WHEN** a task calls `task_move_account` with its own ID as both `task_id` and `caller_id`
- **THEN** the response is a tool error and nothing changes

#### Scenario: Busy target rejected
- **WHEN** the named task's session is actively producing output
- **THEN** the response is a tool error and nothing changes
