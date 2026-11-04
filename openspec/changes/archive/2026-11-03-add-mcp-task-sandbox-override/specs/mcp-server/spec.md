## ADDED Requirements

### Requirement: task_create sandbox override

`task_create` SHALL accept an optional `sandbox_override` of `"enabled"` or
`"disabled"` (empty or omitted means inherit) and pass it through to task
creation so it is persisted as the task's sandbox override. Any other value
SHALL be rejected with a tool error before a task is created. `"disabled"` SHALL
be rejected unless the calling task is identified (via `caller_id` or `cwd`) and
is not sandboxed; `"enabled"` SHALL always be accepted.

#### Scenario: Force-enable sandbox
- **WHEN** `task_create` is called with `sandbox_override: "enabled"`
- **THEN** the created task carries `SandboxOverride == "enabled"`

#### Scenario: Omitted inherits
- **WHEN** `task_create` is called without `sandbox_override`
- **THEN** the created task carries an empty override

#### Scenario: Invalid value
- **WHEN** `task_create` is called with `sandbox_override: "maybe"`
- **THEN** it returns a tool error and creates no task

#### Scenario: Disabled from a sandboxed caller
- **WHEN** `task_create` is called with `sandbox_override: "disabled"` by a sandboxed task
- **THEN** it returns a tool error and creates no task

#### Scenario: Disabled from an unsandboxed caller
- **WHEN** `task_create` is called with `sandbox_override: "disabled"` by an identified unsandboxed task
- **THEN** the created task carries `SandboxOverride == "disabled"`

#### Scenario: Disabled with unidentified caller
- **WHEN** `task_create` is called with `sandbox_override: "disabled"` and no resolvable caller
- **THEN** it returns a tool error and creates no task
