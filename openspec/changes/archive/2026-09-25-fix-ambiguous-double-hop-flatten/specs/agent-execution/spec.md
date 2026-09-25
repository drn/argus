## MODIFIED Requirements

### Requirement: Slow prelaunch does not time out task startup

The system SHALL NOT delete a task's worktree or task row in response to a `StartSession` request timing out from the caller's perspective. A caller-side timeout means only that the caller stopped waiting for a response — the daemon-side start continues running to completion regardless — so it SHALL NOT by itself be treated as proof the daemon-side start failed. Backends whose prelaunch work can legitimately exceed the default RPC deadline SHALL be given a longer deadline on a per-backend basis; other backends SHALL retain the short default so a genuinely unreachable daemon is still reported quickly. This holds regardless of how many RPC hops separate the caller from the actual timeout (e.g. a daemon relaying to a supervisor) — an ambiguous outcome at an inner hop SHALL still be recognizable as ambiguous by the outer caller, not flattened into a definitive-looking failure.

#### Scenario: Pi prelaunch takes longer than the ordinary RPC timeout but completes within pi's extended deadline

- **WHEN** Pi prelaunch takes more than the default RPC timeout and succeeds within pi's extended per-backend deadline
- **THEN** `StartSession` SHALL complete without an RPC timeout
- **AND** the task SHALL remain attached to its worktree

#### Scenario: Pi prelaunch exceeds even the extended per-backend deadline

- **WHEN** Pi prelaunch takes longer than pi's extended per-backend deadline (e.g. a genuinely cold model load)
- **THEN** the RPC call SHALL return an ambiguous-timeout error to the task creator
- **AND** the creator SHALL NOT remove the task's worktree or delete its task row on that error
- **AND** the task SHALL remain in place for the daemon to finish starting it, or for the user to retry

#### Scenario: Pi prelaunch fails

- **WHEN** Pi prelaunch returns a definitive error within its configured budget
- **THEN** the error SHALL reach the task creator
- **AND** the creator SHALL unwind the task, since this is not an ambiguous-timeout case

#### Scenario: A backend with no known slow prelaunch fails fast on an unreachable daemon

- **WHEN** a backend with no per-backend timeout override issues `StartSession` against a daemon that never responds
- **THEN** the RPC SHALL time out at the short default deadline rather than waiting on a budget sized for slow-prelaunch backends

#### Scenario: An ambiguous timeout at an inner RPC hop is not flattened into a definitive error at the outer hop

- **WHEN** the daemon's own `StartSession` call to the supervisor (a second RPC hop, in supervisor mode) times out ambiguously, and that ambiguous outcome reaches the outer caller as an ordinary (non-timeout) RPC response
- **THEN** the outer caller SHALL still recognize the outcome as ambiguous rather than as a definitive failure
- **AND** the task creator SHALL NOT unwind the task on that outcome
