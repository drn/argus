## MODIFIED Requirements

### Requirement: Bounded rerender stop escalation

Transcript-guarded width-repair stops SHALL give the agent three seconds after SIGTERM to flush before escalating a still-running agent to SIGKILL. A cooperative exit during that grace SHALL cancel escalation and leave descendants untouched. Once agent SIGKILL succeeds, cleanup SHALL target its captured descendant tree, including children that entered another session, with SIGTERM followed by a short grace and SIGKILL for survivors. Agent-started dev servers MAY be stopped on escalation. Process creation identity SHALL be checked before signalling captured descendants; unrelated processes SHALL NOT be targeted. Cleanup counts and inspection/signal failures SHALL be logged through the production logger. Ordinary agent-only stops, session switches and Hera revive SHALL retain non-escalating behavior; full task teardown SHALL retain process-group cleanup. Kick transcript guards SHALL remain in force.

#### Scenario: Agent ignores SIGTERM

- **WHEN** a width-repair stop leaves the agent running after its three-second grace
- **THEN** the agent SHALL receive SIGKILL and its captured descendants SHALL be stopped with a short TERM-to-KILL grace

#### Scenario: Cooperative agent exit

- **WHEN** the agent exits before escalation
- **THEN** descendant processes SHALL remain untouched

#### Scenario: Detached helper holds the PTY

- **WHEN** a descendant has entered a separate session and retains a PTY slave during escalation
- **THEN** ancestry selection SHALL include it and stopping it SHALL allow PTY drain without waiting for the five-second drain fallback

#### Scenario: Unrelated process or reused PID

- **WHEN** a process is outside the captured tree or has a different creation identity
- **THEN** cleanup SHALL NOT signal it

#### Scenario: Stale daemon or supervisor

- **WHEN** an RPC peer does not implement bounded width repair
- **THEN** the client SHALL log the rejection and use the existing non-escalating agent-only stop without sending the new scope to the old stop verb
