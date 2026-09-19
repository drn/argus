## ADDED Requirements

### Requirement: Self-service recycle uses content-aware idleness and exposes prolonged waits

The self-service recycle idle gate SHALL treat a live session as idle when either its raw output has been quiescent for the session idle threshold or its meaningful emulated-screen content has remained stable for the content-idle threshold while the agent's working affordance is absent. Cosmetic spinner, timer, cursor, or status-line redraws alone MUST NOT postpone a requested recycle indefinitely. A missing session SHALL continue to be treated as idle for recycle purposes.

The recycle watcher SHALL emit a clearly identified informational daemon log after a task has remained continuously pending and non-idle for at least two minutes. The watcher SHALL keep this diagnostic state in memory, SHALL avoid logging the same uninterrupted wait on every tick, and SHALL clear tracking when the pending request disappears or proceeds.

#### Scenario: Cosmetic redraw does not stall self-service recycle

- **WHEN** a role has requested self-service recycle and its session remains raw-busy only because cosmetic redraw bytes continue while meaningful screen content is stable and not working
- **THEN** the recycle watcher recognizes content idleness and restarts the role on its existing task

#### Scenario: Meaningfully working session still waits

- **WHEN** a role has requested self-service recycle and its session is producing meaningful output or shows the working affordance
- **THEN** the recycle watcher leaves the request pending and does not interrupt the session

#### Scenario: Missing session can recycle

- **WHEN** a role has requested self-service recycle but its prior session no longer exists
- **THEN** the recycle path proceeds rather than waiting for idle state from a missing session

#### Scenario: Prolonged wait is logged once

- **WHEN** a task remains continuously pending and non-idle for at least two minutes
- **THEN** the recycle watcher emits one clearly identified informational log containing the task ID and elapsed wait

#### Scenario: Resolved pending request clears diagnostic tracking

- **WHEN** a previously tracked pending request is removed or its recycle proceeds
- **THEN** the watcher forgets the prior wait so a later request starts a new diagnostic interval
