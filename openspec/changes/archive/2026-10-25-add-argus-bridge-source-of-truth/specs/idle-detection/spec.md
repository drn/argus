## ADDED Requirements

### Requirement: Live Claude bridge overrides terminal inference

For live Claude bridges, the daemon and daemon-connected TUI SHALL use bridge turn and dialog state for idle, needs-input, notifications, task list and Hera spinner gates. Quiescent idle SHALL mean not busy or awaiting input. Scraper sustained-active, settling and escalation behavior SHALL remain intact for non-Claude, stale or missing bridges and the in-process runner. The comparator SHALL continue comparing raw scraper verdicts against live bridge state.

Only uncertain permission needs-input SHALL require raw scraper corroboration because its bridge interval includes execution after approval. Live bridge busy state SHALL remain authoritative throughout; exact permission, question and plan dialogs SHALL be authoritative without corroboration. Subagent dialogs SHALL count as needs-input without changing main-loop busy state.

#### Scenario: Permission approved while tool still runs

- **WHEN** a permission-kind call remains pending after approval
- **THEN** bridge busy remains authoritative and only uncertain permission needs-input is ANDed with raw scraper needs-input until the call clears

#### Scenario: Answered dialog remains in the terminal tail

- **WHEN** a live bridge reports cleared while old dialog text remains in the PTY
- **THEN** needs-input clears on all daemon-fed surfaces and remains cleared

#### Scenario: In-process TUI

- **WHEN** the TUI has no daemon connection
- **THEN** its existing scraper behavior remains in effect

#### Scenario: Master switch disabled while mods still report

- **WHEN** bridge.enabled is false, including after a live bridge reported a dialog
- **THEN** daemon REST/watchers and TUI restore the existing scraper idle and needs-input verdicts, retaining raw scraper state separately from bridge overrides and ignoring continuing bridge posts for authority

