## MODIFIED Requirements

### Requirement: Top-level tab navigation

The shell SHALL expose three top-level tabs in order — Tasks, Hera, Settings — and SHALL switch among them in response to global numeric keys. Tab switching SHALL update both the header's active tab and the status bar's tab context, and SHALL move focus to the newly activated view. Plain Left/Right SHALL remain available to the active view rather than switching tabs. When the persisted `ui.cross_tab_arrows` option is enabled, modified Right (`ModCtrl` or `ModAlt`, representing Cmd+Right) from the unfiltered Tasks list SHALL activate Hera with its rail focused, and modified Left from Hera's unfiltered focused rail SHALL activate Tasks with its list focused. The optional boundary shortcut SHALL NOT apply from a Hera content pane, while either list is filtering, from the Settings tab, or in agent view.

#### Scenario: Numeric keys select tabs by position

- **WHEN** the user presses `1`, `2`, or `3` while not in the agent view
- **THEN** the active tab becomes Tasks, Hera, or Settings respectively and that view is shown with focus

#### Scenario: Plain arrows remain local to the active view

- **WHEN** the user presses unmodified Left or Right in a top-level view
- **THEN** the shell does not switch tabs and the active view may handle the key

#### Scenario: Enabled Cmd+Right enters Hera from Tasks

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Right on the Tasks list while it is not filtering
- **THEN** Hera becomes active with its rail focused

#### Scenario: Enabled Cmd+Left returns to Tasks from the Hera rail

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses modified Left while Hera's rail is focused and not filtering
- **THEN** Tasks becomes active with its list focused

#### Scenario: Disabled option preserves current boundary behavior

- **WHEN** `ui.cross_tab_arrows` is disabled and the user presses either boundary chord
- **THEN** the shell does not switch tabs and the event continues to the active view

#### Scenario: Hera pane arrows retain the focus ladder

- **WHEN** `ui.cross_tab_arrows` is enabled and modified Left or Right is pressed while a Hera coordinator or agent pane is focused
- **THEN** the Hera focus ladder handles the key and the active tab does not change

#### Scenario: Filtering suppresses boundary navigation

- **WHEN** `ui.cross_tab_arrows` is enabled and the user presses a boundary chord while the Tasks list or Hera rail is filtering
- **THEN** the active tab does not change and the filter keeps control of the key

#### Scenario: Switching to a tab refreshes its content

- **WHEN** the user switches to the Hera tab or the Settings tab
- **THEN** the shell refreshes that view's content before showing it
