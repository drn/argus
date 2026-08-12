## Why

738 archived Hera tasks are stuck at `status=in_review` with no live binding, and it is not a bug in any one place – it is structural: nothing in the system, human or agent, has ever had a reachable way to advance a task to `complete` in normal operation. `RollHeraWorkerToReview`'s own doc comment says it plainly: it "never auto-completes... coordinators/freelance are no-ops." A `POST /api/tasks/:id/status` endpoint that accepts `"complete"` exists but nothing in the TUI or web frontend calls it (verified by grep – dead code). The only way a task has ever reached `complete` is a truly self-driven zero-exit process (near-never for an interactive Claude session) or the nuke-reclaim path (`fix-nuke-completion-race`).

Aaron (the human operator) specified the lifecycle he wants: a worker reporting "I'm done" already lands at `in_review` (`hera_status(done)` → `RollHeraWorkerToReview`, unchanged). What's missing is the other half – the coordinator's explicit "I accept this" – which is the only thing that should ever advance a task to `complete`. A merged PR is a strong signal but must never auto-complete on its own (a nudge only, given how ambiguous "confirmed merged" gets for Hera-descended tasks – squash merges, folded-in workers with no PR).

## What Changes

- New coordinator-only `hera_accept` MCP tool: flips a bound role's task to `complete` (via the existing, idempotent `db.SetStatus`) and sends the role a closed-loop check-in asking it to reply confirming it's winding down, telling the coordinator it has more work, or asking a question – the reply never auto-reopens the task; never stops or restarts the agent's session.
- The plan-DAG gater (`internal/heragater`) auto-fires the same accept-equivalent for every blocker of a node it just materialized – "the coordinator rolling forward to the next item" is exactly this trigger. Best-effort: a failure never blocks materialization; idempotent against an already-complete task.
- `ReviveHeraWorkerToInProgress` additionally accepts `complete` as a valid source state (previously `in_review` only) – an accepted-then-stopped task can be revived back to `in_progress` later, making completion revocable.
- The Hera rail's `a` (HIDE) key now also stops the role's live session, but ONLY on the hide direction (never on un-hide), and never touches the worktree/branch – archiving stays otherwise non-destructive and reversible.
- The PR-status poller nudges the task's Hera coordinator (a message only, no status change) the one time a tracked task's PR state transitions to `merged`. Silent no-op for non-Hera tasks or when no coordinator resolves.
- Pressing `Enter` on a dead-session worker/freelance row now refuses to restart it when the task is awaiting coordinator close-out (`ready_to_close`, or a terminal role-status) – found via live-testing `hera_accept`, since `startSession` previously ran unconditionally and silently undid the close-out once the resumed-then-exited session rolled the task back to `in_review`. Coordinators are unaffected.
- The Hera roster/rail now renders a coordinator-accepted (`task.Status=complete`) worker distinctly from a merely self-reported `ready_to_close` one – a bold checkmark and an `"accepted"` label, ranked above the self-reported ladder but below needs-input/active.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `hera-coordination`: adds the `hera_accept` MCP tool (18 → 19 native tools), extends `ReviveHeraWorkerToInProgress` to accept `complete` as a source state, and refuses `Enter`'s dead-session restart for a worker/freelance task that is awaiting close-out.
- `task-orchestration`: the plan-DAG gater additionally auto-accepts every blocker of a node it materializes.
- `hera-view`: the `a` (HIDE) key additionally stops the role's live session on the hide direction only; the roster/rail renders a coordinator-accepted worker distinctly from a merely self-reported ready_to_close one.
- `pr-status`: a genuine PR-state transition to `merged` nudges the task's Hera coordinator, when one resolves.

## Impact

- `internal/hera/accept.go` (new): shared `AcceptRole` primitive – the single implementation both `hera_accept` and the gater call.
- `internal/mcp/hera.go`, `internal/mcp/server.go`: new `hera_accept` tool definition, handler, and dispatch case; `HeraStore` interface gains `Get`/`SetStatus`.
- `internal/heragater/heragater.go`: new `Accepter` hook + `SetAccepter`, fired after a successful `materializeNode`.
- `internal/daemon/daemon.go`: wires the gater's `Accepter` to `hera.AcceptRole`; PR-poller nudge on a genuine merge transition.
- `internal/db/hera.go`: `ReviveHeraWorkerToInProgress` accepts `complete` as a source status.
- `internal/tui/hera/ops.go`, `internal/tui/heraactions.go`: `ArchiveToggle` reports which direction fired; `heraHide` stops the session (backgrounded) only on hide.
- `internal/db/hera.go`: `heraWorkerAwaitingCloseout` exported as `HeraWorkerAwaitingCloseout` so the TUI can reuse it; `internal/tui/heraactions.go`: `heraReattach`'s dead-session branch consults it (via new helper `heraTaskClosedOut`) for worker/freelance selections before calling `startSession`.
- `internal/tui/widget/rolestatusicon.go`: new `RoleStatusInputs.Accepted` field + precedence rung in `RoleStatusIcon`; `internal/tui/hera/rail.go` (`roleStatusInputs`) and `internal/tui/hera/details.go` (`rosterStatusText`) both wire/consume it.
- No schema change, no new MCP tool beyond `hera_accept`, no change to `RollHeraWorkerToReview`/`RollHeraWorkerFailed` or the existing `in_review` semantics.
