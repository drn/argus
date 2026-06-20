## ADDED Requirements

### Requirement: Orchestrator-scoped blocking-edge query

The store SHALL expose `ListHeraBlocks(orchID)` returning every `hera_blocks` edge whose endpoints belong to the given orchestrator, as `(blocked_role_id, blocker_role_id)` pairs. This complements the substrate's per-role `HeraBlockersOf` with a single bulk read for the whole orchestrator, so the plan view can project all edges without N per-node queries. The result is deterministically ordered (by blocked then blocker role id) and excludes edges whose endpoints are archived or nuked roles, consistent with how the view filters roles.

#### Scenario: Returns all edges for an orchestrator

- **WHEN** an orchestrator has blocking edges `3a←2b` and `2a←1a`
- **THEN** `ListHeraBlocks(orchID)` returns both pairs in deterministic order

#### Scenario: Empty when no plan authored

- **WHEN** an orchestrator has no blocking edges
- **THEN** `ListHeraBlocks(orchID)` returns an empty slice without error
