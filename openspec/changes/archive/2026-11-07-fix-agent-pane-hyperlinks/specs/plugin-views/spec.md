## ADDED Requirements

### Requirement: Plugin-view hyperlinks reach the host terminal

The plugin-view terminal pane SHALL map a cell's OSC-8 hyperlink to the painted tcell style the same way the agent pane does: the hyperlink's URL, plus its `id=` param when present, so plugin-printed links (PR references, artifact links) are clickable with the host terminal's own gesture.

#### Scenario: Plugin hyperlink cell carries its URL

- **WHEN** the plugin-view pane paints a cell the emulator recorded as part of a hyperlink with URL `https://example.com/pr/1` and params `id=pr1`
- **THEN** the painted style SHALL carry URL `https://example.com/pr/1` and URL id `id=pr1`

#### Scenario: Plain plugin cell carries no URL

- **WHEN** the plugin-view pane paints a cell with no hyperlink
- **THEN** the painted style SHALL carry no URL
