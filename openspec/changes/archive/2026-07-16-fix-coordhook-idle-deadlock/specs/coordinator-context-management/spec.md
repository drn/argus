## MODIFIED Requirements

### Requirement: Context-budget Stop hook stamps a live signal and nudges over budget

The system SHALL provide an `argus coord-hook` subcommand, invoked as a Claude Code `Stop` hook, that on every invocation first checks for a non-empty `ARGUS_TASK_ID` and a resolved `coordinator`-kind role; it SHALL no-op with no side effects and no error when either check fails. When both checks pass, the subcommand SHALL: (1) read the transcript at the hook's supplied `transcript_path` and extract the latest assistant message's `usage.cache_read_input_tokens`; (2) self-discover the daemon's live REST port and `~/.argus/api-token` (no port/token is exported into the spawned session's environment); (3) overwrite `task_meta` under namespace `hera`, key `context_size`, with the extracted value; and (4) compare the value against the project's configured `coordinator_context_budget` — when the value is at or above the budget AND (after the hard-stop escalation check below has not fired) `task_meta` (`hera`, `pending_recycle`) is not already `"true"`, the subcommand SHALL emit a Claude Code hook "block" decision whose reason instructs the coordinator to reach a safe seam and call for a reboot. When `pending_recycle` is already `"true"`, the subcommand SHALL emit no decision, allowing the Stop event to proceed so the session can reach genuine idleness. A failure reading `pending_recycle` SHALL NOT suppress the block decision — it SHALL fall back to emitting it, treating the flag as not-yet-pending.

Because the hook re-evaluates on every Stop event rather than firing once, the nudge SHALL recur on every subsequent turn for as long as `context_size` remains at or above budget and `pending_recycle` is not yet `"true"`, and SHALL stop recurring the first turn either condition becomes false (context_size drops back below budget, e.g. immediately after a recycle resets accumulated context, OR `pending_recycle` becomes `"true"`, e.g. immediately after the coordinator calls `hera_status(request_recycle=true)`).

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

- **WHEN** a coordinator's `context_size` is at or above its configured budget, and `pending_recycle` is not yet `"true"`, on two consecutive turns
- **THEN** the Stop hook emits the reach-a-seam nudge on both turns

#### Scenario: Nudge stops the turn context drops back below budget

- **WHEN** a coordinator's `context_size` was over budget and, on a later turn, is below budget
- **THEN** the Stop hook does not emit the nudge on that later turn

#### Scenario: Nudge does not recur once recycle is already pending

- **WHEN** a coordinator's `context_size` is at or above budget (but below the hard-stop threshold) and `task_meta` (`hera`, `pending_recycle`) is already `"true"` — the coordinator has already requested a self-service recycle
- **THEN** the Stop hook emits no block decision on that turn, even though `context_size` remains at or above budget, so the Stop event can proceed and the session can reach genuine idleness for `RecycleWatcher` to act on

## ADDED Requirements

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
