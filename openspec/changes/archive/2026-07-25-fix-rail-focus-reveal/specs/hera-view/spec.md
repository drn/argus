# Hera View

## MODIFIED Requirements

### Requirement: Rail reveals the ancestor path to a hidden needs-input descendant through closed folds

When a coordinator or nested sub-coordinator row is folded (collapsed) and any descendant role — at any depth, across bridged sub-orchestrators — needs input, the rail SHALL still render the specific ancestor chain down to each such leaf, even though the fold stays visually closed. Every other row at each level along that chain (sibling workers, sibling sub-coordinators with no needs-input descendant) SHALL remain fully hidden, exactly as under ordinary collapse. This reveal is a pure rendering behavior: it SHALL NOT alter the underlying collapse/fold state, and revealed rows SHALL be normal, selectable rail rows (cursor navigation, selection, and mutation keys all act on them exactly as they would if the ancestor were manually expanded).

In addition, the row the rail cursor was on immediately before a model rebuild (a role row, or an orchestrator header row) SHALL remain revealed through the SAME rebuild even if its own needs-input signal (or, for a header, its subtree's) clears in that rebuild's fresh model — as long as it was the previously-selected row. This "sticky" reveal is re-derived from the CURRENT selection on every rebuild, never cached beyond one rebuild's input: the instant the cursor lands on a different row (because the operator navigated away, or a prior rebuild's cursor restore already moved on), the next rebuild computes stickiness from the NEW selection and the old row is free to fold away like any other row whose reveal condition no longer holds. Sticky reveal SHALL have no observable effect when the previously-selected row was already visible through ordinary (non-collapsed) expansion.

#### Scenario: Single hidden leaf under one closed coordinator

- **WHEN** a coordinator is collapsed and exactly one descendant worker needs input
- **THEN** the rail renders the coordinator's header (with its own `(?)` marker) and, beneath it, that one worker's row — every other worker under the coordinator stays hidden

#### Scenario: Nested closed coordinators reveal the full chain

- **WHEN** a coordinator is collapsed, a sub-coordinator nested beneath it is also collapsed, and a worker under that sub-coordinator needs input
- **THEN** the rail renders the outer coordinator's header, the sub-coordinator's header (both marked `(?)`), and the needing-input worker's row beneath it — siblings at every level along the chain stay hidden

#### Scenario: Multiple hidden leaves under the same coordinator each get revealed

- **WHEN** a collapsed coordinator has two or more descendant workers (possibly under different collapsed sub-coordinators) that need input
- **THEN** the rail reveals a path to each such worker, not only the first found

#### Scenario: Unrelated siblings stay hidden

- **WHEN** a collapsed coordinator has both a needs-input descendant and unrelated siblings with no needs-input descendant
- **THEN** only the ancestor chain(s) to the needs-input descendant(s) render; the unrelated siblings remain fully hidden

#### Scenario: Toggling the fold still behaves exactly as before

- **WHEN** the user presses `Space` on a coordinator whose subtree is partially revealed via this behavior
- **THEN** the fold fully expands (or collapses) exactly as it would have before this change — the reveal does not change what `Space` does or leave any different post-toggle state

#### Scenario: A selected, revealed role survives its own needs-input flag clearing

- **WHEN** a worker's row is revealed only because it needs input, the rail cursor is on that row, and the next rebuild's model shows that worker (and nothing else in the model) no longer needing input
- **THEN** the worker's row remains rendered at the same ancestor path and the cursor remains on it — it is NOT yanked onto the ancestor's header or any other row

#### Scenario: A sticky reveal chain forces every intermediate bridging row too

- **WHEN** the selected, revealed role sits two or more collapsed-fold levels deep (a worker-bridge sub-coordinator's own worker), and every needs-input signal along that chain clears on the next rebuild
- **THEN** every row on the path — the outer coordinator's header, the intermediate bridging worker row, and the leaf's own row — all remain rendered, exactly as before the flags cleared

#### Scenario: The sticky reveal releases once the selection moves away

- **WHEN** a revealed role's row was sticky-kept visible, the operator then moves the cursor to a different row, and a further rebuild occurs with nothing along the old row's path needing input
- **THEN** the old row folds away normally on that further rebuild — sticky reveal never pins a row in place indefinitely

#### Scenario: An orchestrator header selection is sticky the same way

- **WHEN** the rail cursor sits on a nested orchestrator's own header row (revealed only via the partial-fold reveal) and the next rebuild's model shows nothing in its subtree needing input anymore
- **THEN** that header row remains rendered and selected, identically to the role case
