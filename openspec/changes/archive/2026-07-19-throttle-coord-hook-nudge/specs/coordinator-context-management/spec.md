## MODIFIED Requirements

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
