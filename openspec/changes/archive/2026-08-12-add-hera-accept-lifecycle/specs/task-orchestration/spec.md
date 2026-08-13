## ADDED Requirements

### Requirement: Gater auto-accepts a materialized node's blockers

When the gater materializes a worker-kind planned node, it SHALL fire the same accept-equivalent `hera_accept` provides – marking `complete` and notifying – for EVERY one of that node's blockers, sourced from the SAME blocker-id list already resolved for the fan-in notice (no additional query, no new trigger point). This is the DAG's own version of "the coordinator rolling forward to the next item": the instant a dependent materializes, every one of its blockers has, by definition, just had its `done` signal consumed by the DAG moving past it.

This auto-accept SHALL be:

- **Best-effort** – an accept-equivalent failure for one blocker SHALL be logged and SHALL NOT fail the materialization that already succeeded, nor prevent the accept-equivalent from being attempted for the node's other blockers.
- **Idempotent** – a blocker gated by two or more dependent nodes has its accept-equivalent fired once per dependent that materializes, but only the FIRST such call produces a status flip and a notification; every subsequent call against the now-already-`complete` task is a silent no-op (the same idempotency `hera_accept` itself provides).
- **Scoped to the ordinary worker-kind materialize path** – a ROOT node (no blockers) fires no accept calls. A `subcoord` node's OWN materialization does not retroactively auto-accept its blockers through this mechanism (mirrors the existing fan-in notice's own worker-kind-only scope); a subcoord node's blockers ARE still accepted whenever some OTHER, ordinary dependent of theirs materializes through the normal path.

Derived from: `internal/heragater/heragater.go` (`acceptBlockers`, wired via `Accepter`/`SetAccepter`), `internal/hera/accept.go` (`AcceptRole`, the shared primitive also used by the `hera-coordination` capability's `hera_accept` tool), `internal/daemon/daemon.go` (wiring the gater's `Accepter` to `hera.AcceptRole`).

#### Scenario: Materializing a node accepts its single blocker

- **WHEN** a planned node with exactly one blocker materializes
- **THEN** that blocker's bound task flips to complete and it receives the same acceptance notification `hera_accept` sends

#### Scenario: Materializing a fan-in node accepts every blocker

- **WHEN** a planned node with two or more blockers materializes
- **THEN** every one of those blockers' bound tasks flips to complete and each receives an acceptance notification

#### Scenario: A blocker already accepted by a sibling dependent is a no-op

- **WHEN** a blocker gates two dependent nodes and the first dependent's materialization has already accepted it
- **THEN** the second dependent's materialization fires the same accept-equivalent call against that blocker with no error, no second status write, and no second notification

#### Scenario: An accept failure does not affect materialization or sibling blockers

- **WHEN** the accept-equivalent call for one blocker of a fan-in node fails
- **THEN** the failure is logged, the node's materialization (which already succeeded) is unaffected, and the accept-equivalent is still attempted for the node's other blockers

#### Scenario: A root node materializing fires no accept calls

- **WHEN** a planned node with no blockers materializes
- **THEN** no accept-equivalent call is made

#### Scenario: No coordinator to accept as does not panic

- **WHEN** a node materializes in an orchestrator with no coordinator role
- **THEN** the gater logs the condition and proceeds without panicking; no accept-equivalent call is made
