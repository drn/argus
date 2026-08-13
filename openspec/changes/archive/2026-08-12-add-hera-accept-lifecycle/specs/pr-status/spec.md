## ADDED Requirements

### Requirement: A merge transition nudges the task's Hera coordinator

When a poll cycle resolves a task's PR as genuinely MERGED (not merely closed – the cached `state` field persists both as the single terminal value `merged-closed`, so the poller SHALL additionally track the raw merged/closed distinction via `gitutil.PRResult.Merged` to tell them apart) and the task holds (or has ever held) a Hera role, the system SHALL send that task's resolved orchestrator coordinator a nudge message – never a status change, never an automatic accept/complete. The nudge names the task and includes the PR URL, framed as "worth reviewing/accepting" rather than as a completed fact, since a merged PR is a strong signal but not proof the work is fully done (squash merges and folded-in workers with no PR of their own make "confirmed merged" genuinely ambiguous for Hera-descended tasks – see the `merge-safety` capability).

The system SHALL resolve the task's most recent Hera role (any binding, live or ended) and that role's orchestrator's coordinator role, mirroring the existing coordinator-resolution pattern the plan-DAG gater already uses for its own hold/fan-in notices. This is a SILENT no-op – no error, no log spam – in each of the following cases: the PR was closed WITHOUT being merged (Merged is false even though the cached state still reads `merged-closed`); the task never held a Hera role at all (the common case, since most PR-tracked tasks are not Hera-bound); no coordinator role resolves for that orchestrator; or the resolved role IS ITSELF the coordinator (its own PR merged – it does not need to be told via this channel).

Because a PR state is permanently excluded from all future polling once it reaches a terminal value (`merged-closed` – see "Cached, non-blocking polling"), a genuine merge is observed by the poller AT MOST ONCE per task, ever, so this nudge fires at most once per task with no additional bookkeeping.

Derived from: `internal/daemon/daemon.go` (`pollPRStatesOnce`, the nudge added to its write loop), `internal/gitutil/pr_batch.go` (`PRResult.Merged`, `mapBatchNode`), `internal/hera/accept.go` (the sibling `AcceptRole` primitive this nudge deliberately does NOT call).

#### Scenario: A genuine merge transition nudges the coordinator

- **WHEN** a poll cycle resolves a task's PR as MERGED (`PRResult.Merged` true) for a task that holds (or has held) a Hera worker role, and a coordinator resolves for that role's orchestrator
- **THEN** the coordinator role receives a nudge message naming the task and its PR URL; the task's argus status and hera role-status are both left completely unchanged

#### Scenario: An unmerged close never nudges

- **WHEN** a poll cycle resolves a task's PR as CLOSED without being merged (`PRResult.Merged` false, even though the cached `state` still persists as `merged-closed`)
- **THEN** no nudge is sent, regardless of whether the task holds a Hera role

#### Scenario: A non-Hera task never nudges

- **WHEN** a poll cycle resolves a genuine merge for a task that has never held any Hera role
- **THEN** no nudge is sent and no error or log noise is produced

#### Scenario: No resolvable coordinator is a silent no-op

- **WHEN** a poll cycle resolves a genuine merge for a Hera-bound task whose orchestrator has no coordinator role (or no coordinator resolves for any reason)
- **THEN** no nudge is sent and no error is produced

#### Scenario: A coordinator's own merged PR does not self-nudge

- **WHEN** a poll cycle resolves a genuine merge for a task whose most recently resolved Hera role IS the coordinator role itself
- **THEN** no nudge is sent (a coordinator is not notified of its own PR merging via this channel)

#### Scenario: The nudge never fires twice for the same task

- **WHEN** a task's cached PR state is already terminal (`merged-closed`) from a prior poll cycle and a later cycle runs
- **THEN** the task is excluded from that cycle's eligible set entirely (per the existing terminal-state skip) and no second nudge is sent
