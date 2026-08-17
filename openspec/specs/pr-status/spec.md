# pr-status Specification

## Purpose
TBD - created by archiving change add-pr-poll-terminal-skip. Update Purpose after archive.
## Requirements
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

### Requirement: PR review state detection

The system SHALL determine a task branch's open-PR review state by invoking the GitHub CLI (`gh pr view <branch> --json state,isDraft,reviewDecision,url`) with the task's worktree as the working directory, and SHALL collapse the result into a single `PRState` value: `none`, `draft`, `awaiting-review`, `changes-requested`, `approved`, `merged-closed`, or `unknown`. Detection MUST run with a bounded timeout and MUST treat gh output strictly as data (JSON fields only).

#### Scenario: Open PR awaiting review
- **WHEN** `gh pr view` returns an open, non-draft PR whose `reviewDecision` is empty or `REVIEW_REQUIRED`
- **THEN** detection reports `awaiting-review`

#### Scenario: Changes requested
- **WHEN** `gh pr view` returns an open PR whose `reviewDecision` is `CHANGES_REQUESTED`
- **THEN** detection reports `changes-requested`

#### Scenario: Approved
- **WHEN** `gh pr view` returns an open PR whose `reviewDecision` is `APPROVED`
- **THEN** detection reports `approved`

#### Scenario: Draft PR
- **WHEN** `gh pr view` returns an open PR with `isDraft` true
- **THEN** detection reports `draft`

#### Scenario: No PR for the branch
- **WHEN** `gh pr view` exits non-zero indicating no pull request exists for the branch
- **THEN** detection reports `none`

#### Scenario: Merged or closed PR
- **WHEN** `gh pr view` returns a PR whose `state` is `MERGED` or `CLOSED`
- **THEN** detection reports `merged-closed`

#### Scenario: gh unavailable or unauthenticated
- **WHEN** `gh` is not installed or the call fails due to missing authentication
- **THEN** detection reports `unknown` and the condition is logged via uxlog at most once rather than on every poll

### Requirement: TUI task-list indicator

The TUI task list SHALL render an indicator cell immediately after each task's status glyph, showing a distinct glyph and color for `awaiting-review`, `changes-requested`, and `approved`. For all other states the cell MUST be omitted so the task name reclaims that horizontal space. The indicator MUST coexist with the existing status glyph (never replace it).

#### Scenario: Indicator shown for actionable review states
- **WHEN** a task's cached PR state is `awaiting-review`, `changes-requested`, or `approved`
- **THEN** the task row shows the corresponding glyph and color in a cell between the unchanged status glyph and the task name

#### Scenario: Space reclaimed for non-actionable states
- **WHEN** a task's cached PR state is `none`, `draft`, `merged-closed`, or `unknown`
- **THEN** no PR cell is rendered and the task name starts immediately after the status glyph, reclaiming the space the cell would have occupied

### Requirement: Web PWA parity

The REST task representation SHALL include a `pr_state` field populated from the cached `task_meta` `pr` namespace, and the web PWA task list SHALL render a matching badge for actionable review states. The REST handler MUST source `pr_state` from the cache only and MUST NOT invoke the GitHub CLI inline.

#### Scenario: DTO exposes cached state
- **WHEN** a client lists tasks and a task has a cached PR state
- **THEN** the task's JSON includes `pr_state` reflecting the cached value, served without invoking `gh`

#### Scenario: PWA renders badge
- **WHEN** the PWA task list renders a task whose `pr_state` is `awaiting-review`, `changes-requested`, or `approved`
- **THEN** a corresponding PR badge appears for that task

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

### Requirement: A merge transition nudges the task's Hera coordinator

When a poll cycle resolves a task's PR as genuinely MERGED (not merely closed – the cached `state` field persists both as the single terminal value `merged-closed`, so the poller SHALL additionally track the raw merged/closed distinction via `gitutil.PRResult.Merged` to tell them apart) and the task holds (or has ever held) a Hera role, the system SHALL send TWO independent notifications, neither skipped because the other fired:

