## MODIFIED Requirements

### Requirement: Orchestrator and role row rendering (area 3)

The system SHALL render an orchestrator header with a fold chevron (▸ collapsed / ▾ expanded), a coordinator marker glyph, the orchestrator name, and a right-aligned agent count `(N)`. The count SHALL be the total number of non-coordinator roles (worker rows, including bridged sub-coordinator rows) across the orchestrator's WHOLE bridge subtree — itself plus every orchestrator nested beneath it through the worker→coordinator bridge, at any depth — INCLUDING roles currently hidden in a per-coordinator Archive bucket, regardless of whether any role's binding is still live. A nuked role or orchestrator is never counted (the Model excludes both entirely). Each orchestrator's own coordinator role is excluded at every level in the subtree (folded into its own header, or already represented one level up by the bridging worker row that reaches it), so a nested sub-coordinator's agent is counted exactly once. It SHALL render a role row with a status glyph (see status-icon precedence) followed by the role name. Selection is indicated by a `›` marker in the gutter and the selected palette; archived placement dims the row's text style (the glyph itself never lies — only the style dims).

Derived from: `internal/tui/hera/rail.go` (`drawRow`, `drawOrchRow`, `drawRoleRow`), `internal/tui/hera/model.go` (`Model.SubtreeAgentCount`, `Model.BridgeSubtree`).

#### Scenario: Orchestrator header counts its whole subtree, archive included

- **WHEN** an orchestrator has one live worker and two archived (Tier-1 hidden) workers, one of which still holds a live binding (its role was archived without ending its binding)
- **THEN** its header renders `(3)` right-aligned — all three count, regardless of liveness

#### Scenario: A nested sub-coordinator's own archive rolls up to its parent's badge

- **WHEN** a root orchestrator bridges to a child sub-coordinator via a worker row, and that child has two workers of its own (one archived)
- **THEN** the root's badge counts the bridging row once plus both of the child's workers, and the child's own badge (viewed on its own) counts just its own two workers — neither count double-counts the bridging row against the child's own coordinator role

#### Scenario: Archived row dims without changing its glyph

- **WHEN** a role is rendered in the archive section
- **THEN** its text and status glyph render in the dimmed style while the glyph identity is unchanged
