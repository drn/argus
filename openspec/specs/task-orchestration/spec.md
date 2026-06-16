# Task Orchestration (RETIRED)

## Purpose

> **This capability is RETIRED.** Argus previously carried a declarative
> `depends_on` task-dependency DAG: an orchestrator agent built a directed
> acyclic graph where each task declared upstream dependencies, a dependency
> watcher auto-started blocked tasks once their deps completed, a halt cascade
> propagated across downstream tasks when a milestone failed, and the graph was
> laid out visually for inspection. It also owned the graph primitives —
> link/unlink mutations, cycle detection, and one-hop neighbour computation.
>
> All of it was removed in favor of **Hera** (coordinator-driven worker
> spawning) as the single orchestration model. Deleted: `model.Task.DependsOn`/
> `PlanSlug` and their DB columns, `internal/orch`, `internal/depswatcher`,
> `agent.StartPendingBlocked`, the `task_link`/`task_unlink`/`task_deps`/
> `task_halt_downstream`/`task_set_plan_slug` MCP + REST surface, the `/api/dag`
> endpoint, the standalone TUI DAG tab, and the SPA DAG view. Tasks now start
> immediately on creation; a coordinator sequences its workers itself and
> stacks PRs by branching each worker off the previous via `base_branch`
> (the git-stacking mechanic, which was KEPT).
>
> The live orchestration model is documented by the `task-messaging`,
> `mcp-server` (the `hera_*` tools), and `tui-shell` capabilities. The Hera view
> renders the **orchestration tree** (the coordinator → worker → sub-coordinator
> role-binding hierarchy), not a `depends_on` graph.
