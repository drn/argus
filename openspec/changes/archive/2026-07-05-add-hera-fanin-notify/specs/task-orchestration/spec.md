## MODIFIED Requirements

### Requirement: Gater materializes a planned node when its blockers complete

The system SHALL run a hera-native gater that watches role status and, when **all** of a planned node's blockers have reached `done`, materializes that node into a live worker by creating its binding and agent via the existing `agent.CreateAndStart` against the **pre-created** role (not a freshly minted role). Materialization SHALL be idempotent: a node that is already bound, already materializing, or already `ready`-and-claimed SHALL NOT be materialized again. A node with any blocker not yet `done` SHALL remain planned. At materialization the worktree and branch SHALL be created, with `base_branch` resolved from the now-existing blocker branches; when a node has multiple blockers, `base_branch` SHALL be the branch of the most-recently-bound blocker (the stack tip). When a node has **two or more** blockers, materialization SHALL additionally notify the coordinator naming the chosen `base_branch` (and which blocker it came from) and the other blocker(s)' branches that were NOT merged in, so the pick is visible even when the coordinator did not author a self-rebase step. This notice is a one-shot, best-effort delivery: a delivery failure SHALL be logged and SHALL NOT be retried, and SHALL NOT affect materialization, which has already succeeded.

#### Scenario: All blockers done triggers materialization

- **WHEN** the last blocker of a planned node reaches done
- **THEN** the gater materializes the node into a live worker via CreateAndStart against the pre-created role

#### Scenario: Materialization is idempotent

- **WHEN** the gater re-evaluates a node that is already bound or already materializing
- **THEN** it does not spawn a second agent for that node

#### Scenario: Node stays planned while a blocker is unfinished

- **WHEN** at least one blocker of a planned node has not reached done
- **THEN** the node remains planned and no agent is spawned

#### Scenario: Worktree and base_branch resolved at materialization

- **WHEN** a planned node materializes
- **THEN** its worktree and branch are created at that moment and base_branch is resolved from its blockers' branches

#### Scenario: Fan-in materialization notifies the coordinator of the un-merged siblings

- **WHEN** a planned node with two or more blockers materializes
- **THEN** the coordinator is notified naming the resolved `base_branch` (and the blocker it came from) and the other blocker(s)' branches that were not automatically merged in

#### Scenario: Single-blocker and root materializations stay silent

- **WHEN** a planned node with zero or one blockers materializes
- **THEN** no fan-in notice is sent (there is no sibling branch to report)

#### Scenario: A failed fan-in notice does not affect materialization or retry

- **WHEN** the fan-in notice delivery fails
- **THEN** the failure is logged, the already-completed materialization is unaffected, and the notice is not retried on a later tick
