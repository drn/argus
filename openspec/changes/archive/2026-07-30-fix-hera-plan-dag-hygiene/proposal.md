## Why

Three independently-verified hera plan-DAG / rail data-hygiene bugs were found live in `~/.argus/data.sql`, each a different failure mode with a different root cause:

- **Bug A**: `heragater` polls planned nodes forever even after their orchestrator has been archived or nuked. `ListHeraPlannedNodes` filters only on the node's own `archived_at`/`cancelled_at`, never the parent orchestrator's. Two live rows (roles 343 and 358) have retried materialization every ~60s tick for over a month behind orchestrators archived+nuked on 2026-06-20, spamming daemon.log with `hold: no coordinator to ping: <nil>`.
- **Bug B**: a planned node (role 184, "2a-team" under live orchestrator "sherlock-mvp") has a blank `argus_project`, so `agent.CreateAndStart` fails materialization every tick, forever, with zero operator visibility — it just retries silently. This node has no remaining blockers and is genuine pending work, not dead data, so it must not be auto-cancelled or guessed at.
- **Bug C**: two freelance roles (813, 814) render as orphaned in the Hera rail's flat top-level Freelance section even though their role→orchestrator→binding→task chain is fully intact — they were simply never archived once their work finished (a `kind=freelance` role only nests inside its orchestrator when both it and the orchestrator are archived-or-not in sync; `kind=worker` roles always nest regardless). This is confirmed data-hygiene, not a display defect — the identical "task complete, binding still open" shape is the NORMAL, common case for ~150 other historical worker roles that render fine because they nest unconditionally.

This change fixes the two genuine code defects (A and B) and separately cleans up the specific broken rows already sitting in the live DB (A's two orphaned planned nodes, C's two un-archived freelance roles + their stale-open bindings). Node 184 (Bug B) is left untouched — nobody knows what project it should target, so it is flagged back to the human rather than resolved here.

## What Changes

- `ListHeraPlannedNodes` (`internal/db/hera_plan.go`) additionally excludes any planned node whose parent orchestrator has `archived_at` or `nuked_at` set — a defensive filter that also retroactively silences existing broken rows without needing a data migration to take effect.
- `ArchiveHeraOrchestrator` and `NukeHeraOrchestrator` (`internal/db/hera.go`) cascade-cancel (`cancelled_at`) their still-planned (never-materialized) child roles, so a future archive/nuke does not orphan pollable dead nodes in the first place.
- `heragater.materializeNode` (`internal/heragater/heragater.go`) tracks consecutive materialization failures per planned node and, after a bounded threshold, sends a one-time escalation notice to the coordinator instead of retrying silently forever — mirroring the existing `agent.EscalateParkedSelection` consecutive-tick-escalation shape used elsewhere in Hera. It never auto-cancels or guesses a fix for the failing node.
- One-shot data cleanup (not shipped as application code) against the live `~/.argus/data.sql`, run through the daemon's own `*db.DB` connection: cancel planned nodes 343 and 358 (Bug A), archive freelance roles 813 and 814 and close their live bindings 803/804 with an explicit end reason (Bug C). Node 184 (Bug B) is explicitly excluded from this cleanup.

## Capabilities

### Modified Capabilities

- `task-orchestration`: the planned-node listing requirement gains a parent-orchestrator liveness condition (Bug A), and the gater materialization requirement gains a bounded-escalation-on-repeated-failure behavior (Bug B).

## Impact

- `internal/db/hera_plan.go`, `internal/db/hera.go`, `internal/heragater/heragater.go`, plus new/updated tests in `internal/db/hera_plan_test.go`, `internal/db/hera_test.go`, `internal/heragater/heragater_test.go`.
- `context/knowledge/gotchas/orchestration.md` gains a new invariant entry.
- No schema change — `cancelled_at`, `archived_at`, `nuked_at` are pre-existing nullable columns.
- Bug C (freelance-role display) requires no code change — see design.md for why.
