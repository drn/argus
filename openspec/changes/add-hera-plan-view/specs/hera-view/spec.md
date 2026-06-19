## MODIFIED Requirements

### Requirement: Details region stacks roster over the plan DAG (area 6)

For a coordinator selection the system SHALL render BOTH the read-only `" Details "` roster (top) and the embedded `" Plan "` graph (bottom) at once with no toggle. The roster is sized to its natural content height, capped at half the region (so the graph keeps at least half), clamped to a minimum of 3 rows and to the region height; the graph fills the remainder and is skipped when fewer than 2 rows remain. The graph (the plan view) is the ONLY interactive surface — every key in the details region forwards to it (the 4-way navigation, `Enter`/`Space`, and `Esc`), and coordinator-region clicks route to it.

#### Scenario: Both panels render together

- **WHEN** a coordinator is selected and the region is tall enough
- **THEN** the `" Details "` roster and the `" Plan "` graph both render, with no `" DAG "` or `" Orchestration Tree "` title

#### Scenario: Enter on a plain leaf node opens its agent view

- **WHEN** the details region is focused and the user presses Enter on a plain leaf node (not a group, not a sub-coordinator)
- **THEN** the wired `OnEnter` callback fires to jump to that node's agent view

### Requirement: Plan DAG projects blocking edges and planned nodes in-memory (area 6)

The system SHALL project the embedded graph's nodes from the rail's already-built model via `heraPlanNodes` — a pure in-memory read with no DB call at Draw time. The graph renders the orchestrator's PLAN: every planned (never-bound) and live worker role as a node, and every `hera_blocks` blocking edge between them as a dependency edge. Stage placement is computed longest-path over the blocking edges; a node's short-id (parsed from the role-name prefix) is the display label only and never drives placement. A live node's colour comes from its bound task's argus status/result (including the failed-result red `✕`); a planned (never-bound) node renders violet with the `○` glyph. When the selected orchestrator has no planned nodes and no blocking edges, the graph SHALL render the orchestrator's live worker roles as a single flat edgeless stage with a "no plan" hint, so running workers are never invisible. When no orchestrator is selected, the graph renders empty without panic.

#### Scenario: Planned and live nodes render with edges

- **WHEN** an orchestrator has planned roles and blocking edges
- **THEN** the graph shows each role as a node, planned nodes violet `○`, live nodes coloured by task status, connected by the blocking edges

#### Scenario: Stage is computed from edges, not the short-id number

- **WHEN** a node's short-id number disagrees with its computed longest-path layer (e.g. after a plan edit)
- **THEN** the node is placed by the computed layer and still labelled with its short-id

#### Scenario: No plan authored renders live roles flat

- **WHEN** the selected orchestrator has no planned nodes and no blocking edges but has live workers
- **THEN** the graph renders those workers as one flat edgeless stage with a "no plan" hint, not an empty placeholder

#### Scenario: No orchestrator selected yields an empty graph

- **WHEN** no orchestrator is selected
- **THEN** the plan graph renders empty without panic

## ADDED Requirements

### Requirement: Short-id node labels (area 6)

The system SHALL label each plan node with its short-id — the prefix of the role name up to the first `-` (e.g. `2c-fact-checker` → `2c`), where the leading digits are the stage and the trailing letter(s) are the parallel member. When a role name has no parseable short-id prefix the label SHALL fall back to the truncated role name. The short-id is presentation only; it never affects layout or grouping correctness.

#### Scenario: Short-id parsed from the name prefix

- **WHEN** a role is named `2c-fact-checker`
- **THEN** its node is labelled `2c`

#### Scenario: Unparseable name falls back to the truncated name

- **WHEN** a role name has no short-id prefix
- **THEN** the node is labelled with the truncated role name and still placed by its edges

### Requirement: Parallel groups auto-collapse (area 6)

The system SHALL collapse a maximal set of nodes in the same computed stage that share the same blocker set and have no edges among themselves into a single range box labelled `[first–last]` (ids sorted), rendering `[first–last +N]` when membership is non-contiguous (N = the count of ids beyond the two endpoints of the span). A collapsed group box SHALL show aggregate counts of its members by state (e.g. `3 ✓ · 2 ⟳ · 1 ○`). A stage whose nodes do not form a clean group renders them as individual chips.

#### Scenario: Same-stage siblings collapse into a range box

- **WHEN** three same-stage nodes `2a`,`2b`,`2c` share a blocker set and have no edges among themselves
- **THEN** they render as one box `[2a–2c]` with aggregate state counts

#### Scenario: Non-contiguous group shows the span and a count

- **WHEN** a group's members are `2a`,`2b`,`2f` (non-contiguous)
- **THEN** the box renders `[2a–2f +1]`

### Requirement: Partial-dependency marker (option B) (area 6)

When only some members of a collapsed parallel group feed a downstream node, the system SHALL mark the collapsed group box with a `↘` (meaning one member continues downstream) and, on fan-out, mark the specific feeding member's chip with a `↘`. The group stays whole when collapsed; the precise feeding member is revealed only on fan-out.

#### Scenario: Partially-feeding group is marked

- **WHEN** only `2b` of group `[2a–2c]` blocks the downstream `3a`
- **THEN** the collapsed box shows `[2a–2c ↘]` and, fanned out, `2b` carries a `↘` chip

