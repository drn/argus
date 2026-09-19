## MODIFIED Requirements

### Requirement: Top-level tab navigation

The shell SHALL expose three top-level tabs in order — Tasks, Hera, Settings — and SHALL switch among them in response to global keys. Tab switching SHALL update both the header's active tab and the status bar's tab context, and SHALL move focus to the newly activated view.

The active tab SHALL persist locally across an argus restart and SHALL be restored on launch in place of always defaulting to Tasks, so a user who quits while on the Hera or Settings tab reopens on that same tab. In `--remote` mode, where there is no local persistence seam, the active tab SHALL NOT persist and the shell SHALL always start on the Tasks tab.

#### Scenario: Numeric keys select tabs by position

- **WHEN** the user presses `1`, `2`, or `3` while not in the agent view
- **THEN** the active tab becomes Tasks, Hera, or Settings respectively and that view is shown with focus

#### Scenario: Arrow keys step between adjacent tabs

- **WHEN** the user presses Left or Right while not in the agent view and the active view does not itself consume the key
- **THEN** the active tab moves to the previous or next adjacent tab, clamped at the Tasks and Settings ends

#### Scenario: Switching to a tab refreshes its content

- **WHEN** the user switches to the Hera tab or the Settings tab
- **THEN** the shell refreshes that view's content before showing it

#### Scenario: Last active tab is restored on launch

- **WHEN** the user quits argus while the Hera tab is active and then relaunches argus
- **THEN** the shell opens directly on the Hera tab, refreshed, with focus moved to it — the same outcome pressing `2` would produce

#### Scenario: First run with no persisted tab defaults to Tasks

- **WHEN** no tab state has ever been persisted (first run) or argus is running in `--remote` mode
- **THEN** the shell opens on the Tasks tab
