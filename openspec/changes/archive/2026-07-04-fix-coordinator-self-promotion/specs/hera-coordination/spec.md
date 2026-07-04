# hera-coordination (delta)

## MODIFIED Requirements

### Requirement: hera_new_orchestrator bootstraps and claims the coordinator role

The system SHALL, on `hera_new_orchestrator`, create (or idempotently fetch) the named orchestrator, then transactionally create a coordinator role bound to the calling task's worktree, persisting the caller's argus project on the role. It SHALL reject the call when the calling task already holds a live binding under that (target) orchestrator, directing the caller to `hera_join`. It SHALL ALSO reject the call — before creating or fetching the orchestrator, so no orphan orchestrator is left behind — when the calling task already holds a live **coordinator**-kind binding under a DIFFERENT orchestrator: a coordinator dispatches work with `hera_spawn_worker` (whose `project=` targets any repo) and MUST NOT bind its own session as a second coordinator of another orchestrator, so the rejection error SHALL direct the caller to spawn a worker, or — for genuine multi-project/multi-phase decomposition — to use the worker-promotion pattern or a `kind=subcoord` plan node. A caller re-calling for the SAME orchestrator it already coordinates falls through to the target-orchestrator rejection above (the `hera_join` guidance); a caller holding only worker/freelance bindings (worker self-promotion) or no binding at all (fresh bootstrap) SHALL be allowed to proceed. It SHALL mirror the role to the `task_meta` "hera" namespace best-effort (a mirror failure never undoes local state). Required args: `cwd`, `name`, `coordinator_role_name`.

#### Scenario: First call creates orchestrator + coordinator binding

- **WHEN** a task with no binding under orchestrator X calls hera_new_orchestrator(name=X)
- **THEN** orchestrator X exists, a coordinator role bound to the caller is created, and the binding id is returned

#### Scenario: Re-bootstrap under an already-bound orchestrator is rejected

- **WHEN** the caller already holds a live binding under the target orchestrator
- **THEN** the tool errors and directs the caller to hera_join

#### Scenario: A coordinator cannot create another orchestrator on its own session

- **WHEN** a task that already holds a live coordinator binding under orchestrator A calls hera_new_orchestrator(name=B) for a different orchestrator B
- **THEN** the tool errors, no orchestrator B and no second coordinator binding are created on that task, and the error directs the caller to hera_spawn_worker (or a kind=subcoord plan node for a real sub-team)

#### Scenario: Worker self-promotion to sub-coordinator is still allowed

- **WHEN** a task holding only a live worker binding calls hera_new_orchestrator(name=B)
- **THEN** orchestrator B and a coordinator role bound to the caller are created (the worker-promotion pattern succeeds)
