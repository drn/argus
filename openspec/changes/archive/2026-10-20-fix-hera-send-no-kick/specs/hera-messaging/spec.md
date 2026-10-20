## ADDED Requirements

### Requirement: hera_send auto-revives a dead recipient

The system SHALL, on `hera_send`, attempt a **dead-only** revive of the recipient's session before delivering the message, using `internal/hera.ReviveDeadRole`. That primitive shares `hera_revive`'s dead-session restart path (see `openspec/specs/hera-coordination/spec.md`'s "hera_revive coordinator PULL-revive of a bound role" requirement). A recipient whose session has no live process SHALL be restarted in place, resuming via `--session-id` when the task carries one. A recipient whose session is alive SHALL NOT be kicked, stopped, or restarted, regardless of whether it is idle, busy, blocked on a prompt, or a coordinator. The attempt SHALL report the `skipped_alive` outcome, because message delivery itself is what wakes a live idle agent. The attempt SHALL be made ONLY when all of: the caller's role kind is `coordinator`; the recipient was resolved via an explicit `to` argument (not the worker/freelance default-to-coordinator route); and the recipient's role id differs from the caller's own role id.

The attempt SHALL be soft-fail: it SHALL NOT block, delay meaningfully, or fail the message send under any outcome, including a recipient with no live binding, a binding-lookup error other than not-found, a revive error, or the underlying reviver not being configured. A recipient with no live binding is an expected, common case (a planned node, a role never spawned, or an ended role) and SHALL be skipped with at most an Info/Debug log. Any other lookup or revive error SHALL be skipped with a Warn log.

On a successful revive attempt, the `hera_send` tool response SHALL include a `- **revive**: <outcome>` line, rendered via the same outcome-to-message renderer `hera_revive` uses, in addition to the existing `message_id`/`to`/`delivery_mode` lines. The line SHALL be omitted when no revive attempt was made. A successful revive attempt SHALL be logged via the same `slog.Info("[hera] revive", ...)` line `hera_revive` emits.

#### Scenario: Dead recipient is restarted before the message is delivered

- **WHEN** a coordinator calls `hera_send` with an explicit `to` naming a different role whose session has no live process
- **THEN** the recipient's session is restarted in place before the send, the tool response includes `- **revive**: ` describing the restart, and the message is still delivered

#### Scenario: Live idle recipient is never kicked

- **WHEN** a coordinator calls `hera_send` with an explicit `to` naming a role whose session is alive, idle, and not parked at a prompt (for example a worker between turns, or one waiting on a background job)
- **THEN** the session is not stopped, kicked, or restarted; the tool response reports `skipped_alive`; and the message is delivered to the live session, which wakes it

#### Scenario: Busy, blocked, or live-coordinator recipient is left untouched and the send still succeeds

- **WHEN** a coordinator calls `hera_send` with an explicit `to` naming a role that is alive and actively working, alive and blocked on a prompt, or itself a live coordinator
- **THEN** no restart or kick is attempted, the tool response reports `skipped_alive`, and the message is delivered exactly as it would be without the auto-revive attempt

#### Scenario: Recipient with no live binding does not block the send

- **WHEN** a coordinator calls `hera_send` with an explicit `to` naming a role that has never been spawned, is only a planned node, or has ended
- **THEN** the auto-revive attempt is skipped silently (no more than an Info/Debug log), no `- **revive**:` line appears in the response, and the message is still stored and delivery is still attempted exactly as `hera_send` already behaves today

#### Scenario: A revive lookup or call error does not block the send

- **WHEN** the recipient's live-binding lookup fails with an error other than not-found, or the revive call itself returns an error
- **THEN** a Warn is logged, no `- **revive**:` line appears in the response, and the message send proceeds and succeeds or fails purely on its own existing merits

#### Scenario: Reviver not configured does not block the send

- **WHEN** the daemon has not wired a reviver (`s.heraRevive` is nil)
- **THEN** the auto-revive attempt is skipped entirely with no error, no `- **revive**:` line appears in the response, and `hera_send` behaves exactly as it does without auto-revive

#### Scenario: Worker/freelance default-route sends never trigger auto-revive

- **WHEN** a worker or freelance sender calls `hera_send` with no explicit `to` (defaulting to the orchestrator's active coordinator)
- **THEN** no auto-revive attempt is made regardless of the coordinator's session state, and the send proceeds exactly as it does without auto-revive

#### Scenario: A coordinator's self-send never triggers auto-revive

- **WHEN** a coordinator calls `hera_send` with an explicit `to` that resolves to its own calling role
- **THEN** no auto-revive attempt is made, and the call fails with the existing self-send validation error

## REMOVED Requirements

### Requirement: hera_send auto-revives a dead or stuck recipient

**Reason**: The "stuck" half kicked (SIGTERM + `--session-id` resume) every live, idle, unblocked recipient. That is the normal resting state of a worker between turns or one waiting on a background job, so every coordinator message restarted the recipient and killed its in-session background jobs (120 such kicks on 2026-10-18/19). Superseded by "hera_send auto-revives a dead recipient".

**Migration**: None needed. Coordinators that want to force-restart a live but truly wedged worker call `hera_revive` explicitly. Its behavior is unchanged.
