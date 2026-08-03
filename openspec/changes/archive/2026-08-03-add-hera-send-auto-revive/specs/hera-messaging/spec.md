## ADDED Requirements

### Requirement: hera_send auto-revives a dead or stuck recipient

The system SHALL, on `hera_send`, attempt to revive the recipient's live session before delivering the message, using the exact same `internal/hera.ReviveRole` primitive and outcome set as `hera_revive` (see `openspec/specs/hera-coordination/spec.md`'s "hera_revive coordinator PULL-revive of a bound role" requirement) — no new gating logic is introduced. The attempt SHALL be made ONLY when all of: the caller's role kind is `coordinator`; the recipient was resolved via an explicit `to` argument (not the worker/freelance default-to-coordinator route); and the recipient's role id differs from the caller's own role id.

The attempt SHALL be soft-fail: it SHALL NOT block, delay meaningfully, or fail the message send under any outcome, including a recipient with no live binding, a binding-lookup error other than not-found, a revive error, or `hera_revive`'s underlying reviver not being configured. A recipient with no live binding is an expected, common case (a planned node, a role never spawned, or an ended role) and SHALL be skipped with at most an Info/Debug log; any other lookup or revive error SHALL be skipped with a Warn log.

On a successful revive attempt, the `hera_send` tool response SHALL include a `- **revive**: <outcome>` line rendered via the same outcome-to-message renderer `hera_revive` uses, in addition to the existing `message_id`/`to`/`delivery_mode` lines. The line SHALL be omitted when no revive attempt was made. A successful revive attempt SHALL be logged via the same `slog.Info("[hera] revive", ...)` line `hera_revive` emits, so an auto-triggered revive is indistinguishable in logs from a manual `hera_revive` call.

#### Scenario: Dead recipient is restarted before the message is delivered

- **WHEN** a coordinator calls `hera_send` with an explicit `to` naming a different role whose session has no live process
- **THEN** the recipient's session is restarted in place before the send, the tool response includes `- **revive**: ` describing the restart, and the message is still delivered

#### Scenario: Busy, blocked, or live-coordinator recipient is left untouched and the send still succeeds

- **WHEN** a coordinator calls `hera_send` with an explicit `to` naming a role that is alive and actively working, alive and blocked on a prompt, or itself a live coordinator
- **THEN** no restart or kick is attempted, the tool response reports the corresponding skip outcome, and the message is delivered exactly as it would be without the auto-revive attempt

#### Scenario: Recipient with no live binding does not block the send

- **WHEN** a coordinator calls `hera_send` with an explicit `to` naming a role that has never been spawned, is only a planned node, or has ended
- **THEN** the auto-revive attempt is skipped silently (no more than an Info/Debug log), no `- **revive**:` line appears in the response, and the message is still stored and delivery is still attempted exactly as `hera_send` already behaves today

#### Scenario: A revive lookup or call error does not block the send

- **WHEN** the recipient's live-binding lookup fails with an error other than not-found, or the revive call itself returns an error
- **THEN** a Warn is logged, no `- **revive**:` line appears in the response, and the message send proceeds and succeeds or fails purely on its own existing merits

#### Scenario: Reviver not configured does not block the send

- **WHEN** the daemon has not wired a `hera_revive` reviver (`s.heraRevive` is nil)
- **THEN** the auto-revive attempt is skipped entirely with no error, no `- **revive**:` line appears in the response, and `hera_send` behaves exactly as it did before this change

#### Scenario: Worker/freelance default-route sends never trigger auto-revive

- **WHEN** a worker or freelance sender calls `hera_send` with no explicit `to` (defaulting to the orchestrator's active coordinator)
- **THEN** no auto-revive attempt is made regardless of the coordinator's session state, and the send proceeds exactly as it did before this change

#### Scenario: A coordinator's self-send never triggers auto-revive

- **WHEN** a coordinator calls `hera_send` with an explicit `to` that resolves to its own calling role
- **THEN** no auto-revive attempt is made, and the call fails with the existing self-send validation error exactly as it did before this change
