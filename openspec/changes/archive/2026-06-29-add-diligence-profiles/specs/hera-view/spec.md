# Hera View

## ADDED Requirements

### Requirement: Plan view archetype and model readout with missing-profile warning

The plan/DAG view SHALL display, for each node, the node's selected archetype and the model/effort
applied to it, so the operator can see what tiering each unit of work received. The view SHALL also
surface a warning decoration on a node or project that points at a missing or invalid profile, matching
the runtime fail-open behavior (the agent runs on the CLI default, and the operator is told why).

#### Scenario: Node shows archetype and applied model

- **WHEN** a plan node has archetype `code_slice` resolving to model `sonnet`
- **THEN** the node's rendering shows the `code_slice` archetype and the applied `sonnet` model

#### Scenario: Missing profile warned

- **WHEN** a project points at a profile name that is absent or fails validation
- **THEN** the plan/DAG view shows a warning indicating the profile is missing or invalid
