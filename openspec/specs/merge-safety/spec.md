# merge-safety Specification

## Purpose
TBD - created by archiving change add-merge-safety-classifier. Update Purpose after archive.
## Requirements
### Requirement: Tiered merge-safety classification

The system SHALL provide an `internal/mergesafety` classifier that determines, for a given task's repo directory, branch name, and the project's default branch, whether that branch's work is confirmed to have landed in the default branch. The classifier SHALL evaluate two tiers of evidence in order, short-circuiting on the first that succeeds, and SHALL fail closed: any case it cannot positively confirm returns not-safe, and it SHALL NEVER report safe without direct evidence.

#### Scenario: Local branch confirmed merged (Tier A)
- **WHEN** the task's branch still resolves (as a local branch or via a remote-tracking ref) in the repo
- **AND** `git merge-base --is-ancestor <branch> <default>` succeeds against the project's default branch
- **THEN** the classifier reports confirmed-safe via Tier A, without any network call

#### Scenario: Deleted branch confirmed merged via GitHub (Tier B)
- **WHEN** the task's branch no longer resolves locally
- **AND** exactly one plausible merged pull request is found for that exact head ref name (see "Batched merge-candidate lookup")
- **THEN** the classifier reports confirmed-safe via Tier B

#### Scenario: Branch gone with no matching merged PR
- **WHEN** the task's branch no longer resolves locally
- **AND** no pull request with that head ref name is found, or none of the found candidates are merged into the project's default branch
- **THEN** the classifier reports not-confirmed

#### Scenario: Ambiguous branch-name reuse
- **WHEN** more than one plausible merged pull request is found for the same head ref name (i.e. the branch name was reused across unrelated tasks)
- **THEN** the classifier reports not-confirmed rather than guessing which candidate corresponds to this task

#### Scenario: Branch exists but is not an ancestor, with no rescuing PR
- **WHEN** the task's branch still resolves locally
- **AND** it is NOT an ancestor of the project's default branch
- **AND** no exactly-one plausible merged PR is found for it either
- **THEN** the classifier reports not-confirmed

#### Scenario: Unresolvable repo
- **WHEN** the given repo directory does not exist or is not a git repository
- **THEN** the classifier reports not-confirmed with a reason identifying the resolution failure, and performs no git or network operation

### Requirement: Plausibility-guarded merged-PR matching

When Tier B evaluates a candidate merged pull request against a task, it SHALL require ALL of: the candidate's state is merged, the candidate's base ref matches the project's default branch, and the candidate's creation time is not earlier than the task's own creation time (within a small clock-skew allowance). A candidate failing any of these SHALL NOT count as a match.

#### Scenario: Candidate predates the task
- **WHEN** a merged PR is found for the task's branch name, but that PR's creation time is earlier than the task's own creation time
- **THEN** the candidate is rejected as implausible (it belongs to an earlier, different task that reused the same branch name) and does not count toward a Tier B match

#### Scenario: Candidate merged into the wrong branch
- **WHEN** a merged PR is found for the task's branch name, but its base ref is not the project's configured default branch
- **THEN** the candidate is rejected and does not count toward a Tier B match

### Requirement: Batched merge-candidate lookup

The system SHALL fetch Tier B candidates using a batched GitHub GraphQL query per repo, requesting up to 5 most-recently-created pull requests per head ref name (`first: 5, orderBy: {field: CREATED_AT, direction: DESC}`), covering multiple branches from the same repo in a single query the same way the existing PR-status poller batches by repo. It SHALL reuse the existing `gh api graphql` execution primitive (temp-file query, swappable runner test seam, rate-limit-cost parsing) rather than introducing a separate GitHub API client.

#### Scenario: One query per repo for a batch of candidates
- **WHEN** the classifier is asked to evaluate multiple tasks' branches that resolve to the same repo
- **THEN** the system issues a single aliased GraphQL query covering all of those branches for that repo, not one query per branch

#### Scenario: Reuses the existing gh execution primitive
- **WHEN** the batched merge-candidate query is executed
- **THEN** it runs through the same `gh api graphql` temp-file invocation and rate-limit-cost parsing already used by the PR-status poller's batched query, not a separate implementation

### Requirement: Default-branch resolution with fallback

The system SHALL resolve a project's default branch preferring the project's configured branch value when non-empty, and otherwise resolving the remote's HEAD branch, falling back further to a fixed priority list of common default branch names if neither is available.

