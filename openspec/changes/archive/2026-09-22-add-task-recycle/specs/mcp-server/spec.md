## ADDED Requirements

### Requirement: Task context reset via task_recycle

The system SHALL expose a `task_recycle` tool, gated on both task management (`taskMgmtEnabled`) and a wired recycler (`SetTaskRecycler`) being configured. It SHALL resolve its target task the same way other task tools do (`id`, or `cwd` matched against worktree paths), require a non-empty `handoff_note` capped at 16 KiB, and invoke the task-recycle primitive (see the `task-recycle` capability) with the resolved task ID and handoff note. A successful call SHALL return before the restart happens — it schedules the recycle, it does not wait for it.

#### Scenario: Tool hidden without a wired recycler

- **WHEN** task management is configured but `SetTaskRecycler` has not been called
- **THEN** `task_recycle` does not appear in `tools/list` and calling it by name errors

#### Scenario: Empty handoff_note rejected

- **WHEN** `task_recycle` is called with an empty or whitespace-only `handoff_note`
- **THEN** the call errors without invoking the recycler

#### Scenario: Oversized handoff_note rejected

- **WHEN** `handoff_note` exceeds the configured byte cap
- **THEN** the call errors without invoking the recycler
