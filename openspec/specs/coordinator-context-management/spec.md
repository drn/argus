# coordinator-context-management Specification

## Purpose

Hera coordinators are long-lived — unlike a disposable worker, a coordinator persists across an entire multi-stage orchestration and personally accumulates every token it reads, delegates, or relays along the way. This capability gives coordinators a visible context-size signal, a budget-driven nudge to seek a safe seam and recycle before they get too bloated or wedged, a spawn-time discipline spec pushing them to stay lean in the first place, and a `recycle_coord` primitive that resets a coordinator's session in place — same task, worktree, branch, and binding — without losing its mission or plan-DAG state.
## Requirements
### Requirement: Context-budget Stop hook stamps a live signal and nudges over budget

The system SHALL provide an `argus coord-hook` subcommand, invoked as a Claude Code `Stop` hook, that on every invocation first checks for a non-empty `ARGUS_TASK_ID` and a resolved `coordinator`-kind role; it SHALL no-op with no side effects and no error when either check fails. When both checks pass, the subcommand SHALL: (1) read the transcript at the hook's supplied `transcript_path` and extract the latest assistant message's `usage.cache_read_input_tokens`; (2) self-discover the daemon's live REST port and `~/.argus/api-token` (no port/token is exported into the spawned session's environment); (3) overwrite `task_meta` under namespace `hera`, key `context_size`, with the extracted value; and (4) compare the value against the project's configured `coordinator_context_budget` — when the value is at or above the budget AND the increment gate below allows it AND (after the hard-stop escalation check below has not fired) `task_meta` (`hera`, `pending_recycle`) is not already `"true"`, the subcommand SHALL emit a Claude Code hook "block" decision whose reason instructs the coordinator to reach a safe seam and call for a reboot, and SHALL overwrite `task_meta` (`hera`, `last_nudged_context_size`) with the current `context_size` value. When `pending_recycle` is already `"true"`, the subcommand SHALL emit no decision, allowing the Stop event to proceed so the session can reach genuine idleness. A failure reading `pending_recycle` SHALL NOT suppress the block decision — it SHALL fall back to emitting it, treating the flag as not-yet-pending.

The increment gate: the subcommand SHALL read `task_meta` (`hera`, `last_nudged_context_size`) and the project's configured `coordinator_nudge_increment` (default 50000). When `last_nudged_context_size` is unset, OR the current `context_size` is less than `last_nudged_context_size`, the gate SHALL allow the nudge (a first-ever nudge, or a fresh over-budget episode following a drop back below budget — e.g. immediately after a recycle reset accumulated context — both nudge immediately with no increment wait). Otherwise, the gate SHALL allow the nudge only when `context_size >= last_nudged_context_size + coordinator_nudge_increment`, and SHALL suppress it otherwise. A failure reading `last_nudged_context_size` or `coordinator_nudge_increment` SHALL NOT suppress the block decision — it SHALL fall back to allowing the nudge (treating the read as if no prior nudge exists, or the increment as 0), consistent with `pending_recycle`'s existing fail-open precedent.

Because the hook re-evaluates on every Stop event rather than firing once, the nudge SHALL recur only once `context_size` has grown by at least `coordinator_nudge_increment` past the `context_size` at which it last fired — not on every subsequent turn — for as long as `context_size` remains at or above budget and `pending_recycle` is not yet `"true"`, and SHALL stop recurring the first turn either condition becomes false (context_size drops back below budget, e.g. immediately after a recycle resets accumulated context, OR `pending_recycle` becomes `"true"`, e.g. immediately after the coordinator calls `hera_status(request_recycle=true)`).

#### Scenario: Non-hera session is a no-op

- **WHEN** the Stop hook fires in a session with no `ARGUS_TASK_ID` set
- **THEN** it exits with no side effects — no `task_meta` write, no blocking decision

#### Scenario: Worker session is a no-op

- **WHEN** the Stop hook fires in a session bound to a `worker`-kind role
- **THEN** it exits with no side effects

#### Scenario: Context size is always stamped for a coordinator session

- **WHEN** the Stop hook fires in a session bound to a `coordinator`-kind role
- **THEN** `task_meta` (`hera`, `context_size`) is overwritten with the latest computed value, regardless of whether the budget is exceeded

#### Scenario: Nudge fires on the first over-budget turn

- **WHEN** a coordinator's `context_size` first reaches its configured budget and `task_meta` (`hera`, `last_nudged_context_size`) is unset
- **THEN** the Stop hook emits the reach-a-seam nudge and stamps `last_nudged_context_size` to the current `context_size`

#### Scenario: Nudge is suppressed within the same increment window

- **WHEN** a coordinator's `context_size` is at or above budget, `pending_recycle` is not yet `"true"`, and `context_size` is less than `last_nudged_context_size + coordinator_nudge_increment`
- **THEN** the Stop hook emits no block decision on that turn, and `last_nudged_context_size` is left unchanged

