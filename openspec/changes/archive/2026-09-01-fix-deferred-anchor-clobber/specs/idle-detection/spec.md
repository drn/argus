## ADDED Requirements

### Requirement: A deferred rerender kick preserves the width the scrollback is still committed at

The rerender-kick decision SHALL consult a second, caller-tracked width anchor alongside
the session's immutable initial width, so that a mid-session bind whose kick was owed but
never taken still registers as drift on a later evaluation. Whenever a kick is owed and not taken
— the agent is busy, the agent is blocked on a user prompt, or the stop attempt fails — the
system SHALL record the evaluated panel width as that anchor, EXCEPT when an anchor is
already recorded that still differs from the evaluated panel width by at least the rerender
margin. Such an anchor names a width the session's scrollback is genuinely still committed
at and which no kick has re-emitted, so overwriting it discards the only record of the
outstanding drift and — because the replacement equals the evaluated width by construction
— makes every later evaluation at that same width report no drift, dropping the owed kick
permanently. An anchor that is within the margin of the evaluated panel width describes
effectively the same width and SHALL be replaced by the fresher value.

The anchor SHALL be cleared when a kick actually fires, because the resumed session re-emits
its scrollback at the new width and its own recorded initial width becomes the fresh
reference, and when the task is deleted.

#### Scenario: A deferred bind does not erase an outstanding wider anchor

- **GIVEN** a session whose recorded anchor is a width that differs from the current panel
  width by at least the rerender margin
- **WHEN** a kick is evaluated at the current panel width and deferred because the agent is
  busy or blocked on a prompt
- **THEN** the recorded anchor is left unchanged
- **AND** a later evaluation at that same panel width still reports drift and can kick

#### Scenario: A deferred bind refreshes an anchor that already matches

- **GIVEN** a session with no recorded anchor, or one within the rerender margin of the
  current panel width
- **WHEN** a kick is evaluated at the current panel width and deferred
- **THEN** the current panel width is recorded as the anchor

#### Scenario: A kick that fires clears the anchor

- **WHEN** a kick is actually issued for a task
- **THEN** the recorded anchor for that task is removed
