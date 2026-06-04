# pr-status

## ADDED Requirements

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

### Requirement: Cached, non-blocking polling

The system SHALL poll PR review state from a daemon-side background loop on a fixed interval, covering only non-archived tasks that have a branch, with bounded concurrency, and SHALL persist results in the `task_meta` sidecar under namespace `pr` (keys `state` and `url`). The UI MUST NOT invoke the GitHub CLI on its own thread; it reads only the cached value. A transient fetch failure MUST NOT overwrite a previously cached value.

#### Scenario: Skips ineligible tasks
- **WHEN** the poller runs over the task set
- **THEN** archived tasks and tasks without a branch are not polled

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

### Requirement: TUI task-list indicator

The TUI task list SHALL render a reserved indicator cell immediately after each task's status glyph, showing a distinct glyph and color for `awaiting-review`, `changes-requested`, and `approved`, and rendering blank for all other states. The cell MUST coexist with the existing status glyph (never replace it) and MUST always occupy its reserved width so the task name column does not shift as PR state changes.

#### Scenario: Indicator shown for actionable review states
- **WHEN** a task's cached PR state is `awaiting-review`, `changes-requested`, or `approved`
- **THEN** the task row shows the corresponding glyph and color in the reserved cell beside the unchanged status glyph

#### Scenario: Blank cell for non-actionable states
- **WHEN** a task's cached PR state is `none`, `draft`, `merged-closed`, or `unknown`
- **THEN** the reserved cell is blank and the task name column is positioned identically to a task with an actionable PR state

### Requirement: Web PWA parity

The REST task representation SHALL include a `pr_state` field populated from the cached `task_meta` `pr` namespace, and the web PWA task list SHALL render a matching badge for actionable review states. The REST handler MUST source `pr_state` from the cache only and MUST NOT invoke the GitHub CLI inline.

#### Scenario: DTO exposes cached state
- **WHEN** a client lists tasks and a task has a cached PR state
- **THEN** the task's JSON includes `pr_state` reflecting the cached value, served without invoking `gh`

#### Scenario: PWA renders badge
- **WHEN** the PWA task list renders a task whose `pr_state` is `awaiting-review`, `changes-requested`, or `approved`
- **THEN** a corresponding PR badge appears for that task
