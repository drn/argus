## MODIFIED Requirements

### Requirement: TUI copy of a staged payload

In the TUI, the staged-clipboard copy keybinding SHALL always be intercepted
and SHALL NOT fall through to normal terminal handling, whether or not a
payload is staged. If a payload is staged, the copy action SHALL copy the
cached staged payload to the OS clipboard and flash a confirmation notice,
and SHALL NOT clear the task's staged payload as a side effect of copying —
the staged payload SHALL remain available for a subsequent copy. If no
payload is staged, the action SHALL flash a notice indicating there is
nothing to copy, and SHALL NOT forward the keypress to the underlying
terminal. If the OS clipboard write fails, no confirmation notice SHALL be
shown. The staged payload SHALL be affected only by its existing lifecycle
(time-to-live expiry, replacement by a newer staged value, or the owning
agent session exiting) — never by the copy action itself.

#### Scenario: Nothing staged is intercepted with a notice

- **WHEN** the copy action runs with no staged payload cached
- **THEN** it reports that nothing was copied and a "nothing to copy" notice
  is shown instead of forwarding the keypress to the terminal

#### Scenario: Copy preserves the staged payload

- **WHEN** the copy action runs with a staged payload cached for a task
- **THEN** it reports a successful copy, the cached payload and the on-screen
  hint remain unchanged, and the task's staged payload is left intact

#### Scenario: Copying twice in a row both succeed

- **WHEN** the copy action is run twice in immediate succession with the same
  staged payload and nothing else changes the staged state in between
- **THEN** both runs report a successful copy of the same text, and neither
  run flashes "nothing to copy"

#### Scenario: OS write failure suppresses the notice

- **WHEN** the OS clipboard write returns an error during a copy
- **THEN** no success callback fires and no confirmation notice is shown

### Requirement: TUI staged-payload hint tracking

The TUI SHALL track the currently staged payload for the active task by
polling the staging source and SHALL show a hint affordance only while a
payload is present. The hint SHALL render with a color that visibly
distinguishes it from the surrounding chrome text, so its presence is
noticeable rather than blending in. When the staging source is unavailable
(for example, when the TUI runs without a daemon-backed runner), tracking
SHALL be a no-op and no hint SHALL be shown. When a previously present
payload becomes absent, the tracked payload SHALL be cleared and the hint
hidden.

#### Scenario: No staging source available

- **WHEN** the staging source is not available and the cache is refreshed
- **THEN** the tracked payload stays empty and no hint is shown

#### Scenario: Present payload shows a visibly distinct hint

- **WHEN** the staging source reports a present payload for the active task
  and the cache is refreshed
- **THEN** the tracked payload is updated to that text, the hint is shown, and
  it renders in a color distinct from the surrounding header/border-title text

#### Scenario: Absent payload hides the hint

- **WHEN** a previously present payload becomes absent and the cache is
  refreshed
- **THEN** the tracked payload is cleared and the hint is hidden
