## MODIFIED Requirements

### Requirement: hera_move relocates the caller's binding to a different orchestrator

The system SHALL, on `hera_move`, relocate the calling task's live hera binding to a different orchestrator: it SHALL resolve the caller's current live binding (via the same cwd→task→binding resolution used elsewhere, accepting an optional `from_orchestrator` to disambiguate when the task holds 2+ live bindings), then — transactionally — end that binding (`ended_at`/`end_reason: "moved"`) and create a new role+binding of kind `worker` or `freelance` under the target `orchestrator` (rejecting `coordinator`, mirroring `hera_join`). It SHALL reject the call, ending and creating nothing, when the calling task holds no live binding at all (directing the caller to `hera_join` or `hera_new_orchestrator` instead — there is nothing to move), when the resolved source orchestrator equals the target orchestrator (a no-op; directing the caller to `hera_join` without `role_name` to see its current binding), or when the resolved SOURCE binding's role is coordinator-kind (a coordinator's binding IS its orchestrator's coordination — ending it would orphan the whole subtree the coordinator was running, leaving a disconnected worker/freelance stub under the target with no structural link back; the rejection names the caller's role and orchestrator and directs the caller to ask a human to use the Hera TUI's `J` adopt/reparent key instead, since no agent-facing tool nests an existing coordinator + subtree under a new parent). The response SHALL report the source orchestrator and role name that were moved, plus the new binding id. Required args: `cwd`, `orchestrator`, `role_name`, `kind`. Optional args: `from_orchestrator`, `status`.

#### Scenario: Happy path moves the binding

- **WHEN** a task holding a live binding under orchestrator A calls hera_move targeting orchestrator B with a role_name and kind
- **THEN** the binding under A is ended with end_reason "moved", a new role+binding is created under B, and the response reports A + the moved role's name plus the new binding id

#### Scenario: Nothing to move

- **WHEN** an unbound task calls hera_move
- **THEN** the tool errors that there is nothing to move and directs the caller to hera_join or hera_new_orchestrator, creating no binding

#### Scenario: Moving to the same orchestrator is a no-op error

- **WHEN** a task holding a live binding under orchestrator A calls hera_move targeting orchestrator A
- **THEN** the tool errors and directs the caller to hera_join without role_name, ending and creating nothing

#### Scenario: Ambiguous caller requires from_orchestrator

- **WHEN** a task holding live bindings under two orchestrators calls hera_move without from_orchestrator
- **THEN** the tool errors listing the bound orchestrator names, and a follow-up call supplying from_orchestrator succeeds

#### Scenario: Destination kind=coordinator is rejected

- **WHEN** hera_move is called with kind=coordinator
- **THEN** the tool errors and directs the caller to hera_new_orchestrator

#### Scenario: A live coordinator's own binding cannot be moved

- **WHEN** a task holding a live COORDINATOR binding under orchestrator A calls hera_move targeting orchestrator B with kind=worker or kind=freelance
- **THEN** the tool errors identifying the caller as orchestrator A's coordinator and directing them to the Hera TUI's `J` adopt/reparent key, and the original coordinator binding under A remains live and unchanged, with no role or binding created under B
