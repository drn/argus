## Context

The gater evaluates all blocker role statuses before materializing a node. Once every blocker is `done`, `materializeNode` re-fetches the blocker IDs and `resolveBaseBranch` looks up each blocker's task branch through its live binding or latest ended binding. It currently retains only the winning branch and uses the remaining branch lookups later for a coordinator-facing notice.

That notice does not help the newly started worker unless the coordinator receives the message and relays it. The worker's initial prompt is assembled in the same materialization function, so the branch context can be delivered deterministically at spawn time.

## Goals / non-goals

**Goals:**

- Give every ordinary fan-in worker the name and resolved branch of each blocker in its initial prompt.
- Reuse one blocker-branch resolution path for base selection, worker context, and coordinator notification.
- Preserve current gating, base-branch selection, and best-effort coordinator notification behavior.

**Non-goals:**

- Automatically merge sibling branches.
- Add a new MCP read surface for blocker state; materialization occurs only after all blockers are done, so the initial prompt covers the confirmed failure mode without API expansion.
- Change sub-coordinator materialization, whose fan-in notification and acceptance lifecycle are already explicitly out of scope.

## Decisions

### Resolve blocker branch metadata once during materialization

Introduce a small internal blocker-branch descriptor and resolve the live-or-latest binding plus task branch once for each blocker. Use that result to choose the highest-binding-ID base branch, render the worker prompt section, and format the existing coordinator notice. This prevents the three consumers from disagreeing after repeated database reads.

Alternative considered: call the existing `roleBranch` helper while constructing the prompt. That is smaller locally but repeats lookups already performed by `resolveBaseBranch` and can produce a mixed snapshot if bindings change between reads.

### Add context only for true fan-in workers

Append a `## Your blockers' branches` section between the check-in orientation and stored mission when there are two or more blockers. Each bullet contains the blocker role name and branch; the chosen worktree base is identified explicitly. Root and single-blocker prompts remain unchanged.

Alternative considered: include the section for every non-root worker. Single-blocker workers already start on their only upstream branch, so the added prompt text would carry no new information.

### Do not add an MCP read tool

The gater cannot materialize a fan-in node until all blockers are `done`, and their final task branches are therefore known at prompt construction. Retrying a failed materialization rebuilds the prompt from current database state. A new external surface would add frontend/API and authorization obligations without improving the common case targeted here.

## Risks / trade-offs

- [A blocker has no resolvable task branch] → Include the blocker by name with an explicit unavailable marker so the worker knows the map is incomplete; preserve existing fallback behavior for base selection.
- [A branch changes after `done`] → The prompt is a materialization-time snapshot, matching the branch used to create the worktree and the existing coordinator notice.
- [Prompt size grows with fan-in width] → The section is one compact bullet per blocker and only appears for two or more blockers.

## Migration plan

No data migration is required. Deploying the daemon changes future fan-in materializations; rollback restores the prior prompt without altering stored plan data.

## Open questions

None.
