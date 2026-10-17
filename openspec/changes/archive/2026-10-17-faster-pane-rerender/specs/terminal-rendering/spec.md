## ADDED Requirements

### Requirement: Durable local width reconciliation

The local TUI SHALL persist reconciled widths and outstanding committed-width anchors scoped to the session ID and agent PID. An unchanged reconciled width after relaunch SHALL avoid a redundant kick. Genuine width drift SHALL still use the existing busy, prompt, transcript, and margin guards. Unresolved anchors SHALL only be updated through the anchor-preserving writer, and SHALL survive a viewer relaunch without being replaced by the requested width. Remote TUI SHALL retain its current behavior.

#### Scenario: Reopen at reconciled width

- **WHEN** a local viewer reopens the same session at its persisted reconciled width
- **THEN** the original spawn width SHALL NOT independently trigger another kick

#### Scenario: Reopen with unresolved drift

- **WHEN** a previous kick was deferred and its committed anchor differs meaningfully from the new pane width
- **THEN** the anchor SHALL remain a kick trigger across launches and further deferrals

#### Scenario: Small width changes accumulate

- **WHEN** repeated individually sub-margin pane width changes eventually exceed the margin from the reconciled width
- **THEN** the authored width reference SHALL remain fixed and the accumulated drift SHALL trigger a guarded repair

#### Scenario: Session identity changes

- **WHEN** a task has a different session ID or agent PID from the persisted record
- **THEN** the viewer SHALL ignore the old width record

### Requirement: Bounded rerender stop escalation

Transcript-guarded rerender stops SHALL give the agent three seconds after SIGTERM to flush before escalating a still-running agent to PID-only SIGKILL. Exited processes SHALL cancel escalation; descendants SHALL remain untouched so task dev servers survive the width-repair bounce. Ordinary agent-only stops SHALL retain their behavior, and full task teardown SHALL continue stopping the process group, including MCP helpers. Kick transcript guards SHALL remain in force.

#### Scenario: Agent ignores SIGTERM

- **WHEN** the agent process is still running after the grace period
- **THEN** its PID SHALL receive SIGKILL without signalling its descendants

#### Scenario: Stale daemon or supervisor

- **WHEN** an RPC peer does not implement the bounded rerender stop
- **THEN** the client SHALL log the rejection and use the existing non-escalating agent-only stop without sending the new scope to the old stop verb

### Requirement: In-pane width repair status

The TUI SHALL display a small re-rendering indicator while a pane's repair is deferred, stopping, or restarting. Deferred user prompts SHALL remain readable. The indicator SHALL clear when repair resolves, restart fails, or the pane rebinds.

#### Scenario: Busy agent defers repair

- **WHEN** width repair is deferred because the agent is busy
- **THEN** its pane SHALL show the re-rendering status while leaving terminal content available

#### Scenario: Repair completes

- **WHEN** a restarted session is attached successfully or evaluation finds no repair necessary
- **THEN** the indicator SHALL disappear
