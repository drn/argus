## ADDED Requirements

### Requirement: Batched bridge authority snapshot

The authenticated GET /api/bridge/states endpoint SHALL return a task-id-keyed map of currently live, launch-matched Claude bridge states with uncertain permission needs-input corroborated against raw scraper input. Local daemon RPC and remote TUI SHALL read the same snapshot semantics. Existing task, Hera and status endpoints SHALL use these same resolved verdicts while preserving live bridge busy authority throughout permission intervals.

#### Scenario: Remote TUI reads bridge state

- **WHEN** the remote TUI refreshes its running tasks
- **THEN** it fetches a batched live snapshot and overrides terminal inference for authoritative bridge sessions

#### Scenario: No live bridge

- **WHEN** no current bridge is live
- **THEN** the batch endpoint returns an empty object and clients use scraping
