# Idle / Needs-Input Detection

## ADDED Requirements

### Requirement: A live Hera role surfaces needs-input regardless of bound-task status

The system SHALL surface the detected needs-input `(?)` indicator on the Hera rail for ANY role that holds a LIVE binding (worker, coordinator, or freelance) whenever its bound task is in the content-aware needs-input set, REGARDLESS of that task's workflow status. A hera worker deliberately remains in `in_review` (with `meta:hera.ready_to_close` set) while its session lingers alive for the coordinator to close it out; rolling a worker to `in_review` keeps its binding live and never touches the session, so a still-alive worker can genuinely block on a prompt in that state and MUST surface `(?)` then.

The worker `in_progress` gate that previously suppressed this MUST NOT be applied to a live role. The needs-input set is content-aware (a task is flagged only while it shows a CURRENT awaiting-input signal and clears on user input or archive), so it already distinguishes "live at a real prompt" from "idling at a stale done summary" — the task-status gate is not needed to suppress stale markers.

The pre-existing protection against a FINISHED worker pinning `(?)` forever on every ancestor (the rollup never clearing) SHALL be preserved via LIVENESS, not task status: a worker is finished when its SESSION EXITS, which ENDS its binding, dropping the role from the live-binding branch so it no longer surfaces or rolls up `(?)`. A worker idling at a done summary with no interactive affordance is additionally never in the content-aware set.

The flat task-list `(?)` indicator (the always-visible, non-tree surface) is OUT of scope of this requirement and remains gated on `in_progress`; only the Hera rail (the orchestration tree, where a role's liveness is the meaningful signal) surfaces a live non-`in_progress` role.

#### Scenario: Live worker in in_review at a prompt surfaces (?)

- **WHEN** a hera worker's bound task has rolled to `in_review` (its binding still live) and it is in the content-aware needs-input set
- **THEN** the system surfaces the needs-input `(?)` on the worker's row and rolls it up to the ancestor coordinator

#### Scenario: Exited worker does not surface (?) even while flagged

- **WHEN** a hera worker's session has exited (its binding ended) but its task still lingers in the needs-input set
- **THEN** the system does NOT surface `(?)` on the worker's row or in the ancestor coordinator's rollup

#### Scenario: Hera rail feed admits a hera-bound task regardless of status

- **WHEN** building the Hera rail needs-input feed from the sticky set
- **THEN** the system keeps any task that is `in_progress` OR is bound to any hera role (worker or coordinator), and drops a task that is neither

### Requirement: An actively-blocked role's needs-input outranks the ready-to-close glyph

The shared role status-icon classifier SHALL rank the needs-input indicator ABOVE the `ready_to_close` review glyph. A worker stamped `ready_to_close` by the done-roll that is ALSO genuinely blocked on a user prompt is not "ready to close" — the actionable `(?)` MUST be shown so the user is not misled into closing out a worker that is waiting on them. Because needs-input is content-aware upstream, a `ready_to_close` worker merely idling at its done summary (no interactive affordance) is never flagged and still renders the review glyph. This precedence is applied identically by the rail and the plan-view node projection (the single shared classifier).

#### Scenario: Ready-to-close worker at a prompt shows the needs-input glyph

- **WHEN** a role is stamped `ready_to_close` AND its needs-input signal (own or subtree rollup) is set
- **THEN** the status icon renders the needs-input glyph, not the review glyph

#### Scenario: Ready-to-close worker not blocked shows the review glyph

- **WHEN** a role is stamped `ready_to_close` and has no needs-input signal
- **THEN** the status icon renders the review glyph
