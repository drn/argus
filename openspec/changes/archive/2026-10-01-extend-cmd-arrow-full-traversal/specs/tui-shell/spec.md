## MODIFIED Requirements

### Requirement: Top-level tab navigation

The shell SHALL expose three top-level tabs in order — Tasks, Hera, Settings — and SHALL switch among them in response to global numeric keys. Tab switching SHALL update both the header's active tab and the status bar's tab context, and SHALL move focus to the newly activated view. Plain Left/Right SHALL remain available to the active view rather than switching tabs. When the persisted `ui.cross_tab_arrows` option is enabled (opt-in; default disabled), modified Left/Right (`ModCtrl` or `ModAlt`, representing Cmd+arrow) SHALL traverse one continuous chain: Tasks list / agent view (unzoomed or zoomed) ⇄ Hera rail ⇄ Hera coordinator pane ⇄ Hera agent pane ⇄ Settings left pane ⇄ Settings right pane. Hops that enter a view from its left neighbor SHALL focus its leftmost pane; hops that enter from its right neighbor SHALL focus its rightmost present pane. Cmd+Left on Tasks and Cmd+Right on the Settings right pane SHALL be the chain's ends; the latter SHALL be consumed so it cannot alter a setting. The chain SHALL NOT fire in any modal mode or while the agent view's diff view is open, while the Tasks list, Hera rail, or a Settings inline edit holds the keyboard, or while a Hera pane is fullscreen. The Tasks tab's agent view (unzoomed or zoomed) SHALL participate when the option is enabled: the agent-pane-right action (default Cmd+Right) from the rightmost visible pane (files pane unzoomed, the terminal when zoomed) SHALL switch to Hera with its rail focused without stopping or restarting the session, and modified Left from that rail SHALL restore that task's agent view with its prior zoom state, landing on its rightmost visible pane. Without a preceding agent-view hop, modified Left from the rail returns to the task list. The unzoomed terminal-to-files step and all agent-view behavior with the option disabled SHALL be unchanged.

The active tab SHALL persist locally across an argus restart and SHALL be restored on launch in place of always defaulting to Tasks, so a user who quits while on the Hera or Settings tab reopens on that same tab. In `--remote` mode, where there is no local persistence seam, the active tab SHALL NOT persist and the shell SHALL always start on the Tasks tab.

#### Scenario: Numeric keys select tabs by position

- **WHEN** the user presses `1`, `2`, or `3` while not in the agent view
- **THEN** the active tab becomes Tasks, Hera, or Settings respectively and that view is shown with focus

#### Scenario: Plain arrows remain local to the active view

- **WHEN** the user presses unmodified Left or Right in a top-level view
- **THEN** the shell does not switch tabs and the active view may handle the key

#### Scenario: Cmd+Right enters Hera from Tasks

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right on the Tasks list while it is not filtering
- **THEN** Hera becomes active with its rail focused

#### Scenario: Cmd+Left returns to Tasks from the Hera rail

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Left while Hera's rail is focused and not filtering
- **THEN** Tasks becomes active with its list focused

#### Scenario: Zoomed agent view hops to Hera

- **WHEN** `ui.cross_tab_arrows` is enabled, the agent view is zoomed, and the user presses the agent-pane-right chord
- **THEN** Hera becomes active with its rail focused and the agent session keeps running

#### Scenario: Unzoomed agent view hops from the files pane only

- **WHEN** `ui.cross_tab_arrows` is enabled and the agent view is unzoomed
- **THEN** the chord moves the terminal to the files pane first, and from the files pane switches to Hera

#### Scenario: Return trip restores the agent view

- **WHEN** the user hopped from an agent view to Hera and presses modified Left on the Hera rail
- **THEN** that task's agent view is restored with its prior zoom state, focused on the files pane (unzoomed) or the terminal (zoomed), and the session is still attached

#### Scenario: Agent view with the option disabled is unchanged

- **WHEN** `ui.cross_tab_arrows` is disabled and the agent-pane-right chord is pressed in the agent view
- **THEN** behavior is exactly as before: terminal to files when unzoomed, a no-op otherwise

#### Scenario: Cmd+Right from Hera's rightmost pane enters Settings

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right while Hera's agent pane is focused (or its coordinator pane when the agent pane is absent) and no pane is fullscreen
- **THEN** Settings becomes active with its left pane focused

#### Scenario: Cmd+Left from Settings' left pane returns to Hera's rightmost pane

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Left while Settings' left pane is focused and not editing
- **THEN** Hera becomes active with its agent pane focused, or its coordinator pane when the agent pane is absent

#### Scenario: Settings panes step left and right

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right on Settings' left pane, or modified Left on its right pane
- **THEN** focus moves to the right or left pane respectively and the tab does not change

#### Scenario: Cmd+Right at the end of the chain is a no-op

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right while Settings' right pane is focused
- **THEN** nothing changes and the focused setting is not modified

#### Scenario: Disabled option preserves current behavior

- **WHEN** `ui.cross_tab_arrows` is disabled and the user presses any chain chord
- **THEN** the shell does not switch tabs or panes through the chain and the event continues to the active view

#### Scenario: Hera internal ladder is unchanged

- **WHEN** `ui.cross_tab_arrows` is enabled and modified Left or Right is pressed while a Hera pane is focused and the key is not a chain hop
- **THEN** the Hera focus ladder handles the key and the active tab does not change

#### Scenario: Fullscreen suppresses the chain

- **WHEN** `ui.cross_tab_arrows` is enabled and a Hera pane is fullscreen
- **THEN** modified Left/Right behave exactly as they do without the chain and the active tab does not change

#### Scenario: Filters and inline edits suppress the chain

- **WHEN** `ui.cross_tab_arrows` is enabled and the Tasks list or Hera rail is filtering, or a Settings inline edit is active
- **THEN** the active tab and focus do not change and the filter or edit field keeps control of the key

#### Scenario: Switching to a tab refreshes its content

- **WHEN** the user switches to the Hera tab or the Settings tab
- **THEN** the shell refreshes that view's content before showing it

#### Scenario: Last active tab is restored on launch

- **WHEN** the user quits argus while the Hera tab is active and then relaunches argus
- **THEN** the shell opens directly on the Hera tab, refreshed, with focus moved to it — the same outcome pressing `2` would produce

#### Scenario: First run with no persisted tab defaults to Tasks

- **WHEN** no tab state has ever been persisted (first run) or argus is running in `--remote` mode
- **THEN** the shell opens on the Tasks tab
