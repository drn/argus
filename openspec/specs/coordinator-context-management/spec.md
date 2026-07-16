# coordinator-context-management Specification

## Purpose

Hera coordinators are long-lived — unlike a disposable worker, a coordinator persists across an entire multi-stage orchestration and personally accumulates every token it reads, delegates, or relays along the way. This capability gives coordinators a visible context-size signal, a budget-driven nudge to seek a safe seam and recycle before they get too bloated or wedged, a spawn-time discipline spec pushing them to stay lean in the first place, and a `recycle_coord` primitive that resets a coordinator's session in place — same task, worktree, branch, and binding — without losing its mission or plan-DAG state.
## Requirements
### Requirement: Context-budget Stop hook stamps a live signal and nudges over budget

The system SHALL provide an `argus coord-hook` subcommand, invoked as a Claude Code `Stop` hook, that on every invocation first checks for a non-empty `ARGUS_TASK_ID` and a resolved `coordinator`-kind role; it SHALL no-op with no side effects and no error when either check fails. When both checks pass, the subcommand SHALL: (1) read the transcript at the hook's supplied `transcript_path` and extract the latest assistant message's `usage.cache_read_input_tokens`; (2) self-discover the daemon's live REST port and `~/.argus/api-token` (no port/token is exported into the spawned session's environment); (3) overwrite `task_meta` under namespace `hera`, key `context_size`, with the extracted value; and (4) compare the value against the project's configured `coordinator_context_budget` — when the value is at or above the budget, the subcommand SHALL emit a Claude Code hook "block" decision whose reason instructs the coordinator to reach a safe seam and call for a reboot.

Because the hook re-evaluates on every Stop event rather than firing once, the nudge SHALL recur on every subsequent turn for as long as `context_size` remains at or above budget, and SHALL stop recurring the first turn it drops back below budget (e.g. immediately after a recycle resets accumulated context).

#### Scenario: Non-hera session is a no-op

- **WHEN** the Stop hook fires in a session with no `ARGUS_TASK_ID` set
- **THEN** it exits with no side effects — no `task_meta` write, no blocking decision

#### Scenario: Worker session is a no-op

- **WHEN** the Stop hook fires in a session bound to a `worker`-kind role
- **THEN** it exits with no side effects

#### Scenario: Context size is always stamped for a coordinator session

- **WHEN** the Stop hook fires in a session bound to a `coordinator`-kind role
- **THEN** `task_meta` (`hera`, `context_size`) is overwritten with the latest computed value, regardless of whether the budget is exceeded

#### Scenario: Over-budget nudge repeats every turn until resolved

- **WHEN** a coordinator's `context_size` is at or above its configured budget on two consecutive turns
- **THEN** the Stop hook emits the reach-a-seam nudge on both turns

#### Scenario: Nudge stops the turn context drops back below budget

- **WHEN** a coordinator's `context_size` was over budget and, on a later turn, is below budget
- **THEN** the Stop hook does not emit the nudge on that later turn

### Requirement: Coordinator spawn orientation carries a context-discipline spec

The system SHALL extend `HeraCoordinatorOrientation`'s prompt text with instructions covering: a low default reasoning-effort posture with explicit escalation for genuine judgment calls; a delegation rule directing investigation-class work (reading files, understanding code, diagnosing a failure) to Claude's native sub-agent tool rather than `hera_spawn_worker`, reserving `hera_spawn_worker` for work needing its own git worktree, branch, or PR; a pointers-not-payloads convention for messages and reports (reference `path:line`, branch names, and task IDs rather than pasting full content); and a distillate-harvest step before winding down (bringing `design.md`'s open-questions/discovery-findings sections current, then recording a short `handoff_note`).

#### Scenario: Every newly spawned coordinator receives the discipline text

- **WHEN** a new coordinator role is spawned via `hera_new_orchestrator`
- **THEN** its orientation prompt includes the effort, delegation, pointers-not-payloads, and harvest-before-retire instructions

### Requirement: recycle_coord restarts a coordinator on its existing task without losing its place

The system SHALL provide a `recycle_coord` primitive that terminates a coordinator's running session and starts a fresh one on the identical argus task row — same worktree, same branch, same hera binding (bindings key on task ID, not session ID, so no binding change is needed). The primitive SHALL be reachable via two independent trigger paths:

- **Self-service**: a coordinator signals recycle intent (see the `hera_status` extension in `hera-coordination`); the daemon SHALL defer the actual kill-and-restart until the session reaches genuine idleness (no forced interruption mid-turn).
- **Human-forced**: an operator action (see the rail keybinding in `hera-view`) SHALL kill and restart immediately, without waiting for idleness — this path exists specifically for a coordinator that is wedged and will never become idle on its own.

Before restarting, the primitive SHALL check for and terminate any stray background job tied to the outgoing session (via the session's own agent-registry lookup) in addition to the primary session kill, so a surviving background job cannot cause a worktree-write conflict with the new session.

The fresh session's opening prompt SHALL be assembled entirely server-side, before the session starts, from: the role's stored mission prompt, the current plan-DAG node states for the role's orchestrator, and any `handoff_note` present in `task_meta`. The new session SHALL NOT be required to make any tool call to obtain any of these three — they arrive already present in its first message. The assembled prompt SHALL clearly mark the role's stored mission text as historical background — not a live instruction to act on now — and SHALL state, ahead of showing that mission text, that the current plan-DAG state and handoff note (which follow) supersede it. This guards against a fresh session anchoring on a stale original mission as its primary directive when the current state shows the work it describes is already done or superseded.

#### Scenario: Same task survives a recycle

- **WHEN** `recycle_coord` completes for a coordinator role
- **THEN** the role's binding still points at the same argus task ID, worktree path, and branch as before the recycle

#### Scenario: Self-service recycle waits for idleness

- **WHEN** a coordinator requests recycle and its session is still actively producing output
- **THEN** the kill-and-restart does not occur until the session becomes idle

#### Scenario: Human-forced recycle does not wait for idleness

- **WHEN** an operator forces a recycle on a coordinator via the rail
- **THEN** the kill-and-restart occurs immediately regardless of the session's activity state

#### Scenario: Seed prompt requires no follow-up tool calls

- **WHEN** a fresh session starts after a recycle
- **THEN** its opening prompt already contains the role's mission, the current plan-DAG state, and any handoff note, with no `hera_join` or `hera_plan` call needed to obtain them

#### Scenario: A stray background job is cleaned up before restart

- **WHEN** the outgoing session has a background job still running under its session identity at recycle time
- **THEN** that job is terminated before the new session starts

#### Scenario: Original mission is marked historical, not a live instruction

- **WHEN** a fresh session's opening prompt is assembled after a recycle
- **THEN** the mission text is preceded by framing marking it as background/historical and stating that the current plan-DAG state and handoff note below supersede it, so the mission does not read as a live directive to act on now

