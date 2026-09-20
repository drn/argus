## MODIFIED Requirements

### Requirement: Subtree TLDR roll-up via hera_tree_updates

The system SHALL, on `hera_tree_updates`, scan only the caller's explicitly recorded orchestrator subtree for messages newer than a cursor and return TLDR-only subject lines, capped at 200. A child belongs to a parent only when a live hierarchy relation records that parent, child, and the parent-side bridge role; shared Argus task IDs alone SHALL NOT create a subtree relationship or grant message visibility.

#### Scenario: Independent shared task IDs do not bridge orchestrators

- **WHEN** two active orchestrators have bindings for the same Argus task but no explicit hierarchy relation
- **THEN** each orchestrator remains a top-level tree and `hera_tree_updates` excludes the other's messages

### Requirement: TUI coordinator re-parent and detach own their bridge

The system SHALL have TUI `J` re-parent persist an explicit hierarchy relation for its newly created parent-side bridge role. TUI `J` detach SHALL remove only that relation and its owned bridge role, and SHALL NOT end or delete another live role merely because it shares the coordinator task ID.

#### Scenario: Detach preserves unrelated membership

- **WHEN** a coordinator task also has an independent worker binding in another active orchestrator
- **THEN** detaching the coordinator removes no unrelated worker role or binding
