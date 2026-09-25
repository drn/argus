## MODIFIED Requirements

### Requirement: RPC calls are bounded by a timeout

Every RPC the client issues SHALL complete within a bounded time so the TUI never hangs if the daemon becomes unresponsive. When a call exceeds its deadline, the client SHALL return a timeout error. Long-running operations that legitimately exceed the default deadline SHALL be allowed a larger deadline, including per-backend start deadlines for a task whose resolved backend has known slow prelaunch work.

#### Scenario: Daemon never responds

- **WHEN** an RPC is issued against a connection whose far end never replies
- **THEN** the call SHALL return a timeout error once the deadline elapses, instead of blocking indefinitely

#### Scenario: Long-running self-update

- **WHEN** the client issues the self-update RPC (which shells out to a build)
- **THEN** it SHALL use an extended deadline rather than the short default

#### Scenario: Starting a pi-backend session

- **WHEN** the client starts a session for a task whose resolved backend is pi
- **THEN** it SHALL use an extended deadline covering pi's ollama-readiness prelaunch work rather than the short default, so the call is not misreported as failed while the daemon is still legitimately starting the session
