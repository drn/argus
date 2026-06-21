# pr-status Specification

## Purpose
TBD - created by archiving change add-pr-poll-terminal-skip. Update Purpose after archive.
## Requirements
### Requirement: Cached, non-blocking polling

The system SHALL poll PR review state from a daemon-side background loop on a fixed interval, covering only non-archived tasks that have a branch AND whose last-known cached PR state is not terminal (`merged-closed`), and SHALL persist results in the `task_meta` sidecar under namespace `pr` (keys `state` and `url`). Within a cycle the eligible tasks SHALL be grouped by their resolved PR repo and fetched with one batched GraphQL query per repo (see "Batched GraphQL PR-state fetch grouped by repo"), rather than one GitHub CLI invocation per task. A terminal PR state never changes, so once observed it sticks and that task is excluded from all future polls. The terminal-state check MUST read the same persisted `task_meta` cache the poller writes, so the exclusion survives a daemon restart. The UI MUST NOT invoke the GitHub CLI or GraphQL API on its own thread; it reads only the cached value. A transient fetch failure MUST NOT overwrite a previously cached value.

#### Scenario: Skips ineligible tasks
- **WHEN** the poller runs over the task set
- **THEN** archived tasks and tasks without a branch are not polled

#### Scenario: Skips tasks with a terminal cached PR state
- **WHEN** the poller runs over the task set and a non-archived task with a branch has a cached `pr` namespace `state` of `merged-closed`
- **THEN** that task is excluded from the eligible set and is never included in any batched query, and the skip is logged via uxlog

#### Scenario: Still polls tasks with a non-terminal cached state
- **WHEN** a non-archived task with a branch has a cached `pr` namespace `state` that is open/draft/awaiting-review/changes-requested/approved/none/unknown
- **THEN** that task remains eligible and is included in its repo's batched query

#### Scenario: Still polls tasks with no cached state
- **WHEN** a non-archived task with a branch has no cached `pr` namespace `state` row yet
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

### Requirement: Batched GraphQL PR-state fetch grouped by repo

The system SHALL fetch PR state for eligible tasks using batched GitHub GraphQL queries issued through the GitHub CLI (`gh api graphql`), with at most one query per distinct PR repo per poll cycle (subject to a per-query alias cap). Each query SHALL look up PR state by head ref name using aliased `repository(owner,name){ pullRequests(headRefName:…, first:1, orderBy:{field:CREATED_AT,direction:DESC}){ nodes{ state isDraft reviewDecision url } } }` fields, so that state is resolved correctly even when a merged PR's head branch has been deleted. The system SHALL resolve a task's PR repo (`owner/name`) from its cached `task_meta` `pr`/`url` value when present, and otherwise from the worktree's default GitHub repo. When a repo group exceeds the per-query alias cap, the system SHALL split it into multiple sequential queries.

#### Scenario: One query per repo, not per task
- **WHEN** a poll cycle has multiple eligible tasks whose branches resolve to the same repo
- **THEN** the system issues a single GraphQL query covering all of those branches, not one query per task

#### Scenario: Resolves repo from cached PR url
- **WHEN** an eligible task has a cached `task_meta` `pr`/`url` value
- **THEN** the task's PR repo `owner/name` is parsed from that url and used for grouping

#### Scenario: Falls back to worktree default repo
- **WHEN** an eligible task has no cached `pr`/`url` value
- **THEN** the task's PR repo is resolved from the worktree's default GitHub repo

#### Scenario: Resolves state for a deleted merged branch
- **WHEN** a branch's PR was merged and its head branch has since been deleted
- **THEN** the batched query resolves the PR's `merged-closed` state via `pullRequests(headRefName:)` rather than reporting no PR

#### Scenario: Reports no PR as none
- **WHEN** a batched query returns no PR nodes for a branch
- **THEN** that branch's resolved state is `none`

#### Scenario: Chunks oversized repo groups
- **WHEN** a single repo group contains more branches than the per-query alias cap
- **THEN** the system splits the group into multiple sequential queries, each within the cap

#### Scenario: Logs per-cycle poll summary
- **WHEN** a poll cycle completes
- **THEN** the system emits the `[pr] poll: eligible=… skipped=… written=… errored=…` uxlog summary line

