## MODIFIED Requirements

### Requirement: TUI copy of a staged payload

In the TUI, the staged-clipboard copy keybinding SHALL always be intercepted
and SHALL NOT fall through to normal terminal handling, whether or not a
payload is staged. If a payload is staged, the copy action SHALL copy the
cached staged payload to the OS clipboard, clear the task's staged state, and
flash a confirmation notice. If no payload is staged, the action SHALL flash a
notice indicating there is nothing to copy, and SHALL NOT forward the keypress
to the underlying terminal. Local staged state and the on-screen hint SHALL be
cleared immediately on a successful copy, and the underlying clear of the
task's staged payload SHALL proceed independently; a failure to clear SHALL be
logged without disrupting the copy. If the OS clipboard write fails, no
confirmation notice SHALL be shown.

#### Scenario: Nothing staged is intercepted with a notice

- **WHEN** the copy action runs with no staged payload cached
- **THEN** it reports that nothing was copied and a "nothing to copy" notice
  is shown instead of forwarding the keypress to the terminal

#### Scenario: Copy clears local state and the staged payload

- **WHEN** the copy action runs with a staged payload cached for a task
- **THEN** it reports a successful copy, immediately clears the cached payload
  and the on-screen hint, and clears the task's staged payload

#### Scenario: Clear failure does not disrupt copy

- **WHEN** clearing the task's staged payload fails after a copy
- **THEN** the failure is logged and the copy still reports success without
  panicking

#### Scenario: OS write failure suppresses the notice

- **WHEN** the OS clipboard write returns an error during a copy
- **THEN** no success callback fires and no confirmation notice is shown