#### Scenario: Nudge repeats once context has grown by a full increment

- **WHEN** a coordinator's `context_size` reaches or exceeds `last_nudged_context_size + coordinator_nudge_increment`
- **THEN** the Stop hook emits the reach-a-seam nudge again and stamps `last_nudged_context_size` to the new current `context_size`

#### Scenario: Nudge fires immediately on a fresh over-budget episode

- **WHEN** a coordinator's `context_size` was previously over budget and nudged, then dropped below budget (e.g. following a recycle), and later reaches or exceeds budget again while `context_size` is still less than the stale `last_nudged_context_size` from the prior episode
- **THEN** the Stop hook emits the reach-a-seam nudge on that turn without waiting for `coordinator_nudge_increment` of growth past the stale value

#### Scenario: Nudge stops the turn context drops back below budget

- **WHEN** a coordinator's `context_size` was over budget and, on a later turn, is below budget
- **THEN** the Stop hook does not emit the nudge on that later turn

#### Scenario: Nudge does not recur once recycle is already pending

- **WHEN** a coordinator's `context_size` is at or above budget (but below the hard-stop threshold) and `task_meta` (`hera`, `pending_recycle`) is already `"true"` — the coordinator has already requested a self-service recycle
- **THEN** the Stop hook emits no block decision on that turn, even though `context_size` remains at or above budget, so the Stop event can proceed and the session can reach genuine idleness for `RecycleWatcher` to act on

### Requirement: Coordinator spawn orientation carries a context-discipline spec

The system SHALL extend `HeraCoordinatorOrientation`'s prompt text with instructions covering: a low default reasoning-effort posture with explicit escalation for genuine judgment calls; a delegation rule directing investigation-class work (reading files, understanding code, diagnosing a failure) to Claude's native sub-agent tool rather than `hera_spawn_worker`, reserving `hera_spawn_worker` for work needing its own git worktree, branch, or PR; a pointers-not-payloads convention for messages and reports (reference `path:line`, branch names, and task IDs rather than pasting full content); and a distillate-harvest step before winding down (bringing `design.md`'s open-questions/discovery-findings sections current, then recording a short `handoff_note`).

#### Scenario: Every newly spawned coordinator receives the discipline text

- **WHEN** a new coordinator role is spawned via `hera_new_orchestrator`
- **THEN** its orientation prompt includes the effort, delegation, pointers-not-payloads, and harvest-before-retire instructions

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

### Requirement: Hard-stop escalation forces a recycle at 1.5x budget

The system SHALL, within the same `argus coord-hook` Stop-hook invocation and before consulting or acting on `pending_recycle`, compare a coordinator's `context_size` against 1.5x its configured `coordinator_context_budget` (compared as `context_size * 2 >= coordinator_context_budget * 3`, avoiding floating-point arithmetic). When at or above this hard-stop threshold, the subcommand SHALL call a `Daemon.ForceRecycleCoordinator` RPC — over the daemon's existing Unix socket, identified by `ARGUS_TASK_ID` — instead of emitting a block decision, and SHALL do so regardless of whether `pending_recycle` is already `"true"`.

`Daemon.ForceRecycleCoordinator` SHALL resolve the coordinator-kind role bound to the given task ID and force a `recycle_coord` restart via its human-forced trigger (`RecycleHumanForced`) — the same idle-gate-free kill-and-restart path already used by the hera-view rail's forced-recycle keybinding (`heraDoForceRecycle`) — and SHALL return an error, with no session kill or restart performed, when no coordinator-kind role is bound to the given task (whether unbound entirely, or bound only to a worker/freelance role).

#### Scenario: Hard-stop fires at exactly 1.5x budget

- **WHEN** a coordinator's `context_size` is exactly 1.5x its configured budget
- **THEN** the Stop hook calls `Daemon.ForceRecycleCoordinator` and emits no block decision on that turn

#### Scenario: Hard-stop does not fire just under 1.5x budget

- **WHEN** a coordinator's `context_size` is one token under 1.5x its configured budget
- **THEN** the Stop hook does not call `Daemon.ForceRecycleCoordinator`, and falls back to the ordinary at/over-budget nudge logic

#### Scenario: Hard-stop fires regardless of pending_recycle

- **WHEN** a coordinator's `context_size` is at/over the hard-stop threshold and `task_meta` (`hera`, `pending_recycle`) is already `"true"`
- **THEN** the Stop hook still calls `Daemon.ForceRecycleCoordinator`

#### Scenario: ForceRecycleCoordinator rejects an unbound or non-coordinator task

- **WHEN** `Daemon.ForceRecycleCoordinator` is called for a task with no bound coordinator-kind role (unbound entirely, or bound only to a worker/freelance role)
- **THEN** it returns an error and performs no session kill or restart

