## MODIFIED Requirements

### Requirement: hera_accept coordinator accept of a bound role's work

The system SHALL provide a coordinator-only `hera_accept(cwd, role_name, [orchestrator], [message])` MCP tool that marks a role's bound task `complete` – the operator/coordinator-facing counterpart to the worker's own `hera_status(done)` self-report. Unlike the worker-done roll (which requires the task be `in_progress`), `hera_accept` acts from ANY non-complete status (`in_progress`, `in_review`, or otherwise) – the coordinator's explicit accept is authoritative regardless of whether the target already self-reported done.

On a genuine flip, the system SHALL send the target role a check-in message (never a forced session stop or restart) whose default body tells it its work has been accepted and marked complete, and explicitly instructs it to reply with exactly one of: confirming it has no other tasks and is winding down, telling the coordinator it still has more work to do, or a question if it isn't sure which applies. That reply is informational only – it SHALL NOT automatically reopen the task; a premature accept is undone only via the explicit revive path (`ReviveHeraWorkerToInProgress`'s `complete` source, see below), never by the reply's content. An optional `message` is appended to that default body. On a target task that is ALREADY `complete`, the tool SHALL return success describing a no-op – no second status write, no second message – rather than erroring or re-notifying.

The underlying status flip and notification SHALL be implemented by a single shared primitive (`internal/hera.AcceptRole`) also called by the plan-DAG gater's auto-accept (see the `task-orchestration` capability's "Gater auto-accepts a materialized node's blockers" requirement), so the two trigger paths can never drift.

`hera_accept` SHALL share `hera_revive`'s exact caller-authorization shape: the caller MUST hold a live coordinator binding in the target's orchestrator, and the target role MUST NOT be the caller's own role.

Derived from: `internal/hera/accept.go` (`AcceptRole`), `internal/mcp/hera.go` (`toolHeraAccept`), `internal/heragater/heragater.go` (`acceptBlockers`, the gater's caller).

#### Scenario: Accept flips an in-progress worker to complete and notifies it

- **WHEN** a coordinator calls `hera_accept` on a worker role whose task is `in_progress`
- **THEN** the task's status flips to `complete` and the worker role receives a message stating its work was accepted and asking it to reply confirming it is winding down, telling the coordinator it has more work, or asking a question

#### Scenario: Accept flips an in-review worker (the ordinary done-report state) to complete

- **WHEN** a coordinator calls `hera_accept` on a worker role whose task is `in_review` (having already self-reported `hera_status(done)`)
- **THEN** the task's status flips to `complete` identically to the in_progress case

#### Scenario: Accepting an already-complete task is a clean no-op

- **WHEN** a coordinator calls `hera_accept` on a role whose task is already `complete`
- **THEN** the tool reports success with a no-op note; no second status write occurs and no second message is sent

#### Scenario: An optional custom message is appended

- **WHEN** a coordinator calls `hera_accept` with a non-empty `message`
- **THEN** the sent notification includes that message alongside the default acceptance body

#### Scenario: The acceptance message is a closed-loop check-in, not a one-way notice

- **WHEN** `hera_accept` sends its default acceptance message to the target role
- **THEN** the message explicitly instructs the recipient to reply with exactly one of confirming it is winding down, telling the coordinator it has more work to do, or asking a question, and states that the reply never automatically reopens the task

#### Scenario: hera_accept is coordinator-only

- **WHEN** a worker or freelance role calls `hera_accept`
- **THEN** the tool errors that only coordinators may accept a role's work

#### Scenario: hera_accept rejects targeting the caller's own role

- **WHEN** a coordinator calls `hera_accept` naming its own role
- **THEN** the tool errors that the target must be a different role the caller coordinates

#### Scenario: hera_accept does not itself stop the target's session

- **WHEN** `hera_accept` flips a task's status to complete
- **THEN** the call itself does not stop, restart or detach the target role's live session; the session keeps running to receive and answer the check-in, and is stopped only once idle by the agent-execution capability's finished-session check
