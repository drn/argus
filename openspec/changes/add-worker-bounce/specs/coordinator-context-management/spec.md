## MODIFIED Requirements

### Requirement: recycle_coord restarts a coordinator on its existing task without losing its place

The system SHALL provide a `recycle_coord` primitive that terminates a hera role's running session and starts a fresh one on the identical argus task row — same worktree, same branch, same hera binding (bindings key on task ID, not session ID, so no binding change is needed). The primitive SHALL be reachable via two independent trigger paths:

- **Self-service**: a role signals recycle intent (see the `hera_status` extension in `hera-coordination`, now accepted from any role kind); the daemon SHALL defer the actual kill-and-restart until the session reaches genuine idleness (no forced interruption mid-turn). For a coordinator this is driven by the `argus coord-hook` budget nudge; for a worker or freelance role it is driven only by a human-initiated rail bounce (see `hera-view`) that instructs the role to call `hera_status(request_recycle=true)` itself — there is no automated nudge or budget tracking for worker/freelance roles.
- **Human-forced**: an operator action on a coordinator selection (see the rail keybinding in `hera-view`) SHALL kill and restart immediately, without waiting for idleness — this path exists specifically for a coordinator that is wedged and will never become idle on its own. This path SHALL remain coordinator-only; a worker or freelance role is reachable only via the self-service path above.

Before restarting, the primitive SHALL check for and terminate any stray background job tied to the outgoing session (via the session's own agent-registry lookup) in addition to the primary session kill, so a surviving background job cannot cause a worktree-write conflict with the new session.

The fresh session's opening prompt SHALL be assembled entirely server-side, before the session starts, from: the role's stored mission prompt, the current plan-DAG node states for the role's orchestrator, and any `handoff_note` present in `task_meta`. The new session SHALL NOT be required to make any tool call to obtain any of these three — they arrive already present in its first message. The assembled prompt SHALL clearly mark the role's stored mission text as historical background — not a live instruction to act on now — and SHALL state, ahead of showing that mission text, that the current plan-DAG state and handoff note (which follow) supersede it. This guards against a fresh session anchoring on a stale original mission as its primary directive when the current state shows the work it describes is already done or superseded. This framing, and the whole seed-assembly process, SHALL apply identically regardless of the recycled role's kind — the prompt SHALL NOT assume the recycled role is a coordinator.

#### Scenario: Same task survives a recycle

- **WHEN** `recycle_coord` completes for a coordinator role
- **THEN** the role's binding still points at the same argus task ID, worktree path, and branch as before the recycle

#### Scenario: Self-service recycle waits for idleness

- **WHEN** a coordinator requests recycle and its session is still actively producing output
- **THEN** the kill-and-restart does not occur until the session becomes idle

#### Scenario: Self-service recycle works for a worker role

- **WHEN** a worker role's `task_meta` records `pending_recycle=true` and its session is idle
- **THEN** the recycle watcher drives it through `recycle_coord`'s self-service path, restarting it in place same as a coordinator

#### Scenario: Self-service recycle works for a freelance role

- **WHEN** a freelance role's `task_meta` records `pending_recycle=true` and its session is idle
- **THEN** the recycle watcher drives it through `recycle_coord`'s self-service path, restarting it in place same as a coordinator

#### Scenario: Human-forced recycle does not wait for idleness

- **WHEN** an operator forces a recycle on a coordinator via the rail
- **THEN** the kill-and-restart occurs immediately regardless of the session's activity state

#### Scenario: Human-forced recycle remains coordinator-only

- **WHEN** the rail's human-forced recycle action is invoked with a worker or freelance role selected
- **THEN** no immediate kill-and-restart occurs — only the self-service path (instruct-and-wait, see `hera-view`) is reachable for that role kind

#### Scenario: Seed prompt requires no follow-up tool calls

- **WHEN** a fresh session starts after a recycle
- **THEN** its opening prompt already contains the role's mission, the current plan-DAG state, and any handoff note, with no `hera_join` or `hera_plan` call needed to obtain them

#### Scenario: A stray background job is cleaned up before restart

- **WHEN** the outgoing session has a background job still running under its session identity at recycle time
- **THEN** that job is terminated before the new session starts

#### Scenario: Original mission is marked historical, not a live instruction

- **WHEN** a fresh session's opening prompt is assembled after a recycle
- **THEN** the mission text is preceded by framing marking it as background/historical and stating that the current plan-DAG state and handoff note below supersede it, so the mission does not read as a live directive to act on now
