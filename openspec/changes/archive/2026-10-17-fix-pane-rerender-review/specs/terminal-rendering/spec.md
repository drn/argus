## ADDED Requirements

### Requirement: Unresolved width anchors survive intermediate no-drift binds

No-drift evaluation and successful stop SHALL preserve unresolved committed-width anchors in memory. Only a successful new session start SHALL clear the outstanding anchor. The TUI SHALL NOT persist or override the session's spawn width with a redundant per-task reconciled-width record.

#### Scenario: Intermediate Skip before returning narrow

- **WHEN** an 80-column session defers at 100 columns, skips at 92 columns, and is then viewed at 80 columns
- **THEN** the 100-column anchor SHALL survive and cause the final evaluation to request repair

### Requirement: Fresh spawns use recent TUI pane dimensions

Fresh headless tasks and Hera workers/coordinators SHALL fill omitted PTY dimensions from the appropriate recently reported local TUI pane size. Explicit dimensions SHALL take precedence. Missing, invalid, or expired reports SHALL preserve the existing 24-row, 80-column defaults.

#### Scenario: Fresh coordinator is shown on Projects

- **WHEN** the TUI reports a 98-column coordinator pane and a fresh coordinator is created without explicit PTY dimensions
- **THEN** the session SHALL start at 98 columns so first attachment does not owe the measured 80-to-98-column repair

#### Scenario: No recent TUI report

- **WHEN** no valid report exists or the report has expired
- **THEN** omitted dimensions SHALL use the existing 24×80 defaults

#### Scenario: Explicit dimensions

- **WHEN** the caller supplies a PTY dimension explicitly
- **THEN** the TUI report SHALL NOT override that dimension

#### Scenario: Report has no role-specific pane

- **WHEN** a valid recent report includes only active-pane geometry
- **THEN** a fresh Hera task SHALL use that geometry for omitted dimensions

### Requirement: Pane-size publication does not block UI callbacks

Pane-layout SQL writes SHALL run on an ordered background worker. Unchanged layouts SHALL be refreshed at a bounded heartbeat rather than written on every frame, and newer queued layout SHALL NOT be overwritten by an older write.

#### Scenario: Repeated redraw with unchanged geometry

- **WHEN** frames redraw within the publication heartbeat with no pane-size change
- **THEN** the TUI SHALL NOT issue another SQL write

### Requirement: Only width repairs opt into bounded kick stops

Session switches and Hera revive SHALL retain ordinary non-escalating agent-only stops. Explicit width repairs SHALL use the bounded repair stop after their existing guards. Grace, cancellation, escalation, and old-peer fallback events SHALL be visible in daemon/supervisor logs.

#### Scenario: Switch or revive uses kick machinery

- **WHEN** a session switch or Hera revive queues a kick restart
- **THEN** it SHALL NOT acquire width-repair SIGKILL escalation

#### Scenario: Deferred main-view repair

- **WHEN** the main view defers a repair without a scheduled retry
- **THEN** its border SHALL show “Re-render pending” rather than claim re-rendering is active

## MODIFIED Requirements

### Requirement: Bounded rerender stop escalation

Transcript-guarded width-repair stops SHALL give the agent three seconds after SIGTERM to flush before escalating a still-running agent to PID-only SIGKILL. Exited processes SHALL cancel escalation; descendants SHALL remain untouched so task dev servers survive the width-repair bounce. Ordinary agent-only stops, session switches, and Hera revive SHALL retain non-escalating behavior, and full task teardown SHALL continue stopping the process group, including MCP helpers. Kick transcript guards SHALL remain in force.

#### Scenario: Agent ignores SIGTERM

- **WHEN** a width-repair stop leaves the agent process running after the grace period
- **THEN** its PID SHALL receive SIGKILL without signalling its descendants

#### Scenario: Stale daemon or supervisor

- **WHEN** an RPC peer does not implement bounded width repair
- **THEN** the client SHALL log the rejection and use the existing non-escalating agent-only stop without sending the new scope to the old stop verb

### Requirement: In-pane width repair status

The TUI SHALL display a small border indicator while a pane's repair is deferred, stopping, or restarting. A deferral without an automatic retry SHALL say “Re-render pending”; an active repair or scheduled retry SHALL say “Re-rendering…”. Terminal content, including deferred user prompts, SHALL remain readable. The indicator SHALL clear when repair resolves, restart fails, the session exits cleanly, or the pane rebinds, including inactive Hera panes.

#### Scenario: Busy agent defers repair

- **WHEN** width repair is deferred because the agent is busy
- **THEN** its pane SHALL show the appropriate border status without covering terminal content

#### Scenario: Repair completes

- **WHEN** a restarted session is attached successfully or evaluation finds no repair necessary
- **THEN** the indicator SHALL disappear

## REMOVED Requirements

### Requirement: Durable local width reconciliation

**Reason**: A reconciled width equal to InitialPTYSize adds no decision value; the attempted record can clobber unresolved anchors and does not prevent the measured fresh-spawn kicks.

**Migration**: Use the actual session spawn width and the existing in-memory unresolved anchor. New fresh sessions use a recent TUI pane-size report as their launch preference.
