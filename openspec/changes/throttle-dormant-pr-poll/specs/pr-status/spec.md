## MODIFIED Requirements

### Requirement: Cached, non-blocking polling

The system SHALL poll PR review state from a daemon-side background loop, covering only non-archived tasks that have a branch AND whose last-known cached PR state is not terminal (`merged-closed`), and SHALL persist results in the `task_meta` sidecar under namespace `pr` (keys `state` and `url`). The per-task polling cadence within that loop is tiered by dormancy (see "Dormancy-tiered poll cadence") rather than a single fixed interval. Within a cycle the tasks selected for polling SHALL be grouped by their resolved PR repo and fetched with one batched GraphQL query per repo (see "Batched GraphQL PR-state fetch grouped by repo"), rather than one GitHub CLI invocation per task. A terminal PR state never changes, so once observed it sticks and that task is excluded from all future polls. The terminal-state check MUST read the same persisted `task_meta` cache the poller writes, so the exclusion survives a daemon restart. The UI MUST NOT invoke the GitHub CLI or GraphQL API on its own thread; it reads only the cached value. A transient fetch failure MUST NOT overwrite a previously cached value.

#### Scenario: Skips ineligible tasks
- **WHEN** the poller runs over the task set
- **THEN** archived tasks and tasks without a branch are not polled

#### Scenario: Skips tasks with a terminal cached PR state
- **WHEN** the poller runs over the task set and a non-archived task with a branch has a cached `pr` namespace `state` of `merged-closed`
- **THEN** that task is excluded from the eligible set and is never included in any batched query, and the skip is logged via uxlog

#### Scenario: Still polls tasks with a non-terminal cached state
- **WHEN** a non-archived task with a branch has a cached `pr` namespace `state` that is open/draft/awaiting-review/changes-requested/approved/none/unknown AND its dormancy/PR-state cadence selects it this cycle
- **THEN** that task remains eligible and is included in its repo's batched query

#### Scenario: Still polls tasks with no cached state
- **WHEN** a non-archived task with a branch has no cached `pr` namespace `state` row yet AND its dormancy cadence selects it this cycle
- **THEN** that task remains eligible and is included in its repo's batched query

#### Scenario: Terminal exclusion survives a daemon restart
- **WHEN** the daemon restarts and a task's cached `pr` namespace `state` is `merged-closed`
- **THEN** the first poll after restart reads that persisted value and excludes the task without re-fetching it

#### Scenario: Persists successful result
- **WHEN** a batched query successfully resolves a branch's PR state
- **THEN** the `state` and `url` are written to `task_meta` namespace `pr` for that task, including writing a `none` state when the branch has no associated PR

#### Scenario: Preserves cache on transient failure
- **WHEN** a repo's batched query fails with a timeout or network error and a task in that group already has a cached PR state
- **THEN** the existing `task_meta` value for that task is left unchanged

#### Scenario: Clean shutdown
- **WHEN** the daemon begins shutdown
- **THEN** the poller goroutine stops without blocking shutdown

#### Scenario: Cleared with the task
- **WHEN** a task is deleted or archived
- **THEN** its `pr` namespace `task_meta` rows are removed by the existing meta cleanup

## ADDED Requirements

### Requirement: Dormancy-tiered poll cadence

The system SHALL select, each poll cycle, which eligible tasks to query based on a per-task cadence, so branches that change rarely are queried far less often than active ones. This conserves the GitHub GraphQL budget, which is cost-based (≈ 1 unit per branch resolved) rather than request-based, so batching alone does not bound it.

The cadence tier SHALL be derived from the task's most recent lifecycle timestamp — the latest of `ended_at`, `started_at`, and `created_at` — as: within the last 1h → every cycle; 1h to 24h → every 5th cycle; 24h to 7d → every 15th cycle; older than 7d → every 30th cycle.

A branch whose last-known cached PR state is an open PR (`draft`, `awaiting-review`, `changes-requested`, or `approved`) SHALL be polled every cycle regardless of dormancy tier, so externally-driven review or merge transitions surface promptly.

Eligible tasks NOT selected in a given cycle SHALL be skipped without issuing any GitHub query and without altering their cached `task_meta` state. Selection across cycles SHALL be spread (e.g. by task-id hash) so each cycle polls a roughly constant fraction of each tier rather than all of a tier's tasks landing on the same cycle.

#### Scenario: Active branch polled every cycle
- **WHEN** an eligible task's most recent lifecycle timestamp is within the last hour
- **THEN** it is selected for polling on every cycle

#### Scenario: Dormant PR-less branch polled at the frozen tier
- **WHEN** an eligible task has no open PR (cached state `none`/`unknown`) and its most recent lifecycle timestamp is older than 7 days
- **THEN** it is selected for polling at most once every 30 cycles, and on the cycles it is not selected the system issues no GitHub query for it

#### Scenario: Open PR overrides dormancy
- **WHEN** an eligible task's most recent lifecycle timestamp is older than 7 days but its cached PR state is `awaiting-review`
- **THEN** it is selected for polling on every cycle

#### Scenario: Unselected eligible task makes no query and keeps its cache
- **WHEN** an eligible dormant task is not selected this cycle
- **THEN** no GraphQL query is issued for it and its existing `task_meta` `pr` value is unchanged

#### Scenario: Selection is spread across cycles
- **WHEN** two eligible tasks share the same dormancy tier (stride greater than 1)
- **THEN** they are not necessarily selected on the same cycles — selection is distributed across the stride window rather than all firing together

### Requirement: Operator poll kill-switch

The system SHALL support pausing the PR-status poller via a sentinel file (`pr-poller.disabled`) in the data directory. While the sentinel exists, every poll cycle SHALL be skipped before any database read or GitHub query, and the skip SHALL be logged. Removing the sentinel SHALL resume polling on the next cycle. Toggling the sentinel MUST NOT require a daemon restart.

#### Scenario: Sentinel pauses the poller
- **WHEN** the `pr-poller.disabled` sentinel file exists in the data dir at the start of a poll cycle
- **THEN** the cycle issues zero GitHub queries, writes no `task_meta`, and logs that it was paused

#### Scenario: Removing the sentinel resumes polling
- **WHEN** the sentinel file is removed
- **THEN** the next poll cycle proceeds normally, with no daemon restart required
