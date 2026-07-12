# Idle / Needs-Input Detection

## ADDED Requirements

### Requirement: An actively-producing role's working spinner outranks the ready-to-close and done/failed glyphs

The shared role status-icon classifier SHALL rank the working spinner (the `active` signal) ABOVE the stale-able resting glyphs `ready_to_close`, `failed`, and `done`, and BELOW needs-input. A worker stamped `ready_to_close` by the done-roll (or carrying a stale `done` / `failed` hera role-status) that is ALSO genuinely producing output — a live binding whose session is running and not idle (`RoleView.IsActive`, BUG-C) — is working again, so the animated spinner MUST be shown, not the static review / ✓ / ✕ glyph. The `active` signal is the HONEST, content-derived "producing output right now" signal, NOT a stale hera role-status/meta, so it is the truer current state and must not be masked by the done-roll's stamp.

Because `active` drops to false the moment the session goes idle or exits (the session-running / not-session-idle gate), the resting glyph correctly returns when the worker stops producing — so the close-out / done / failed resting states are preserved for a quiet worker. A `ready_to_close` worker merely idling at its done summary (not active) still renders the review glyph.

needs-input remains highest: a worker blocked on a user prompt is more urgent than one merely producing output (and the two are mutually exclusive in practice). This precedence is applied identically by the rail and the plan-view node projection (the single shared classifier), so a plan-view node for an actively-producing worker also animates.

The full precedence is: needs-input → active(spinner) → ready_to_close → failed(red ✕) → done → idle → live → default.

#### Scenario: Reactivated ready-to-close worker producing output shows the spinner

- **WHEN** a role is stamped `ready_to_close` AND its `active` signal is set (live binding, running session, not idle — producing output)
- **THEN** the status icon renders the animated working spinner, advancing with the frame counter, not the review glyph

#### Scenario: Ready-to-close worker not producing shows the review glyph

- **WHEN** a role is stamped `ready_to_close` and is NOT active (its session is idle or has exited)
- **THEN** the status icon renders the review glyph (the resting close-out state is preserved)

#### Scenario: Active outranks a stale done or failed role-status

- **WHEN** a role carries a stale `done` or `failed` hera role-status AND its `active` signal is set (producing output again)
- **THEN** the status icon renders the working spinner, not the done ✓ or failed ✕

#### Scenario: Needs-input still outranks the active spinner

- **WHEN** a role is blocked on a user prompt (needs-input set) AND its `active` signal is also set
- **THEN** the status icon renders the needs-input glyph, not the spinner
