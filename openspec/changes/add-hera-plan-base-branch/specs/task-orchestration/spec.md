## ADDED Requirements

### Requirement: Plan-DAG root nodes materialize off a configurable base branch

When the gater materializes a **root** planned node (one with no blockers, so no blocker branch to stack on), the system SHALL resolve the new worktree's base branch in this order: (1) the orchestrator's explicit `base_branch` when one was set at bootstrap; (2) otherwise the orchestrator's coordinator role's bound-task branch; (3) otherwise the project default branch. The orchestrator SHALL persist an optional `base_branch`, supplied (optionally) when the orchestrator is created and defaulting to empty. This composes with the existing gater-materialization rule for nodes that DO have blockers — a blocker-having node's base SHALL continue to resolve from the most-recently-bound `done` blocker, unchanged. The default-to-coordinator-branch behavior SHALL be backward-compatible: a coordinator on the project default branch yields roots on the project default branch.

#### Scenario: Root node uses the explicit orchestrator base branch

- **WHEN** an orchestrator was created with an explicit `base_branch` and a root planned node materializes
- **THEN** the new worktree is based on that explicit branch

#### Scenario: Root node defaults to the coordinator branch

- **WHEN** an orchestrator has no explicit `base_branch` set and a root planned node materializes
- **THEN** the new worktree is based on the orchestrator's coordinator role's bound-task branch

#### Scenario: Falls back to the project default when no base resolves

- **WHEN** a root planned node materializes and neither an explicit base branch nor a coordinator branch is resolvable
- **THEN** the new worktree is based on the project default branch, as before this change

#### Scenario: Blocker-having node base resolution is unchanged

- **WHEN** a planned node with one or more blockers materializes
- **THEN** its base branch is resolved from the most-recently-bound blocker's branch, exactly as before this change

#### Scenario: Orchestrator persists an optional base branch

- **WHEN** an orchestrator is bootstrapped with no base branch supplied
- **THEN** its persisted base branch is empty and root nodes fall through to the coordinator-branch default
