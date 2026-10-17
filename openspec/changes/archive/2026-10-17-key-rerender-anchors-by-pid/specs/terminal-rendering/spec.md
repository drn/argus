## MODIFIED Requirements

### Requirement: Unresolved width anchors survive intermediate no-drift binds

No-drift evaluation and successful stop SHALL preserve unresolved committed-width anchors in memory for the same live session PID. The anchor SHALL be keyed to SessionHandle.PID(), not the task row's potentially stale AgentPID. When a replacement PID binds, the TUI SHALL discard the old anchor and invalidate cached attach decisions before evaluating the new process's own spawn width. A successful new session start or task deletion SHALL clear anchor identity and width; a failed start SHALL preserve both. The TUI SHALL NOT persist or override spawn width with redundant per-task reconciled-width metadata. Async decisions superseded by another process binding SHALL NOT restore an old-process anchor or stop a replacement process.

#### Scenario: Intermediate Skip before returning narrow

- **WHEN** the same 80-column process defers at 100 columns, skips at 92 columns, and is then viewed at 80 columns
- **THEN** its 100-column anchor SHALL survive and cause the final evaluation to request repair

#### Scenario: External Hera revive or REST resume replaces the process

- **WHEN** a task with an unresolved anchor restarts outside TUI startSession and later binds with a different live PID
- **THEN** the old anchor SHALL be discarded even if the task row still contains its old PID
- **AND** an unchanged new spawn width SHALL NOT trigger a repair from that old anchor

#### Scenario: New process has genuine width drift

- **WHEN** a replacement process binds at a cached pane width that differs meaningfully from its own spawn width
- **THEN** invalidation of the previous process's attach cache SHALL allow the guarded repair predicate to run

#### Scenario: Superseded async decision

- **WHEN** an old-process probe completes after a different PID binds
- **THEN** its callback SHALL NOT change the replacement's anchor or repair state
