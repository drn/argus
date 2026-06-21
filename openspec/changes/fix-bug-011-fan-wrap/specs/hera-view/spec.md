# Hera View

## MODIFIED Requirements

### Requirement: Fanned group visually expands into member boxes (area 6)

When a parallel group is fanned out, the diagram SHALL render its members as individual node boxes inside a SOLID rounded enclosure (matching the design); the enclosure SHALL carry the members' common role token rendered vertically down its left inner edge and a `▲` collapse affordance at its top-right. The member boxes SHALL be packed left-to-right and WRAPPED onto multiple rows so the enclosure fits the available diagram width: a new row is started whenever the next member box would exceed the available inner width, and a member box wider than the available width on its own occupies a row of its own. The enclosure's height SHALL grow to hold the wrapped rows, and the row's centering SHALL account for the wider/taller expanded block. `←→` navigation SHALL walk the members in order across the wrapped grid (stepping off the end of a row continues onto the next row), so every member is reachable. Every member with an out-of-group (downstream) edge SHALL carry a `↘` on its box, and the inter-stage edge below the group SHALL remain anchored beneath the now-taller wrapped block. The BUG-010 horizontal viewport SHALL still ensure-visible the selected member (so a lone over-wide member box scrolls into view). A collapsed group SHALL still render as a dashed range box.

#### Scenario: Fanning a group expands the diagram into member boxes

- **WHEN** the cursor is on a collapsed group and the user presses `Enter`
- **THEN** the diagram replaces the range box with one box per member inside a solid rounded enclosure carrying a `▲` collapse affordance, and the range-box label is no longer drawn for that stage

#### Scenario: A wide fanned group wraps onto multiple rows

- **WHEN** a fanned group has more member boxes than fit the pane width in one row
- **THEN** the member boxes wrap onto multiple rows so every member box is rendered fully within the diagram region (none overflow the pane and no horizontal-scroll edge indicator is needed), and the first and last members render on different rows

#### Scenario: The cursor reaches every member across the wrapped rows

- **WHEN** a fanned group is wrapped onto multiple rows and the cursor is walked with `→` from the first member
- **THEN** the cursor advances through every member in order, including members on later rows, ending on the last member

#### Scenario: The downstream edge anchors below the wrapped group

- **WHEN** a fanned group that feeds a downstream stage is wrapped onto multiple rows
- **THEN** the inter-stage connector and the downstream stage's box are rendered below the wrapped group block (not overlapping it)

#### Scenario: A collapsed group stays a dashed range box

- **WHEN** a group is not fanned out
- **THEN** the diagram renders it as the collapsed dashed range box
