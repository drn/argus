# Proposal: Hera plan-DAG substrate

## Why

Retiring the `depends_on` DAG did not replace Argus's record of *what blocks what* — it relocated that structure into the coordinator LLM's context window, where it is ephemeral, forgettable, and parallel only as far as the coordinator remembers to fan out. For a large, well-understood phased project (phase 1 → {phase 2 ∥ phase 3}), the order of operations is known up front and should be a durable, inspectable artifact, not something an agent holds in working memory across compactions and daemon bounces.

## What Changes

- **Un-retire the `task-orchestration` capability** as the home for a hera-native plan-DAG. (Not BREAKING: the old `depends_on` surface — `task_link`/`unlink`/`deps`/`halt_downstream`/`set_plan_slug`, `/api/dag`, the standalone DAG tab, the SPA DAG view — is already gone and is NOT being resurrected.)
- **Planned nodes (node ≠ agent).** Add a path that creates a hera role with **no live binding** — a durable, declarative plan node costing one DB row, with no agent, worktree, or inbox until it materializes.
- **Blocking edges.** Add a `hera_blocks(blocked_role_id, blocker_role_id)` table, cycle-checked on insert via DFS, scoped to a single orchestrator in v1.
- **The gater.** A hera-native watcher (the retired `depswatcher` reborn) that materializes a planned node into a live worker — via the existing `agent.CreateAndStart` against the pre-created role — the instant all of its blockers reach `done`.
- **Mechanical gate, dialogue steering.** The daemon spawns on unblock with no LLM in the loop; the materialized worker boots with a standing order to **check in with its coordinator and poll `hera_inbox`** for go/wait before doing real work (worker-pulled, never a passively-awaited push).
- **Gate on role-status `done`; failure holds.** The gate is the blocker's hera **role status** reaching `done` (a finished hera worker rolls its task to `in_review`, never auto-`complete`, so task status is NOT the gate). A still-`working` blocker — e.g. iterating on CI — does not open the gate, so the next phase never starts under churning work; a blocker whose session ends without reaching `done` **holds** the dependent and pings the coordinator (no spawn-and-park).
- **Hierarchical composition.** Each orchestrator owns its own DAG; a sub-coordinator is one node in its parent's DAG while owning a separate DAG in its child. Whole-phase sequencing across sub-teams is expressed at the parent level; only boundary-piercing cross-orchestrator edges are out of scope. **No concurrency cap** — a wide unblock materializes all ready nodes, and the coordinator throttles via the check-in.
- **Authoring over MCP.** New native hera tools `hera_plan_node`, `hera_block`, and `hera_plan` (whole-graph submit), coordinator-only like `hera_spawn_worker`. No human form editor — a coordinator *is* an agent.
- **Short-id authoring convention.** The planner assigns a stable short-id (`number` = stage, `letter` = parallel member) baked into the node name as a prefix. (Rendering short-ids in the tree is a **follow-up view change**, out of scope here.)

## Capabilities

### New Capabilities

_None._ The plan-DAG lives in the existing (currently retired) `task-orchestration` capability rather than a new one.

### Modified Capabilities

- `task-orchestration`: un-retired; gains the planned-node, blocking-edge, hierarchical-composition, gater, check-in, gate/hold, and short-id-convention requirements (the capability currently has 0 requirements).
- `hera-coordination`: the born-bound spawn requirement gains the decoupled planned-role/materialize path; the native MCP tool-surface requirement grows from nine tools to twelve (adds `hera_plan_node`, `hera_block`, `hera_plan`).

## Impact

- **Code:** `internal/db` (schema: `hera_blocks` table; store: `CreateHeraRole`-without-binding, cycle-check query); a new gater package mirroring the retired `internal/depswatcher`; `internal/agent` (`CreateAndStart` materialize-against-existing-role path; check-in bootstrap prefix); `internal/mcp/hera.go` (the three authoring tools + coordinator guard).
- **No REST/SPA surface.** Hera-native only — no `/api/dag`, no SPA, no standalone tab.
- **View deferred.** Re-pointing `internal/tui/dagview` at `hera_blocks` edges (short-ids, collapsing groups, navigation) is a separate follow-up change.
- **Migration:** additive schema (new table, no column drops); the gater is off unless a plan exists, so existing born-bound spawning is unaffected.
