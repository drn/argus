# Task Auto-Naming

## MODIFIED Requirements

### Requirement: Bound the LLM call with a timeout

The system SHALL invoke the LLM name request under a context with a deadline so
the operation cannot run unbounded. The deadline SHALL be sized comfortably
above the observed `claude` CLI cold-start latency under machine load, so a
cold start that coincides with concurrent work is not killed before the model
replies. Because the request runs in a fire-and-forget background goroutine, a
generous deadline carries no user-facing cost.

#### Scenario: LLM call receives a deadline-bound context

- **WHEN** auto-naming invokes the LLM name request
- **THEN** the request is given a context that carries a deadline and the overall operation does not exceed the configured timeout

#### Scenario: Deadline absorbs a cold start under load

- **WHEN** the LLM CLI cold-starts while the machine is under concurrent load and takes substantially longer than a warm call
- **THEN** the deadline is large enough that the call completes rather than being terminated by the deadline
