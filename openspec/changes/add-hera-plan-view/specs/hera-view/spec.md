## MODIFIED Requirements

### Requirement: Details region stacks roster over the plan DAG (area 6)

For a coordinator selection the system SHALL render BOTH the read-only `" Details "` roster (top) and the embedded `" Plan "` graph (bottom) at once with no toggle. The roster is sized to its natural content height, capped at half the region (so the graph keeps at least half), clamped to a minimum of 3 rows and to the region height; the graph fills the remainder and is skipped when fewer than 2 rows remain. The graph (the plan view) is the ONLY interactive surface — every key in the details region forwards to it (the 4-way navigation, `Enter`/`Space`, and `Esc`), and coordinator-region clicks route to it.

#### Scenario: Both panels render together

- **WHEN** a coordinator is selected and the region is tall enough
- **THEN** the `" Details "` roster and the `" Plan "` graph both render, with no `" DAG "` or `" Orchestration Tree "` title

#### Scenario: Enter on a plain leaf node jumps to its role within the Hera view

- **WHEN** the details region is focused and the user presses Enter on a plain leaf node (not a group, not a sub-coordinator)
- **THEN** the system selects that node's role in the Hera rail (by its bound task id) and moves focus to that role's agent pane, staying on the Hera tab (it SHALL NOT switch to the Tasks view)

#### Scenario: Enter on a leaf node whose session is dead restarts and joins it

- **WHEN** the user presses Enter on a plain leaf node whose backing agent session has exited (no live session in the runner)
- **THEN** the system restarts-and-joins that session, identically to the rail's Enter — firing the same reattach under the same liveness gate (a dead session of any role, or a live non-coordinator role, fires it; a live coordinator stays navigate-only) — so the node never merely selects without restarting

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

### Requirement: Collapsed group box format and feed indicator (area 6)

A collapsed parallel group box SHALL render as two lines (matching the design):

- **Top line:** the bare range label `[first–last]` followed by a feed indicator — `→ <short-id>` when every out-of-group edge from the group's members points to ONE downstream node (full feed), or `↘` when only some members feed downstream (partial). No feed indicator when the group feeds nothing. The range label SHALL NOT carry a trailing bare count (the old `[2a–2c] 3 ○`, which read as "blocks 3a", is removed).
- **Sub line:** the group's common role token followed by the per-state aggregate counts, joined by ` · ` (e.g. `research · 1 ✓ · 2 ○`); the counts alone when there is no common token.

On fan-out, every member with an out-of-group edge SHALL carry a `↘` on its box (not only a single feeder).

#### Scenario: Full-feed group shows the arrow target

- **WHEN** all members of group `[2d–2f]` feed the single downstream `3a`
- **THEN** the collapsed box top line is `[2d–2f] → 3a` and its sub line is `<token> · <counts>`

#### Scenario: Partially-feeding group shows the partial marker

- **WHEN** only `2b` of group `[2a–2c]` blocks the downstream `3a`
- **THEN** the collapsed box top line is `[2a–2c] ↘` (no arrow target) and, fanned out, `2b` carries a `↘`

### Requirement: Four-way plan navigation with group fan-out (area 6)

The plan view SHALL support a cursor over `(stage, slot, member)`: `↑`/`↓` change the stage and collapse any fanned-out group on the way; `←`/`→` move within a stage between slots (nodes and collapsed groups); `Enter`/`Space` on a COLLAPSED group fan it out. When the cursor is on an interior MEMBER of a fanned-out group, `Enter`/`Space` SHALL navigate to that member — firing the same leaf action a plain node fires (open/jump, or drill in when the member is a sub-coordinator) — and SHALL NOT collapse the group; collapsing a fanned group is `Esc`'s job (see the Esc back-out requirement). `Enter`/`Space` on a fanned enclosure with no member under the cursor SHALL collapse it. Inside a fanned-out group, `←`/`→` walk between members; stepping off either edge exits and collapses the group and moves to the adjacent slot (or clamps at the stage edge).

#### Scenario: Up/down changes stage and collapses

- **WHEN** a group is fanned out and the user presses `↓`
- **THEN** the cursor moves to the next stage and the group collapses

#### Scenario: Enter fans out a group

- **WHEN** the cursor is on a collapsed group and the user presses `Enter`
- **THEN** the group fans out to show its members and the cursor lands on the first member

#### Scenario: Enter on a fanned-out group member navigates to it (does not collapse)

- **WHEN** a group is fanned out, the cursor is on one of its members, and the user presses `Enter`
- **THEN** the system fires that member's leaf action (jump to / open the member, or drill in when it is a sub-coordinator) and the group stays fanned out — it does NOT collapse

#### Scenario: Stepping off a group edge exits and collapses

- **WHEN** the cursor is on the last member of a fanned-out group and the user presses `→`
- **THEN** the group collapses and the cursor moves to the next slot in the stage

### Requirement: Plan master-detail header (area 6)

