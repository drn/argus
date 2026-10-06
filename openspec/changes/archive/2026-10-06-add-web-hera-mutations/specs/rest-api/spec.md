## MODIFIED Requirements

### Requirement: Hera roster endpoint

`GET /api/hera` remains read-only. Hera mutations are no longer TUI-only: the REST API SHALL additionally expose the mutation endpoints listed under "Hera mutation endpoints".

## ADDED Requirements

### Requirement: Hera mutation endpoints

The REST API SHALL expose authenticated POST endpoints under `/api/hera` for nuke (orchestrator cascade and role), archive/unarchive, pin/unpin, rename, role-status set, and orchestrator kanban-status set. Nuke endpoints SHALL require the master token. Every endpoint SHALL act on an explicit orchestrator or role id (never a bare task id), SHALL NEVER hard-delete a hera row (nuke stamps `nuked_at`), and SHALL return 404 for an unknown id, 409 on a name conflict, and 400 for an invalid status/kanban value or a kanban change on a nested orchestrator. A nuke SHALL end bindings as `user_deleted`, archive sole-bound tasks, preserve tasks bound outside the subtree, and delegate worktree reclaim to `ReconcileHeraReclaims`. `GET /api/hera/orchestrators/{id}/nuke-preview` SHALL return the counts the nuke would act on without mutating anything.

#### Scenario: Nuke cascades the subtree
- **WHEN** a master-token client POSTs `/api/hera/orchestrators/{id}/nuke` for an orchestrator with a nested sub-orchestrator
- **THEN** both orchestrators and all their roles are stamped nuked and disappear from `GET /api/hera`

#### Scenario: Non-master token rejected
- **WHEN** a non-master token POSTs a nuke endpoint
- **THEN** the response is 403 and nothing changes

#### Scenario: Multi-bound task preserved
- **WHEN** a nuked role's task is also live-bound under an orchestrator outside the subtree
- **THEN** the task, its session and worktree are left untouched; only the role row is nuked

#### Scenario: Hiding a worker stops its session
- **WHEN** a client archives a worker role with a live session
- **THEN** the role is archived and the session stopped; unarchive never touches a session
