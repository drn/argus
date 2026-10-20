## Context

`internal/hera.ReviveRole` decides, in order: dead → restart; live coordinator → skip; no session id → skip; kick pending → skip; busy → skip; blocked on prompt → skip; otherwise → kick (`agent.KickRevive`, a `StopAgentOnly` SIGTERM of the agent PID followed by a `--session-id` resume). `hera_revive` (explicit, coordinator-only) and `hera_send`'s auto-revive both call it.

The "otherwise → kick" branch assumes that an idle, unblocked, live session is wedged. It usually isn't. `Session.IsIdle` is just "no PTY output for 3s", which is true of every agent sitting at its prompt between turns and of every agent waiting on a background job. Argus doesn't signal the agent's descendants on a `StopAgentOnly` stop, but the agent's own shutdown does: Claude Code tears down its `run_in_background` shells when it exits. Jobs detached with `nohup … &` from a foreground shell are reparented away and survive.

## Goals / Non-Goals

**Goals:**

- A coordinator message never restarts a live recipient.
- A dead recipient is still restarted on send (the useful half of auto-revive).
- Explicit `hera_revive`'s cost is documented where coordinators read it.

**Non-Goals:**

- No change to explicit `hera_revive`'s gating or outcomes.
- No guard that detects a live background job before an explicit kick. Telling the agent's own job processes apart from its stdio MCP servers in the descendant tree is fiddly. Recorded as a follow-up in `context/knowledge/gotchas/messaging.md`.
- No change to the TUI's Enter-key revive (operator-initiated, a deliberate keypress).

## Decisions

**D1: Dead-only send revive via a sibling primitive, not a flag threaded into `ReviveRole`.** `ReviveDeadRole(store, runner, taskID)` shares `ReviveRole`'s task-load and dead-restart branch (factored into an unexported helper) and returns `ReviveSkippedAlive` for any live session. Keeping `ReviveRole`'s signature unchanged leaves `hera_revive` byte-identical. A coordinator recipient needs no special case on the send path, because a live coordinator is just "alive".

**D2: `HeraReviveInput.DeadOnly` selects the primitive.** The MCP layer stays independent of `internal/hera` types (HeraSpawner precedent). The daemon's dispatch lives in a small `reviveForInput` helper in `internal/daemon/revive.go` so that it is unit-testable without constructing a full `Daemon`.

**D3: Report `skipped_alive` rather than omit the revive line.** The coordinator still learns from one response whether the recipient was dead (restarted) or alive (the message will wake it). This matches the existing "every path is reported" contract.

## Risks / Trade-offs

- **[Risk] A genuinely wedged live worker (composer not accepting input) no longer gets unstuck by a send.** → The reliable-notify path already retries and acknowledges delivery. A coordinator that sees no response can still call `hera_revive` explicitly, and the skill now frames that as the last resort it is.
