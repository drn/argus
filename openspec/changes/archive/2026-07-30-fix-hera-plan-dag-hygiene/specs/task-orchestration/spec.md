## ADDED Requirements

### Requirement: A planned node is excluded once its parent orchestrator archives or is nuked

The system SHALL treat a planned node as no longer eligible for gating or materialization once its parent orchestrator has been archived or nuked, even though the node's own `archived_at`/`cancelled_at` are unaffected by an orchestrator-level action. Archiving or nuking an orchestrator SHALL cascade-cancel (stamp `cancelled_at`) every still-planned (never-materialized, not already archived or cancelled) worker-kind child role belonging to it, at the moment of the archive/nuke. Independently of that cascade, the planned-node listing query SHALL also exclude any node whose parent orchestrator has `archived_at` or `nuked_at` set, regardless of the node's own `cancelled_at` — so a node that predates this fix (its orchestrator ended before the cascade existed) is excluded without requiring any data migration.

#### Scenario: Archiving an orchestrator cancels its still-planned children

- **WHEN** a coordinator's orchestrator with a never-materialized planned child node is archived
- **THEN** the child node's `cancelled_at` is stamped

#### Scenario: Nuking an orchestrator cancels its still-planned children

- **WHEN** an orchestrator with a never-materialized planned child node is nuked
- **THEN** the child node's `cancelled_at` is stamped

#### Scenario: A materialized (already-bound) child is not cancelled

- **WHEN** an orchestrator is archived or nuked
- **THEN** any child role that already holds a binding (materialized) is left untouched — cascade-cancel only reaches never-bound planned nodes

#### Scenario: A planned node under an archived orchestrator is excluded from the gate even without cascade-cancel having run

- **WHEN** a planned node's parent orchestrator has `archived_at` or `nuked_at` set, regardless of whether the node itself carries `cancelled_at`
- **THEN** the node does not appear in the set of nodes the gater evaluates for materialization

### Requirement: Materialization failures escalate after repeated retries

The system SHALL track, per planned node, the number of CONSECUTIVE materialization failures since the node last succeeded or was last evaluated as a fresh planned node. When that count reaches a bounded threshold, the system SHALL send a ONE-TIME escalation notice to the coordinator naming the node and the last error, instead of continuing to retry in total silence. The system SHALL NOT automatically cancel, reconfigure, or guess a fix for a node that has crossed the threshold — escalation is advisory only, and the node SHALL remain planned and continue to be retried on the normal tick schedule. A node that later succeeds, or that is no longer a planned node (materialized, cancelled, or removed), SHALL have its failure count and escalation state cleared.

#### Scenario: A node under the threshold retries silently

- **WHEN** a planned node's materialization fails fewer than the escalation threshold's consecutive times
- **THEN** no notice is sent and the node remains planned for the next tick

#### Scenario: Crossing the threshold sends a one-time notice

- **WHEN** a planned node's materialization fails the escalation threshold's consecutive times
- **THEN** the coordinator receives exactly one escalation notice naming the node and the last error

#### Scenario: The notice does not repeat every subsequent tick

- **WHEN** a node continues failing after already crossing the escalation threshold
- **THEN** no further escalation notice is sent for that node

#### Scenario: A later success clears the failure count

- **WHEN** a previously-failing planned node materializes successfully
- **THEN** its consecutive-failure count and escalation state are cleared

#### Scenario: Escalation never auto-cancels or reconfigures the node

- **WHEN** a planned node crosses the escalation threshold
- **THEN** the node's `argus_project`, prompt, and `cancelled_at` are left unchanged — only a notice is sent
