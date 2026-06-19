## MODIFIED Requirements

### Requirement: Cached, non-blocking polling

The system SHALL poll PR review state from a daemon-side background loop on a fixed interval, covering only non-archived tasks that have a branch AND whose last-known cached PR state is not terminal (`merged-closed`), with bounded concurrency, and SHALL persist results in the `task_meta` sidecar under namespace `pr` (keys `state` and `url`). A terminal PR state never changes, so once observed it sticks and that task is excluded from all future polls. The terminal-state check MUST read the same persisted `task_meta` cache the poller writes, so the exclusion survives a daemon restart. The UI MUST NOT invoke the GitHub CLI on its own thread; it reads only the cached value. A transient fetch failure MUST NOT overwrite a previously cached value.

#### Scenario: Skips ineligible tasks
- **WHEN** the poller runs over the task set
- **THEN** archived tasks and tasks without a branch are not polled

#### Scenario: Skips tasks with a terminal cached PR state
- **WHEN** the poller runs over the task set and a non-archived task with a branch has a cached `pr` namespace `state` of `merged-closed`
- **THEN** that task is excluded from the eligible set and no `gh pr view` call is made for it, and the skip is logged via uxlog

#### Scenario: Still polls tasks with a non-terminal cached state
- **WHEN** a non-archived task with a branch has a cached `pr` namespace `state` that is open/draft/awaiting-review/changes-requested/approved/none/unknown
- **THEN** that task remains eligible and is polled

#### Scenario: Still polls tasks with no cached state
- **WHEN** a non-archived task with a branch has no cached `pr` namespace `state` row yet
- **THEN** that task remains eligible and is polled

#### Scenario: Terminal exclusion survives a daemon restart
- **WHEN** the daemon restarts and a task's cached `pr` namespace `state` is `merged-closed`
- **THEN** the first poll after restart reads that persisted value and excludes the task without re-polling it

#### Scenario: Persists successful result
- **WHEN** a poll successfully resolves a branch's PR state
- **THEN** the `state` and `url` are written to `task_meta` namespace `pr` for that task

#### Scenario: Preserves cache on transient failure
- **WHEN** a poll fails with a timeout or network error for a task that already has a cached PR state
- **THEN** the existing `task_meta` value for that task is left unchanged

#### Scenario: Clean shutdown
- **WHEN** the daemon begins shutdown
- **THEN** the poller goroutine stops without blocking shutdown

#### Scenario: Cleared with the task
- **WHEN** a task is deleted or archived
- **THEN** its `pr` namespace `task_meta` rows are removed by the existing meta cleanup