### Requirement: Four-way plan navigation with group fan-out (area 6)

The plan view SHALL support a cursor over `(stage, slot, member)`: `↑`/`↓` change the stage and collapse any fanned-out group on the way; `←`/`→` move within a stage between slots (nodes and collapsed groups); `Enter`/`Space` fan out a collapsed group or collapse a fanned-out one when the cursor is on a group. Inside a fanned-out group, `←`/`→` walk between members; stepping off either edge exits and collapses the group and moves to the adjacent slot (or clamps at the stage edge).

#### Scenario: Up/down changes stage and collapses

- **WHEN** a group is fanned out and the user presses `↓`
- **THEN** the cursor moves to the next stage and the group collapses

#### Scenario: Enter fans out a group

- **WHEN** the cursor is on a collapsed group and the user presses `Enter`
- **THEN** the group fans out to show its members and the cursor lands on the first member

#### Scenario: Stepping off a group edge exits and collapses

- **WHEN** the cursor is on the last member of a fanned-out group and the user presses `→`
- **THEN** the group collapses and the cursor moves to the next slot in the stage

### Requirement: Plan master-detail header (area 6)

Above the plan diagram the system SHALL render a fixed-height header describing the current selection: for a node, its name, a description (the first line of the role's delivery prompt), and what it feeds; for a collapsed group, the group range and title, its members, and its downstream target. The header height is budgeted exactly so the diagram fills the remainder without truncation drift.

#### Scenario: Header shows the selected node

- **WHEN** the cursor is on a node
- **THEN** the header shows that node's name, description, and feeds

#### Scenario: Header shows the selected group

- **WHEN** the cursor is on a collapsed group
- **THEN** the header shows the group range/title, its members, and its downstream target

### Requirement: Sub-coordinator drill-in (area 6)

When the cursor is on a node whose bound task is the coordinator of a child orchestrator (a sub-coordinator, discovered via the rail's in-memory bridge), `Enter` SHALL push that child orchestrator onto a navigation stack and re-project the plan DAG for the child; `Esc` SHALL pop back to the parent orchestrator's plan DAG. The header title SHALL reflect the currently-displayed orchestrator. Drill-in is navigation between independently-projected per-orchestrator DAGs; it SHALL NOT draw a cross-orchestrator edge. A sub-coordinator node SHALL carry a visible drillable marker so the gesture is discoverable.

#### Scenario: Enter drills into a sub-coordinator

- **WHEN** the cursor is on a sub-coordinator node and the user presses `Enter`
- **THEN** the diagram swaps to that child orchestrator's plan DAG and the header title names the child

#### Scenario: Esc pops back to the parent

- **WHEN** the view is showing a drilled-in child plan DAG and the user presses `Esc`
- **THEN** the diagram returns to the parent orchestrator's plan DAG

### Requirement: Selected plan node is highlighted (area 6)

The plan diagram SHALL render the chip under the cursor with a highlight style (reverse video, more prominent when the widget owns focus) so the selected node is visible in the diagram itself, not only described in the master-detail header. A chip the cursor is not on SHALL render with its plain state style.

#### Scenario: Cursor chip is highlighted

- **WHEN** the cursor is on a plan node
- **THEN** that node's chip is drawn with the highlight style and a non-cursor chip is not

#### Scenario: Cursor member chip is highlighted in a fanned group

- **WHEN** the cursor is on a member of a fanned-out group
- **THEN** that member's chip is drawn with the highlight style and the other member chips are not

### Requirement: Fanned group visually expands in the diagram (area 6)

When a parallel group is fanned out, the diagram SHALL render its members as individual chips (glyph + short-id) in place of the collapsed range box, laid out horizontally across that stage row; the row's centering SHALL account for the wider expanded width. A member that partially feeds a downstream node SHALL carry the `↘` partial-feed marker on its expanded chip (D5). A collapsed group SHALL still render as the range box.

#### Scenario: Fanning a group expands the diagram into member chips

- **WHEN** the cursor is on a collapsed group and the user presses `Enter`
- **THEN** the diagram replaces the range box with one chip per member on that stage row, and the range-box label is no longer drawn for that stage

#### Scenario: A collapsed group stays a range box

- **WHEN** a group is not fanned out
- **THEN** the diagram renders it as the collapsed range box

### Requirement: Plan cursor and fan-out survive a model refresh (area 6)

The Hera view re-projects the selected coordinator's plan on every refresh tick. A re-projection of the SAME orchestrator's plan SHALL preserve the operator's cursor position and fanned-group state: when the projected structure is unchanged the cursor and fan-out are untouched; when the structure changed (a cascade step materialized a node, a state flipped) the cursor SHALL re-anchor to the same node (or collapsed group, by its member set) when it still exists and clamp into the new layout when it vanished, and every still-present fanned group SHALL stay fanned. A genuine selection change (a different coordinator) or a drill-in push/pop SHALL still reset the cursor.

#### Scenario: Refresh preserves the cursor and fanned group

- **WHEN** the operator has moved the plan cursor and fanned out a group, and a refresh tick re-projects the same coordinator's plan
- **THEN** the cursor stays where the operator put it and the fanned group stays fanned

#### Scenario: Selecting a different coordinator resets the cursor

- **WHEN** the operator selects a different coordinator
- **THEN** the plan cursor resets to the first stage
