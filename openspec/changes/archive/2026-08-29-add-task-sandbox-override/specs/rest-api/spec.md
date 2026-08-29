## MODIFIED Requirements

### Requirement: Task creation

The server SHALL create a task from a JSON body containing `prompt`, `project`, and optional `name`, `backend`, and `sandbox_override` fields. `project` SHALL be required. At least one of `name` or `prompt` SHALL be required. When `name` is empty it SHALL be synthesized from the prompt (first 40 characters, newlines/tabs replaced with spaces) and an asynchronous rename may follow. A `backend` that is not configured SHALL be rejected before any worktree is created. A `sandbox_override` that is not one of `""`, `"enabled"`, or `"disabled"` SHALL be rejected before any worktree is created. On success the server SHALL respond 201 with the new task id, name, and status. Requests with a `multipart/form-data` body SHALL additionally accept file attachments.

#### Scenario: Creates a task

- **WHEN** a valid create request with name, prompt, and project is submitted
- **THEN** the server responds 201 with the task id and name

#### Scenario: Missing project rejected

- **WHEN** a create request omits `project`
- **THEN** the server responds 400 Bad Request

#### Scenario: Missing name and prompt rejected

- **WHEN** a create request supplies neither `name` nor `prompt`
- **THEN** the server responds 400 Bad Request

#### Scenario: Unknown backend rejected

- **WHEN** a create request names a backend that is not configured
- **THEN** the server responds 400 Bad Request

#### Scenario: Backend override persists to the task

- **WHEN** a create request names a configured backend
- **THEN** the created task records that backend

#### Scenario: Sandbox override persists to the task

- **WHEN** a create request sets `sandbox_override` to `"enabled"` or `"disabled"`
- **THEN** the created task records that override and it is reflected in the task's resolved sandbox state

#### Scenario: Invalid sandbox override rejected

- **WHEN** a create request sets `sandbox_override` to a value other than `""`, `"enabled"`, or `"disabled"`
- **THEN** the server responds 400 Bad Request