1. **Coordinator nudge** (unchanged): the system SHALL send that task's resolved orchestrator coordinator a nudge message – never a status change, never an automatic accept/complete. The nudge names the task and includes the PR URL, framed as "worth reviewing/accepting" rather than as a completed fact. This is a SILENT no-op when the PR was closed WITHOUT being merged; when the task never held a Hera role at all; when no coordinator role resolves for that orchestrator; or when the resolved role IS ITSELF the coordinator (its own PR merged – it does not need to be told via this channel).
2. **Role self-assessment notice** (new): the system SHALL ALSO send a message directly to the task's own resolved Hera role (regardless of role kind, and regardless of the task's current status), telling it the PR merged and asking it to decide, using its own judgment, whether it has any further tasks – if not, to inform its coordinator and mark itself complete via its own existing tools. This introduces no new completion primitive and never flips the task's status or hera role-status itself; the daemon only delivers information. This is a SILENT no-op when the task never held a Hera role, when no coordinator role resolves (needed to identify the notice's sender), or when the resolved role IS the coordinator (a message cannot be sent from a role to itself).

The system SHALL resolve the task's most recent Hera role (any binding, live or ended) and that role's orchestrator's coordinator identically for both notifications, mirroring the existing coordinator-resolution pattern the plan-DAG gater already uses for its own hold/fan-in notices.

Because a PR state is permanently excluded from all future polling once it reaches a terminal value (`merged-closed` – see "Cached, non-blocking polling"), a genuine merge is observed by the poller AT MOST ONCE per task, ever, so both notifications fire at most once per task with no additional bookkeeping.

Derived from: `internal/daemon/daemon.go` (`pollPRStatesOnce`, `resolveHeraRoleAndCoordinator`, `notifyCoordinatorOfMergedPR`, `notifyRoleOfMergedPR`), `internal/gitutil/pr_batch.go` (`PRResult.Merged`, `mapBatchNode`), `internal/db/hera_messages.go` (`SendHeraMessage`'s `ErrHeraMessageSelfSend` guard, the reason the role notice cannot reach a coordinator's own directly-bound task).

#### Scenario: A genuine merge transition nudges the coordinator

- **WHEN** a poll cycle resolves a task's PR as MERGED (`PRResult.Merged` true) for a task that holds (or has held) a Hera worker role, and a coordinator resolves for that role's orchestrator
- **THEN** the coordinator role receives a nudge message naming the task and its PR URL; the task's argus status and hera role-status are both left completely unchanged

#### Scenario: A genuine merge transition also asks the role to self-assess

- **WHEN** a poll cycle resolves a task's PR as MERGED (`PRResult.Merged` true) for a task whose most recently bound Hera role is NOT the coordinator itself, and a coordinator resolves for that role's orchestrator
- **THEN** the role receives a message naming the merged PR's URL and instructing it to inform its coordinator and mark itself complete if it has no further tasks; the coordinator's own nudge from the scenario above is also delivered, unaffected

#### Scenario: The role notice fires regardless of task status

- **WHEN** a poll cycle resolves a task's PR as MERGED (`PRResult.Merged` true) for a task whose most recently bound Hera role is worker-kind and whose task status is `in_progress` (not `in_review`)
- **THEN** the role still receives the self-assessment notice; the task's status is not read or used as a gating condition for this notification

#### Scenario: A coordinator's own merged PR gets neither notification

- **WHEN** a poll cycle resolves a task's PR as MERGED (`PRResult.Merged` true) for a task whose most recently bound Hera role IS the coordinator itself
- **THEN** neither the coordinator nudge (self-skipped) nor the role self-assessment notice (blocked by the messaging layer's self-send rejection) is delivered

#### Scenario: An unmerged close never triggers either notification

- **WHEN** a poll cycle resolves a task's PR state to `merged-closed` but `PRResult.Merged` is false
- **THEN** neither the coordinator nudge nor the role self-assessment notice is sent