#### Scenario: Configured branch is used when present
- **WHEN** a project has a non-empty configured default branch
- **THEN** the classifier uses that value directly without probing the remote

#### Scenario: Falls back to remote HEAD when unconfigured
- **WHEN** a project's configured default branch is empty
- **THEN** the classifier resolves the repo's remote HEAD branch (e.g. `origin/HEAD`) and uses that

### Requirement: No network access from Tier A

Tier A evaluation SHALL perform no network operation (no `git fetch`, no GitHub API call) — it evaluates only refs already present in the local repository.

#### Scenario: Tier A never fetches
- **WHEN** Tier A evaluates a branch whose local remote-tracking ref is stale
- **THEN** the classifier does not fetch to refresh it, and may report not-confirmed for a branch that was in fact very recently merged upstream — never the reverse (a stale ref never causes a false confirmed-safe result)

### Requirement: Coordinator-inferred safety for the global Cleanup pass

The global Cleanup compute pass (`internal/api`'s `runCleanupCompute`) SHALL support a bounded, one-hop fallback tier, `coordinator-inferred`, for a candidate task that classified not-safe via the existing Tier A/B evaluation AND belongs to a Hera orchestrator (per its resolved `StuckTaskCandidate.Orchestrator`). This tier exists specifically for a Hera-descended worker task folded into its coordinator's branch via a plain `git merge`, which never had — and can never retroactively be given — a standalone PR of its own, and is therefore structurally unclassifiable by Tier A or Tier B alone.

For each distinct orchestrator among such not-safe, orchestrator-bearing candidates, the system SHALL resolve that orchestrator's coordinator role's currently-bound task exactly once and classify it via the existing Tier A/B classifier (never via this same coordinator-inference logic — no chain of inference through a grandparent orchestrator). When the coordinator's own verdict is confirmed-safe, every one of that orchestrator's not-safe candidates SHALL be overridden to safe, tier `coordinator-inferred`, with a reason naming the coordinator task and citing its own tier and reason. When the coordinator's own verdict is not-safe, or its task cannot be resolved at all, every candidate under that orchestrator SHALL be left exactly as its own Tier A/B verdict reported — this fallback SHALL fail closed, never treating an unresolvable or not-safe coordinator as an error.

This tier SHALL NOT be produced by any Tier-A-only classification path (the single-role nuke, cascade nuke, or clear-archived flows) — resolving a coordinator's own verdict can require a Tier B network call, which those interactive/synchronous paths must never wait on.

#### Scenario: Coordinator confirmed safe rescues its not-safe workers

- **WHEN** a candidate classifies not-safe via Tier A/B, belongs to orchestrator O, and O's coordinator role's bound task classifies confirmed-safe
- **THEN** the candidate's verdict is overridden to safe, tier `coordinator-inferred`, with a reason naming the coordinator task and its own confirming tier/reason

#### Scenario: Coordinator not-safe leaves the candidate unchanged

- **WHEN** a candidate belongs to orchestrator O, and O's coordinator role's bound task itself classifies not-safe
- **THEN** the candidate's verdict remains exactly its own Tier A/B result — not overridden, not treated as an error

#### Scenario: Unresolvable coordinator task leaves the candidate unchanged

- **WHEN** a candidate belongs to orchestrator O, and O has no resolvable coordinator role/binding (e.g. pruned before this feature existed, or the orchestrator name does not resolve at all)
- **THEN** the candidate's verdict remains exactly its own Tier A/B result, with no error raised

#### Scenario: One coordinator classified once regardless of worker count

- **WHEN** two or more not-safe candidates in the same compute pass share the same orchestrator
- **THEN** that orchestrator's coordinator task is resolved and classified exactly once, and the resulting verdict is applied to every one of that orchestrator's candidates

#### Scenario: The inference is capped at one hop

- **WHEN** a not-safe coordinator task, resolved as the coordinator of orchestrator O, is itself associated with a further ("grandparent") orchestrator whose own coordinator would classify safe
- **THEN** the system does not look up or classify that grandparent orchestrator's coordinator, and O's not-safe candidates are NOT rescued by it

#### Scenario: Never produced by a Tier-A-only path

- **WHEN** the single-role nuke, cascade nuke, or clear-archived flow classifies a candidate
- **THEN** the resulting verdict never carries tier `coordinator-inferred`, since those paths never perform the coordinator-inference lookup

