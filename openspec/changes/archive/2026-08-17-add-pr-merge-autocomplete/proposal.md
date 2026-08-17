## Why

When a hera-bound task's PR is genuinely merged, the daemon-side PR poller today only tells the task's coordinator ("worth reviewing/accepting") — the worker itself never hears about it, and closing the task out still requires a human coordinator to act, even though the worker already has the tools (`hera_send`, `task_complete`) to close itself out. A merged PR is exactly the moment a worker should be prompted to check whether it has any further work, and finish up on its own if not — with no mechanical daemon-side judgment call about what "done" means.

## What Changes

- The daemon-side PR poller (`internal/daemon.pollPRStatesOnce`) additionally sends a message directly to the task's own Hera role (not just its coordinator) the moment its PR resolves as genuinely merged (`gitutil.PRResult.Merged`, not merely the collapsed `merged-closed` state). The message tells the role its PR merged and asks it to decide, using its own judgment, whether it has further tasks — if not, to inform its coordinator and mark itself complete via its own existing `task_complete` tool.
- This introduces **no new completion primitive and no automatic status flip**. The daemon only delivers information; the agent decides and acts using tools it already has.
- The existing coordinator nudge (`notifyCoordinatorOfMergedPR`, "worth reviewing/accepting") is **unchanged and fires independently** — both notifications are additive; neither is skipped because the other fired.
- No new gating on role kind or task status: the new role-notify fires for worker, coordinator, or freelance roles alike, and regardless of whether the task is `in_progress` or `in_review` — the agent's own judgment is the only gate, not a status or role-kind precondition. (One structural exception: a coordinator's own directly-bound task can never receive the role-notify via the hera_messages channel, since `db.SendHeraMessage` hard-rejects a message from a role to itself — an accepted limitation, not a design choice.)
- **Supersedes an earlier design** explored in this same change: an initial draft had the daemon auto-complete a worker's task via the shared `internal/hera.AcceptRole` primitive (mirroring `hera_accept`/the plan-DAG gater), narrowly gated on worker-kind + exact `in_review` status. That approach was set aside in favor of this one after review: rather than the daemon mechanically inferring "done" from structural signals (a prior explicit decision recorded in `context/knowledge/gotchas/orchestration.md` had already judged this genuinely ambiguous for Hera-descended tasks — squash merges, folded-in workers with no PR of their own), this hands the decision to the agent that actually has the context.

## Capabilities

### Modified Capabilities
- `pr-status`: the "A merge transition nudges the task's Hera coordinator" requirement gains a second, independent notification — the same merge trigger also asks the task's own Hera role to self-assess and self-close via its own tools.

## Impact

- `internal/daemon/daemon.go`: `pollPRStatesOnce`'s merge branch gains a call to a new `notifyRoleOfMergedPR` function, alongside the existing (unchanged) `notifyCoordinatorOfMergedPR`. A small shared `resolveHeraRoleAndCoordinator` helper factors out the role/coordinator resolution both now use.
- `internal/daemon/pr_poll_test.go`: new tests cover the role-notify path (worker, coordinator-as-role, in_progress vs in_review, never-fires-twice, silent no-ops), alongside the existing unchanged coordinator-nudge tests.
- `context/knowledge/gotchas/orchestration.md`: documents the new role-notify trigger and the self-send structural limitation.
- No API, schema, or MCP tool surface changes — no new tools, no status-flip primitive; the daemon only sends an additional hera message.
