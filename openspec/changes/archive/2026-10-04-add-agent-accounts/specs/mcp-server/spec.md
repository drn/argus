## ADDED Requirements

### Requirement: task_create account selection and inheritance

`task_create` SHALL accept optional `account`, `caller_id` and `cwd` parameters. An explicit `account` (`"default"` included) SHALL be used as given; when the task store exposes configuration, an unknown account SHALL return a tool error. Without `account`, a caller identified by `caller_id` or `cwd` SHALL pass its own stored account (a default-account caller pinning `default`) to the new task as an inherited account; an inherited account that cannot run the backend SHALL fail the call. When the caller cannot be resolved, the account SHALL resolve through the project/global default and the miss SHALL be logged.

#### Scenario: Worker spawned by an agent inherits its account
- **WHEN** a task on account `personal` calls `task_create` with its `caller_id` and no `account`
- **THEN** the new task runs on `personal`

#### Scenario: Unknown explicit account
- **WHEN** `task_create` is called with `account: "nope"`
- **THEN** the response is a tool error and no task is created
