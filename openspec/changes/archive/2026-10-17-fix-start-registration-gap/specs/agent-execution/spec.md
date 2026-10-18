## ADDED Requirements

### Requirement: Input and resize ride out the session start gap

While a session start is in flight (slot reserved but session not yet published) or a kick-restart is queued, a WriteInput or Resize request for that task SHALL wait, bounded to a few seconds, for the session to be published instead of failing with "session not found". If no start is in flight, or the start fails, the request SHALL fail immediately with "session not found".

#### Scenario: Input immediately after a kick restart
- **WHEN** a client sends input after a kick restart's replacement process has been exec'd but before it is registered
- **THEN** the input is delivered to the replacement session

#### Scenario: No start in flight
- **WHEN** a client sends input for a task with no session and no start in flight
- **THEN** the request fails immediately with "session not found"
