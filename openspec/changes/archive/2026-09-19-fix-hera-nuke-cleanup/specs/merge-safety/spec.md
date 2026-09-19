## ADDED Requirements

### Requirement: Stack-inferred safety for cascade nuke

The cascade-nuke pre-confirm pass (`internal/tui/heraactions.go`'s `heraCascadeNukeFrom`) SHALL support a bounded fallback tier, `stack-inferred`, for a candidate task that classifies not-safe via the existing Tier A check AND is part of a `base_branch` stack — a chain of tasks each branched off the previous (`model.Task.BaseBranch`), where only the last task in the chain ever opens a standalone PR. This tier exists specifically for an earlier link in such a stack, which never had — and can never retroactively be given — a standalone PR of its own, and whose ancestry against the project's default branch is severed entirely once the chain's tip is squash-merged (the squashed commit on the default branch is never a literal ancestor of anything in the chain).

For a not-safe candidate task X, the system SHALL walk the stack forward from X — resolving, at each step, the task Y (if any) whose `BaseBranch` equals the current task's `Branch` — until it reaches a task Z with no further descendant. It SHALL then classify Z via the existing Tier A/B classifier (`mergesafety.Classify`, which may issue a Tier B GitHub call when Z's local ancestry check alone cannot confirm it — see "No network access from Tier A", which is unaffected: Tier A itself still never touches the network, this classification of Z simply is not restricted to Tier A the way the per-task cascade count is). When Z classifies confirmed-safe, the system SHALL additionally verify, via a purely local `git merge-base --is-ancestor` check against Z's own branch (not the default branch), that X is a real ancestor of Z. Only when both hold — Z confirmed safe, and X a real ancestor of Z — SHALL X be marked safe, tier `stack-inferred`, with a reason naming Z and citing Z's own confirming tier. Any break in the chain (no resolvable descendant, a descendant that itself cannot be classified safe, or a failed/erroring ancestry check) SHALL leave X exactly as its own Tier A verdict reported — this tier fails closed, never guessing safe without both a confirmed tip and a verified local ancestry chain to it.

This tier SHALL be produced ONLY by the cascade-nuke pre-confirm pass — never by the single-role nuke review, the clear-archived sweep, or the global Cleanup pass's `coordinator-inferred` fallback, each of which has its own, separate safety-inference scope. Unlike Tier A/the existing per-task cascade count, resolving Z MAY issue a bounded, batched GitHub call (at most one classification per distinct stack tip in the cascade, never one per branch) — a deliberate, scoped exception to this codebase's general "an interactive nuke confirm never waits on the network" rule, accepted specifically for cascade nuke (a "wrap up this whole feature" action, not a frequent single-agent keypress).

#### Scenario: Earlier stack link rescued by its confirmed-merged tip

- **WHEN** task X classifies not-safe via Tier A, `BaseBranch` resolves a chain X → Y → Z with no further descendant of Z, and Z classifies confirmed-safe via Tier A or Tier B
- **AND** X is confirmed a real git ancestor of Z's own branch
- **THEN** X's verdict is overridden to safe, tier `stack-inferred`, naming Z and Z's own confirming tier/reason

#### Scenario: Squash-merged tip still rescues its earlier links

- **WHEN** the chain's tip Z's PR was squash-merged (Z is not a local ancestor of the default branch, but its PR's GitHub state is `MERGED`)
- **THEN** Z classifies confirmed-safe via Tier B despite failing Tier A, and every earlier link that is a real ancestor of Z's own branch is still rescued via `stack-inferred`

#### Scenario: A broken chain (rebase or cherry-pick) is not rescued

- **WHEN** task X's stack walk resolves a tip Z that classifies confirmed-safe, but X is NOT a real git ancestor of Z's own branch (e.g. X's work was cherry-picked into Z rather than Z being branched from X)
- **THEN** X's verdict remains exactly its own Tier A result — not overridden, fails closed

#### Scenario: An unconfirmed tip leaves the whole chain unrescued

- **WHEN** task X's stack walk resolves a tip Z, and Z itself classifies not-safe (no merged PR found, or ambiguous branch-name reuse)
- **THEN** X's verdict remains exactly its own Tier A result, with no error raised

#### Scenario: One tip classified once regardless of chain length or candidate count

- **WHEN** two or more not-safe candidates in the same cascade resolve to the same stack tip Z
- **THEN** Z is classified exactly once, and the resulting verdict is applied to every candidate whose ancestry check against Z succeeds

#### Scenario: Never produced outside the cascade-nuke path

- **WHEN** the single-role nuke review, the clear-archived sweep, or the global Cleanup pass classifies a candidate
- **THEN** the resulting verdict never carries tier `stack-inferred`, since none of those paths perform the stack-walk resolution