Above the plan diagram the system SHALL render a fixed-height header describing the current selection: for a node, its name, a **status** (the node's state — planned for a never-bound role, else the live worker's state — with the state glyph), a description (the first line of the role's delivery prompt), and what it feeds; for a collapsed group, the group range and title, its members, and its downstream target. The header height is budgeted exactly so the diagram fills the remainder without truncation drift.

#### Scenario: Header shows the selected node

- **WHEN** the cursor is on a node
- **THEN** the header shows that node's name, status, description, and feeds

#### Scenario: Node header status reflects state

- **WHEN** the cursor is on a never-bound planned node
- **THEN** the header status line reads "planned"; for a live working node it reads "working"

#### Scenario: Header shows the selected group

- **WHEN** the cursor is on a collapsed group
- **THEN** the header shows the group range/title, its members, and its downstream target

### Requirement: Sub-coordinator drill-in (area 6)

When the cursor is on a node whose bound task is the coordinator of a child orchestrator (a sub-coordinator, discovered via the rail's in-memory bridge), `Enter` SHALL push that child orchestrator onto a navigation stack and re-project the plan DAG for the child. The header title SHALL reflect the currently-displayed orchestrator. Drill-in is navigation between independently-projected per-orchestrator DAGs; it SHALL NOT draw a cross-orchestrator edge. A sub-coordinator node SHALL carry a visible drillable marker so the gesture is discoverable. (Drilling back out is governed by the `Esc` back-out requirement below.)

#### Scenario: Enter drills into a sub-coordinator

- **WHEN** the cursor is on a sub-coordinator node and the user presses `Enter`
- **THEN** the diagram swaps to that child orchestrator's plan DAG and the header title names the child

### Requirement: Esc backs out one level and never jumps to the rail (area 6)

`Esc` in the plan view SHALL back out of the plan view's OWN state by one level, in a fixed priority order, and SHALL be CONSUMED by the widget in every case (it SHALL NOT propagate to the page or the rail):

1. when the cursor is on a fanned-out group, collapse it (un-fan; cursor returns to the collapsed slot);
2. otherwise, when drilled into a sub-coordinator, pop back to the parent orchestrator's plan DAG;
3. otherwise (root, nothing fanned), it is a consumed no-op.

The operator leaves the plan pane via the focus ladder (`Ctrl+Q` / `Tab`), never via `Esc`.

#### Scenario: Esc collapses a fanned group first

- **WHEN** the cursor is on a fanned-out group and the user presses `Esc`
- **THEN** the group collapses, the cursor returns to the collapsed slot, and the drill stack is unchanged

#### Scenario: Esc pops back to the parent when nothing is fanned

- **WHEN** the view is showing a drilled-in child plan DAG with nothing fanned and the user presses `Esc`
- **THEN** the diagram returns to the parent orchestrator's plan DAG

#### Scenario: Esc at the root is a consumed no-op

- **WHEN** the cursor is at the root with nothing fanned and the user presses `Esc`
- **THEN** nothing changes and focus stays in the plan pane (Esc does not reach the rail)

### Requirement: Live plan node icons are 1:1 with the rail (area 6)

A LIVE plan node's status icon (glyph AND style, including the animated spinner for a genuinely-active node) SHALL be identical to what the rail's status icon renders for the same role, computed through a SINGLE shared classifier so the two surfaces can never drift — not a parallel glyph table. The shared vocabulary: ready-to-close → review clipboard; needs-input → the needs-input glyph (so a worker blocked on a prompt is actionable from the DAG); done → `✓`; genuinely-active → the animated spinner (the plan view recomputes the frame at draw so it animates in lockstep); idle → moon-outline; live-quiet → moon-stars. Two plan-view-specific overlays the rail has no concept of: a PLANNED (never-bound) node renders the `○` circle, and a FAILED node (bound task result reports failure) renders `✕`. The header Status line uses the same resolved icon. The animated-spinner re-resolution applies ONLY when the shared classifier actually resolved to the spinner; a higher-precedence signal (notably needs-input on a still-active in_progress role) resolves to its STATIC glyph and the node SHALL NOT animate, so it renders 1:1 with the rail's `?` rather than swapping in the spinner frame.

#### Scenario: A live node's icon equals the rail's

- **WHEN** a live worker role is in any status (done / working / idle / in-review / needs-input)
- **THEN** its plan node renders the same glyph and style the rail's status icon renders for that role, and a working node animates

#### Scenario: Needs-input outranks active without animating (BUG-012)

- **WHEN** a live worker role's bound task is in_progress (genuinely active) AND the role also needs input (blocked on a prompt, or a descendant in its subtree does)
- **THEN** its plan node renders the static needs-input `?` glyph and style — identical to the rail row — and is NOT flagged animated, so the widget does not swap the `?` for the live spinner frame at draw

#### Scenario: Planned and failed overlays

- **WHEN** a node is a never-bound planned role, or a bound role whose task reports failure
- **THEN** the planned node renders `○` and the failed node renders `✕`

### Requirement: Plan nodes render as boxes with a double-line border selection cue (area 6)

The plan diagram SHALL render each UNSELECTED node as a padded single rounded box (`╭─╮ / │ glyph short-id │ / ╰─╯`) and the SELECTED node (the box under the cursor) as a DOUBLE-LINE border box (`╔═╗ / ║ glyph short-id ║ / ╚═╝`) in its OWN state colour — bold when the widget owns focus, plain weight when it does not (the focused distinction is weight, not hue). Both selected and unselected boxes draw the border in the node's state colour, and the content (glyph and label) always keeps its state colour; there SHALL be no dedicated selection colour and no background fill. Selection is conveyed purely by border weight (double vs single), so it survives any state colour — a selected DONE (green) node shows a green double border, distinguishable from a green single-rounded done node. A collapsed group under the cursor SHALL keep its dashed identity but render with a HEAVY dashed border (`┏╍╍┓ / ╏ … ╏ / ┗╍╍┛`) instead of the light dashed border, so selection reads without losing the collapsed/expandable signal. Each stage's box row SHALL be centered horizontally within the diagram region and the whole block centered vertically when it fits.

A dedicated selection colour is deliberately NOT used: green would collide with the green DONE state. A background fill is deliberately NOT used: a terminal background is whole-cell, but the border glyph sits mid-cell, so a fill paints gray around the border line and escapes the visual box.

#### Scenario: The cursor's box renders a double-line border with no fill

- **WHEN** the cursor is on a plan node
- **THEN** that node's box renders a double-line border in its state colour (bold when focused) with state-coloured content and no selection background fill, while a non-cursor box renders a single rounded border in its state colour

#### Scenario: A selected done node is distinguishable from an unselected done node

- **WHEN** the cursor is on a done (green) node and another done node is not selected
- **THEN** the selected node renders a green DOUBLE-line border and the unselected node renders a green single rounded border — distinguishable by border weight despite sharing the green state colour

#### Scenario: Cursor member box renders a double-line border in a fanned group

- **WHEN** the cursor is on a member of a fanned-out group
- **THEN** that member's box renders a double-line border in its state colour (no fill) and the other member boxes render single rounded borders in their state colour

### Requirement: Fanned group visually expands into member boxes (area 6)

When a parallel group is fanned out, the diagram SHALL render its members as individual node boxes inside a SOLID rounded enclosure (matching the design), laid out horizontally; the enclosure SHALL carry the members' common role token rendered vertically down its left inner edge and a `▲` collapse affordance at its top-right. The row's centering SHALL account for the wider expanded width. Every member with an out-of-group (downstream) edge SHALL carry a `↘` on its box. A collapsed group SHALL still render as a dashed range box.

#### Scenario: Fanning a group expands the diagram into member boxes

- **WHEN** the cursor is on a collapsed group and the user presses `Enter`
- **THEN** the diagram replaces the range box with one box per member inside a solid rounded enclosure carrying a `▲` collapse affordance, and the range-box label is no longer drawn for that stage

#### Scenario: A collapsed group stays a dashed range box

- **WHEN** a group is not fanned out
- **THEN** the diagram renders it as the collapsed dashed range box

### Requirement: Plan diagram has a footer hint and scrolls to the cursor (area 6)

The plan diagram SHALL render a dim footer hint bar on its bottom row (`↑↓ stage · ←→ within · Enter fan · Esc back`). Because boxes are taller than single-line chips, when the stacked block exceeds the diagram region the view SHALL scroll vertically so the cursor's stage box is fully visible; when the block fits, no scroll is applied. All painting SHALL be clipped to the diagram region (no `screen.Sync`).

#### Scenario: Footer hint renders

- **WHEN** the plan diagram is drawn
- **THEN** a dim nav-legend footer row is present

#### Scenario: The diagram scrolls to keep the selected node visible

- **WHEN** the plan has more stages than fit the region and the cursor is on the last stage
- **THEN** the last stage's box is rendered within the region and the first stage has scrolled out of view

### Requirement: Plan cursor and fan-out survive a model refresh (area 6)

The Hera view re-projects the selected coordinator's plan on every refresh tick. A re-projection of the SAME orchestrator's plan SHALL preserve the operator's cursor position and fanned-group state: when the projected structure is unchanged the cursor and fan-out are untouched; when the structure changed (a cascade step materialized a node, a state flipped) the cursor SHALL re-anchor to the same node (or collapsed group, by its member set) when it still exists and clamp into the new layout when it vanished, and every still-present fanned group SHALL stay fanned. A genuine selection change (a different coordinator) or a drill-in push/pop SHALL still reset the cursor.

#### Scenario: Refresh preserves the cursor and fanned group

- **WHEN** the operator has moved the plan cursor and fanned out a group, and a refresh tick re-projects the same coordinator's plan
- **THEN** the cursor stays where the operator put it and the fanned group stays fanned

#### Scenario: Selecting a different coordinator resets the cursor

- **WHEN** the operator selects a different coordinator
- **THEN** the plan cursor resets to the first stage
