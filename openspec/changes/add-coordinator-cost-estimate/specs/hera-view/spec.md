## MODIFIED Requirements

### Requirement: Orchestrator and role row rendering (area 3)

The system SHALL render an orchestrator header with a fold chevron (▸ collapsed / ▾ expanded), a coordinator marker glyph, the orchestrator name, a right-aligned agent count rendered as a bare number (no parentheses — e.g. `17`, not `(17)`), and — when the subtree's rollup cost is available (see `cost-estimation`) — a subtree cost figure rendered alongside the agent count (e.g. `17 · $4.82`), omitted entirely when no role in the subtree has a computed cost. The count SHALL be the total number of non-coordinator roles (worker rows, including bridged sub-coordinator rows) across the orchestrator's WHOLE bridge subtree — itself plus every orchestrator nested beneath it through the worker→coordinator bridge, at any depth — INCLUDING roles currently hidden in a per-coordinator Archive bucket, regardless of whether any role's binding is still live. A nuked role or orchestrator is never counted in the agent count (the Model excludes both entirely) — this exclusion is unchanged by this requirement's cost addition; the subtree cost figure, by contrast, DOES include a nuked role's recorded cost, per `cost-estimation`'s subtree-rollup requirement, since it is a financial total rather than a display census. Each orchestrator's own coordinator role is excluded from the agent count at every level in the subtree (folded into its own header, or already represented one level up by the bridging worker row that reaches it), so a nested sub-coordinator's agent is counted exactly once; the cost figure applies the same exactly-once convention to a nested sub-coordinator's own cost. It SHALL render a role row with a status glyph (see status-icon precedence) followed by the role name, and — for a worker or freelance role (see the context-pressure indicator requirement below) — a reserved trailing indicator slot. Selection is indicated by a `›` marker in the gutter and the selected palette; archived placement dims the row's text style (the glyph itself never lies — only the style dims).

Per-role cost/token detail is NOT rendered on the rail row itself — the row is already width-constrained by the reserved context-pressure indicator slot — and instead surfaces in the details pane's per-role view.

Derived from: `internal/tui/hera/rail.go` (`drawRow`, `drawOrchRow`, `drawRoleRow`), `internal/tui/hera/model.go` (`Model.SubtreeAgentCount`, `Model.BridgeSubtree`, and the new subtree-cost rollup from `cost-estimation`).

#### Scenario: Orchestrator header counts its whole subtree, archive included

- **WHEN** an orchestrator has one live worker and two archived (Tier-1 hidden) workers, one of which still holds a live binding (its role was archived without ending its binding)
- **THEN** its header renders `3` right-aligned, with no surrounding parentheses — all three count, regardless of liveness

#### Scenario: A nested sub-coordinator's own archive rolls up to its parent's badge

- **WHEN** a root orchestrator bridges to a child sub-coordinator via a worker row, and that child has two workers of its own (one archived)
- **THEN** the root's badge counts the bridging row once plus both of the child's workers, and the child's own badge (viewed on its own) counts just its own two workers — neither count double-counts the bridging row against the child's own coordinator role

#### Scenario: Archived row dims without changing its glyph

- **WHEN** a role is rendered in the archive section
- **THEN** its text and status glyph render in the dimmed style while the glyph identity is unchanged

#### Scenario: Header shows a subtree cost figure alongside the agent count

- **WHEN** at least one role in an orchestrator's bridge subtree has a computed cost
- **THEN** the header renders the subtree's total cost alongside the existing agent-count badge

#### Scenario: Header omits the cost figure when nothing is measured

- **WHEN** no role in an orchestrator's bridge subtree has any computed cost (all unmeasured or all resolved to uncurated models)
- **THEN** the header renders only the agent-count badge, with no cost figure shown

#### Scenario: A nuked child's cost still reaches the header, unlike its agent count

- **WHEN** a nuked role in the subtree had accrued nonzero cost before being nuked
- **THEN** the header's cost figure includes that nuked role's cost even though the same header's agent-count badge does not count it
