## MODIFIED Requirements

### Requirement: Master-only gating for destructive and configuration endpoints

Endpoints that mutate shared configuration or act across all tasks SHALL require the master token: project CRUD, backend CRUD, the config snapshot read (`GET /api/config`), settings writes (`PUT /api/settings`), token minting/listing/revocation, stop-all sessions, prune-completed, and the cleanup-candidates bulk apply endpoint. A request authenticated with a device token or a plugin-scoped token SHALL be rejected with 403 for these endpoints. The curated settings read (`GET /api/settings`) SHALL remain available to any authenticated token, including device tokens. Per-task operations (stop, delete, archive, rename, set-status, write input) SHALL remain available to any authenticated token. The cleanup-candidates compute-trigger and list (read) endpoints are NOT master-gated — they are read/trigger operations available to any authenticated token, matching the per-task-operation tier rather than the bulk-mutation tier.

#### Scenario: Device token rejected from a master-only endpoint

- **WHEN** a request authenticated as `device` calls a master-only endpoint such as stop-all, token minting, project/backend CRUD, or the cleanup-candidates apply endpoint
- **THEN** the server responds 403 Forbidden

#### Scenario: Master token permitted

- **WHEN** a request authenticated as `master` calls a master-only endpoint
- **THEN** the endpoint executes and returns its normal success status

#### Scenario: Plugin scope token does not satisfy master

- **WHEN** a request authenticated as `scope:<plugin>` calls a master-only endpoint
- **THEN** the server responds 403 Forbidden

#### Scenario: Device token allowed for per-task operations

- **WHEN** a request authenticated as `device` calls a per-task operation such as stop or delete
- **THEN** the operation executes (subject to task existence)

#### Scenario: Settings read available to a device token

- **WHEN** a request authenticated as `device` calls `GET /api/settings`
- **THEN** the server returns the curated settings view (the read is not master-gated)

#### Scenario: Device token allowed to trigger or read cleanup-candidate classification

- **WHEN** a request authenticated as `device` calls the cleanup-candidates compute-trigger or list endpoint
- **THEN** the operation executes normally — only the bulk apply endpoint is master-gated

## ADDED Requirements

### Requirement: Cleanup-candidate classification endpoints

The system SHALL expose endpoints to trigger and read an on-demand, daemon-side classification of tasks matching the stuck-task predicate (`archived=1`, `status=in_review`, no live Hera binding): `POST /api/maintenance/cleanup-candidates/compute` starts a background classification pass (a no-op if one is already in flight) covering eligible tasks without a cached verdict or with a non-terminal (Needs Review) cached verdict, and `GET /api/maintenance/cleanup-candidates` returns the current cached results plus a `computing` flag. Classification SHALL run entirely server-side (the merge-safety classifier), never on the calling client. Results SHALL be cached (surviving a daemon restart) so repeat calls do not re-spend the shared GitHub GraphQL budget on already-confirmed-safe tasks.

#### Scenario: Compute starts a background pass
- **WHEN** `POST /api/maintenance/cleanup-candidates/compute` is called and no computation is currently running
- **THEN** the server starts a background classification pass and returns immediately without waiting for it to finish

#### Scenario: Compute is idempotent while running
- **WHEN** `POST /api/maintenance/cleanup-candidates/compute` is called while a computation is already in flight
- **THEN** the server does not start a second concurrent pass

#### Scenario: List reflects cached results and in-flight status
- **WHEN** `GET /api/maintenance/cleanup-candidates` is called
- **THEN** the response includes every currently-eligible task's last cached classification (tier, safe/not-safe, reason) and a `computing` flag reflecting whether a pass is currently running

#### Scenario: Safe verdicts are cached as terminal
- **WHEN** a task's cached verdict is confirmed-safe
- **THEN** a subsequent compute pass does not re-classify it (no repeat GraphQL cost for that task)

#### Scenario: Needs-review verdicts are re-checked on the next compute
- **WHEN** a task's cached verdict is not-confirmed (needs review)
- **THEN** a subsequent compute pass re-classifies it, since a later merge could change the outcome

### Requirement: Cleanup-candidate bulk apply is master-only and snapshot-scoped

`POST /api/maintenance/cleanup-candidates/apply` SHALL require the master token (an across-all-tasks bulk mutation, matching the existing master-only gating for prune-completed and stop-all). It SHALL accept a `scope` of `safe` or `all`, and SHALL act on the currently cached classification snapshot (not a fresh live re-classification), advancing each matching task's status to `complete` via the existing generic status-transition primitive. Before mutating each task it SHALL re-verify the task still matches the stuck-task predicate and SHALL skip (not error) any that no longer do.

#### Scenario: Device token rejected
- **WHEN** a request authenticated as `device` or `scope:<plugin>` calls the apply endpoint
- **THEN** the server responds 403 Forbidden

#### Scenario: Safe-only scope only touches confirmed-safe tasks
- **WHEN** the master token calls apply with `scope: "safe"`
- **THEN** only tasks whose cached verdict is confirmed-safe have their status advanced to `complete`; needs-review tasks are untouched

#### Scenario: All scope touches every listed candidate
- **WHEN** the master token calls apply with `scope: "all"`
- **THEN** every currently cached candidate (safe and needs-review) has its status advanced to `complete`

#### Scenario: A task that no longer qualifies is skipped, not errored
- **WHEN** apply processes a cached candidate whose task has since stopped matching the stuck-task predicate (e.g. it gained a live Hera binding, or its status already changed)
- **THEN** that task is skipped without aborting or erroring the rest of the batch

#### Scenario: Apply acts on the reviewed snapshot, not a fresh classification
- **WHEN** apply runs
- **THEN** it uses the classification results already returned by the most recent `GET /api/maintenance/cleanup-candidates` call, not a newly computed pass
