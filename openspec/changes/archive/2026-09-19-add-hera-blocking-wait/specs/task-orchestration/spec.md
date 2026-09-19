## MODIFIED Requirements

### Requirement: Materialized worker checks in before doing real work

The system SHALL deliver to every gater-materialized worker a standing instruction, prepended to its prompt, to **check in with its coordinator and use blocking `hera_inbox(timeout_seconds=120)` calls** for a go/wait decision before doing real work. The check-in SHALL be worker-pulled (the worker reads its durable inbox), never a push the worker waits for passively, because mid-flight pushed messages to a busy or freshly-started worker are unreliable. The instruction SHALL recommend the server-side blocking wait rather than a background timer or sleep-based polling loop. The daemon gate itself SHALL NOT consult the coordinator before spawning — the spawn decision is purely state-driven.

#### Scenario: Materialized worker boots with a check-in order

- **WHEN** the gater materializes a worker
- **THEN** the delivered prompt instructs it to check in with its coordinator and call `hera_inbox` with `timeout_seconds=120` for go/wait before real work

#### Scenario: Go/wait arrives via pulled inbox

- **WHEN** the coordinator replies go or wait to a checked-in worker
- **THEN** the worker receives that decision by reading its inbox through a server-side blocking call, not by waiting for a pushed delivery or running a background timer

#### Scenario: Spawn does not wait on the coordinator

- **WHEN** a planned node's blockers are all done
- **THEN** the daemon materializes it immediately without first asking the coordinator
