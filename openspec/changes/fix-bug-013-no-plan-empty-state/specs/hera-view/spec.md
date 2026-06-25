# Hera View

## MODIFIED Requirements

### Requirement: Plan DAG projects blocking edges and planned nodes in-memory (area 6)

The system SHALL project the embedded graph's nodes from the rail's already-built model via `heraPlanNodes` — a pure in-memory read with no DB call at Draw time. The graph renders the orchestrator's PLAN: every planned (never-bound) and live worker role as a node, and every `hera_blocks` blocking edge between them as a dependency edge. Stage placement is computed longest-path over the blocking edges; a node's short-id (parsed from the role-name prefix) is the display label only and never drives placement. A live node's colour comes from its bound task's argus status/result (including the failed-result red `✕`); a planned (never-bound) node renders violet with the `○` glyph. When the selected orchestrator has NO authored plan — no planned nodes AND no blocking edges — the graph SHALL render an empty-plan placeholder ("No plan authored." with a one-line authoring hint) and SHALL NOT render the orchestrator's live worker roles as a stage; the live agents remain visible in the rail. When no orchestrator is selected, the graph renders empty without panic.

#### Scenario: Planned and live nodes render with edges

- **WHEN** an orchestrator has planned roles and blocking edges
- **THEN** the graph shows each role as a node, planned nodes violet `○`, live nodes coloured by task status, connected by the blocking edges

#### Scenario: Stage is computed from edges, not the short-id number

- **WHEN** a node's short-id number disagrees with its computed longest-path layer (e.g. after a plan edit)
- **THEN** the node is placed by the computed layer and still labelled with its short-id

#### Scenario: No authored plan renders the empty-plan state

- **WHEN** the selected orchestrator has no planned nodes and no blocking edges (even when it has live workers, or is a sub-coordinator bridge with neither planned nodes nor block edges)
- **THEN** the graph renders the empty-plan placeholder ("No plan authored." + an authoring hint) with no stages, and does NOT render the live worker roles as a flat edgeless stage — the live agents stay visible in the rail

#### Scenario: No orchestrator selected yields an empty graph

- **WHEN** no orchestrator is selected
- **THEN** the plan graph renders empty without panic
