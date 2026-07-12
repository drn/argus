---
name: hera-plan
description: >-
  The hera plan-DAG: author staged, dependency-ordered multi-worker plans inside an argus sandbox
  and let the daemon gater materialize each node into a born-bound worker as its blockers finish.
  Load this (in addition to the base `hera` skill) when you are a coordinator and the work
  decomposes into multiple hera worker units that have DEPENDENCIES among them (one stage needs
  another's output, or a required ordering) — internal dependencies are the clean signal to plan a
  DAG rather than spawn workers ad hoc. Covers the authoring/mutation verbs, gating-on-`done`,
  automatic branch-stacking and its fan-in footgun, short-id node naming, sub-coordinator nodes, and
  the self-guard prompt patterns. NOT for non-argus sessions; NOT for in-session ephemeral work (use
  Claude's native sub-agents); NOT for independent workers with no ordering (just spawn them).
---

# Hera plan-DAG — staged, dependency-ordered multi-agent work

This is the **coordinator-only** plan-DAG layer of hera. It assumes you already hold a live
coordinator binding and know the base hera model (roles, bindings, messaging, `hera_spawn_worker`,
status/tree) from the `hera` skill — **load that first if you haven't.** Every tool here takes
`cwd` (pass `cwd=$PWD`) and `orchestrator` is required when your task holds 2+ live bindings.

**Plan nodes land under your EXISTING orchestrator – the one you already coordinate.** Authoring a
plan-DAG is *not* a bootstrap step: you do **not** call `hera_new_orchestrator` to hold your plan.
The `hera_plan` / `hera_plan_node` verbs add nodes to the orchestrator you already coordinate, and
the gater materializes each node as a worker **directly beneath you**. Spinning up a second
orchestrator to hold your own plan creates a redundant self-coordinator that wrongly owns the DAG –
don't. The only reason to pass `orchestrator=` is to disambiguate when your task holds 2+ live
bindings; the only reason to spin up a *new* orchestrator inside a plan is the deliberate
`kind=subcoord` node (a genuinely distinct sub-goal handed to a separate sub-team – see below).

## When the plan-DAG is the right tool

The base `hera` skill's decision triad gets you here: the work decomposes into units that each must
be **their own argus session** (separate worktree / own PR / long-running / own sandbox), **and**
those units have **dependencies among them**. That dependency is the clean trigger:

- **Dependencies present** (stage B needs stage A's branch/output, or a required ordering) → **plan
  a DAG.** Author planned nodes wired by blocking edges; the gater runs them in order. *Decide this
  yourself when the dependencies are obvious — don't ask the human.* Only ask the human when it's
  genuinely ambiguous whether the effort warrants multi-session orchestration at all.
- **Independent units, no ordering** → don't author a DAG; just `hera_spawn_worker` them in parallel.
- **Ephemeral in-session work** (research, review, fan-out reads that return to you) → not hera at
  all; use Claude's native sub-agents (Agent/Task tool).

> **With a live coordinator binding, the plan-DAG is the single source of truth for the staged
> worker activity it covers. Author the workers as plan nodes; track progress through the DAG;
> reconcile the plan as work evolves. The harness `TaskCreate` system-reminder does not apply to
> coordinated work — use the plan/spawn tools, never bare task creation.**

## The gating contract (how nodes become live workers)

A **planned node** is a worker role with no live agent / worktree / inbox yet — one DB row. The
daemon gater (~60s tick) materializes it into a born-bound worker (exactly what `hera_spawn_worker`
would produce) according to this contract:

- **A node materializes ONLY when EVERY blocker reaches hera role-status `done`** — the worker's
  explicit "I'm finished" (which rolls its task to `in_review`). Role-status `done`, **not** task
  status, **not** idle.
- A blocker still `working` (e.g. iterating on CI) keeps the dependent **planned** — the next stage
  never starts under churning work.
- A blocker whose session **ended without ever reaching `done`** (crash, or it gave up / reported
  `failed`) **HOLDS** the dependent (no materialize) and pings you. No worker is ever spawned-and-
  parked behind dead or unfinished work.
- A node with **no blockers** is a root and materializes on the next tick.
- A cancelled planned node is treated as satisfied (non-blocking) — its dependents proceed.

## Authoring verbs

- **`hera_plan_node(cwd, name, prompt, [orchestrator], [project], [kind], [goal], [archetype])`** — create ONE
  planned node. **Name nodes by a `<stage><member>` short-id — number = serial stage, letter =
  parallel member (`1a`, `2a`, `2b`, `3a`)** — optionally with a *terse* suffix (`1a-seed`,
  `2a-alpha`). This is **not cosmetic**: the rail/DAG renders one box per node, and long descriptive
  names (`backend-api-handlers`, `frontend`) blow the boxes wide and wreck legibility once you have
  more than a handful of stages, while `2a`-style ids keep the graph tight and scannable. Names are
  uniquified within the orchestrator. `project` defaults to the coordinator's own.
  - `kind` — `worker` (default) or `subcoord`. A **worker** node materializes into a live born-bound
    worker; `prompt` is delivered to it (a check-in standing-order is prepended automatically). A
    **subcoord** node materializes into a *distinct sub-coordinator agent* — see "Sub-coordinator
    nodes" below.
  - `goal` — **required for `kind=subcoord`** (used instead of `prompt`): the objective handed to the
    sub-coordinator. You hand only the goal — not its child orchestrator name or its sub-plan.
  - `archetype` — the node's **diligence archetype** (e.g. `code_slice`, `review`, `ci_loop`); persisted
    on the planned node and copied onto the task when the gater materializes it, so the worker is born
    with the right per-archetype model + `ARGUS_ARCHETYPE`. See §9 of the base `hera` skill.

- **`hera_block(cwd, blocked, blocker, [orchestrator])`** — add a blocking edge: `blocked` waits on
  `blocker` reaching role-status `done` before it materializes. Both roles must be in your
  orchestrator. **Rejected** on a cycle, cross-orchestrator endpoints, or a **coordinator** blocker
  (a coordinator never reaches `done`, so it would be permanently unsatisfiable).

- **`hera_plan(cwd, nodes, [edges], [orchestrator])`** — submit a WHOLE graph in one
  **transactional** call: `nodes` = `[{name, prompt, [project], [kind], [goal], [archetype]}]`, `edges` =
  `[{blocked, blocker}]` referencing nodes by name (or existing roles). **All-or-nothing** — any
  cycle / cross-orchestrator / coordinator-blocker / validation error rolls back the entire graph
  (no orphan nodes). The way to lay out a multi-stage plan at once. **Name every node by its
  `<stage><member>` short-id** so the rendered DAG stays tight.

## Mutation verbs — the DAG is living, not authoring-time

Update the graph as reality diverges from the plan; don't abandon it.

- **`hera_plan_node_update(cwd, name, [prompt], [project], [orchestrator])`** — edit a **planned**
  node's prompt and/or project. Rejected once the node has materialized (the prompt was already
  delivered). Use when you discover the spec needs revision before the node spawns.
- **`hera_unblock(cwd, blocked, blocker, [orchestrator])`** — drop one blocking edge. Idempotent. To
  re-point: `hera_unblock` (old blocker) then `hera_block` (new blocker).
- **`hera_plan_node_cancel(cwd, name, [orchestrator])`** — cancel a planned node: it never
  materializes, its dependents proceed (no longer gated on it), it stays visible as a grey ✕.
  Rejected once materialized (use the task lifecycle to stop a running worker).

**Standing order:** after every worker interaction, check whether the plan still mirrors reality —
`hera_plan_node_update` a changed scope, `hera_unblock` an obsolete edge, `hera_plan_node_cancel` a
superseded node. A worker re-engaging on rework after `done`/`failed` reports `working` on its next
`hera_send` by requirement, so the DAG self-corrects for a simple reopen.

## Materialization + branch-stacking (the gater drives this, not you)

- **Non-root nodes stack automatically**: a materializing node is branched off its most-recently-
  `done` blocker's branch, so a *linear* chain produces cleanly stacked PRs.
- **Fan-in stacks on ONE blocker, not a merge of all.** A node with multiple blockers bases off the
  *single* most-recently-`done` blocker's branch — it does **not** merge the others in. In a diamond
  (`3a` blocked by both `2a` and `2b`), `3a` starts from whichever of `2a`/`2b` materialized later
  and is **missing the other's work** unless those two were themselves stacked. For true fan-in,
  either keep the stages a linear chain, or have the fan-in node merge the branches itself via a
  self-rebase step (see below).
- **`done` gates materialization, but `done` ≠ merged/integrated.** A worker reaching `done` rolls
  its task to `in_review` (*not* merged) — so the gater materializes the dependent the instant the
  blocker *reports* done, **before** you've reviewed or merged anything. The dependent stacks on the
  blocker's worker branch as it stood at `done`. That's exactly right for a linear stack where that
  branch *is* the integration point; but if your workflow merges upstream work into a separate
  feature branch before cutting the next stage, the materialized node will be racing ahead of your
  merge — make node prompts self-defending (next section).
- **Root nodes** (no blockers) resolve their base branch as: explicit orchestrator `base_branch` →
  the coordinator role's bound-task branch → the project default. Root a plan on your feature branch
  by passing `base_branch` to `hera_new_orchestrator`.
- **Respond to check-ins promptly.** Each node check-ins on materialization via `hera_send`; pull it
  from `hera_inbox` and reply (e.g. `"go"`). A node HELD behind a genuinely failed blocker pings you
  — `hera_unblock` the edge, `hera_plan_node_cancel` the held node, or `hera_spawn_worker` a
  replacement. (Coordinator-as-blocker is rejected at authoring time, so the graph can't wedge on a
  never-`done` coordinator.)

## Self-defending node prompts (the standard mitigation)

Because a node materializes the instant its blockers *report* `done` — ahead of your review/merge —
any node that depends on upstream output should carry two prompt-side guards:

- **Self-rebase** — the node's first step is `git merge --no-edit origin/<integration-branch>` (or
  the sibling branch in a fan-in) to pull in whatever is integrated so far.
- **Self-guard** — the node greps for the API routes / files / symbols it depends on and, if absent,
  `hera_send`s you to wait instead of building against a phantom contract.

This is what makes plan-mode safe for stacked-integration and contract-discovery work, so you rarely
need to fall back to driving every stage by hand. Reserve pure incremental `hera_spawn_worker`
(spawn the next stage manually only after you've merged the prior) for when even self-guarding is too
racy — i.e. a hard human/coordinator decision gate must sit between phases.

## Sub-coordinator nodes (`kind=subcoord`)

Use a subcoord node when a plan stage is itself a *sub-team* — a chunk big enough to warrant its own
coordinator and its own fan-out — rather than a single unit of work. It's the **declarative** form of
worker promotion (a worker calling `hera_new_orchestrator` on itself mid-task): you author the
sub-team as a plan node up front, and the gater materializes it as a *distinct coordinator agent*
when its blockers finish.

- It occupies the parent DAG exactly like any node (a worker role in **your** orchestrator) — blocking
  edges, gating, hold/ping, and branch-stacking all treat it identically; its worker-role `done` gates
  the parent's dependents.
- At materialization it becomes **one new agent** (own task + worktree) that is simultaneously a
  worker in your orchestrator AND the coordinator of a freshly-created, auto-named child orchestrator
  — so it nests under you in the rail/tree via the multi-binding bridge, never sharing your task.
- You hand it only the `goal`. It runs its own planning (often `/brainstorm` → its own `hera_plan`)
  and spawns its own workers. Bake rich context into the goal so it needs little back-and-forth.
- Keep it an explicit choice — default to plain worker nodes; don't spin up middle-management for a
  stage one worker can do.

## Worked example — author a staged plan-DAG and let it self-materialize

The work has a seed, a parallel fan-out, and a fan-in:

1. **Author the DAG under your EXISTING orchestrator – do NOT call `hera_new_orchestrator` to hold
   your plan.** If you are already a coordinator (you bootstrapped or claimed an orchestrator earlier
   this session – the common case when you reach this skill), skip straight to step 2: the nodes
   become workers in your current orchestrator, materialized directly beneath you by the gater. Only
   call `hera_new_orchestrator(cwd=$PWD, name="<feature>", coordinator_role_name="coord")` in the rare
   cold-start case where you do **not** yet hold ANY coordinator binding; calling it when you already
   coordinate one creates a redundant second coordinator that wrongly holds your DAG. (To root a
   *fresh* orchestrator's plan on a feature branch, pass `base_branch="argus/<your-branch>"`; an
   existing orchestrator already carries its own base – see the root-node branch resolution above.)
   Author your own stages directly as plain worker nodes; reach for `kind=subcoord` only to hand a
   genuinely distinct sub-goal to a separate sub-team.
2. Submit the whole graph transactionally — short-id names, full spec baked into each prompt:
   ```
   hera_plan(cwd=$PWD,
     nodes=[
       {name:"1a-seed",  prompt:"<complete spec…>"},
       {name:"2a-alpha", prompt:"<complete spec…>"},
       {name:"2b-beta",  prompt:"<complete spec…>"},
       {name:"3a-final", prompt:"<complete spec…>"}],
     edges=[
       {blocked:"2a-alpha", blocker:"1a-seed"},
       {blocked:"2b-beta",  blocker:"1a-seed"},
       {blocked:"3a-final", blocker:"2a-alpha"},
       {blocked:"3a-final", blocker:"2b-beta"}])
   ```
   `1a-seed` materializes first (rooted on your branch); `2a`/`2b` materialize in parallel once it's
   `done` (each stacked on `1a-seed`'s branch); `3a-final` waits for **both** and stacks on the latest.
   **Fan-in caveat:** `3a-final` bases off whichever of `2a`/`2b` finished later — it does NOT
   auto-merge the other half. Give `3a-final`'s prompt a self-rebase first step (`git merge --no-edit`
   the sibling / integration branch) so it actually has both halves before it builds.
3. Watch it fill in the second-tab plan-DAG (planned `○` → live). Respond to each node's check-in:
   `hera_inbox(cwd=$PWD)` on the doorbell → reply `hera_send(cwd=$PWD, to="<node>", body="go", tldr="go")`.
4. If a node is HELD behind a genuinely failed blocker, the gater pings you — `hera_unblock`,
   `hera_plan_node_cancel`, or `hera_spawn_worker` a replacement.
5. **Reconcile as work unfolds** — `hera_plan_node_update` a changed scope before it materializes,
   `hera_unblock` an obsolete edge, `hera_plan_node_cancel` a superseded node. Keep the DAG a live
   mirror of the actual plan.

## Gotchas worth calling out

- **The gate is role-status `done`, not idle and not merged.** Idle-without-`done` keeps a node
  planned; a session that ended without `done` HOLDS its dependents and pings you. Materialization
  fires the instant a blocker *reports* done, which is before your review/merge — see the
  branch-stacking and self-defending-prompts sections.
- **Fan-in does not merge — it picks one branch.** A multi-blocker node bases off only the
  latest-`done` blocker's branch. Self-rebase the others in, or keep the stages linear.
- **Mutation verbs only work pre-materialization.** `hera_plan_node_update` / `hera_plan_node_cancel`
  are rejected once a node has a binding — at that point manage the running worker via the task
  lifecycle, not the plan.
- **Short-id names are load-bearing for legibility.** Descriptive node names blow the rail/DAG boxes
  wide at scale; use `<stage><member>` ids.
