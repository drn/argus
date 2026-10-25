## ADDED Requirements

### Requirement: Batched bridge authority snapshot

The authenticated GET /api/bridge/states endpoint SHALL return a task-id-keyed map of currently live, launch-matched Claude bridge states. Local daemon RPC and remote TUI SHALL read the same snapshot semantics. Existing task, Hera and status endpoints SHALL use authoritative idle and needs-input verdicts, preserving scraper fallback for permission intervals.

#### Scenario: Remote TUI reads bridge state

- **WHEN** the remote TUI refreshes its running tasks
- **THEN** it fetches a batched live snapshot and overrides terminal inference for authoritative bridge sessions

#### Scenario: No live bridge

- **WHEN** no current bridge is live
- **THEN** the batch endpoint returns an empty object and clients use scraping
