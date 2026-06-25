---
name: hera
description: >-
  Inside an argus sandbox (cwd under ~/.argus/worktrees/ or ARGUS_TASK_ID set), coordinate
  multi-agent work via hera's mcp__argus__hera_* tools — bootstrap an orchestrator, claim or
  attach a worker/freelance role, and message other roles over the idle-gated bus. Use when you
  need to spawn and coordinate other agent sessions, run a large multi-session project, or
  message another role. NOT for non-argus sessions, where these MCP tools are not registered.
---

# Hera — native multi-agent coordination inside argus

Hera is argus's **native, in-tree** layer for running a *team* of agents. It is not a separate
daemon or plugin — coordination runs in-process in the argus daemon, the rail/tree render directly
in the TUI's second tab, and agents drive it entirely through the twelve `mcp__argus__hera_*` MCP
tools. State lives in the same `~/.argus/data.sql` (the `hera_*` tables). You never touch the
plumbing; you call the tools.

## 1. When this applies (and when it does NOT)

This skill applies **only inside an argus task sandbox**. You are in one if **either** holds:

- `ARGUS_TASK_ID` is set, **or**
- the current working directory is under `~/.argus/worktrees/`.

**If neither holds, stop.** The `mcp__argus__hera_*` tools are not registered in this session — there
is no CLI fallback and nothing below applies.

**Every hera tool takes `cwd` — always pass `cwd=$PWD`.** That is how hera resolves which argus task
(and therefore which role) this session is. There is no separate "auth" or session handle.

## 2. The role model

- **Orchestrator** — a named coordination graph (one per project / feature / wave). Roles live under
  it. Created by `hera_new_orchestrator` (idempotent by name).
- **Coordinator role** — the orchestrator's driver. Created by `hera_new_orchestrator`. Talks to the
  human in its own agent pane; talks to other roles via `hera_send`. Folded into the rail's
  orchestrator header (it is not a separate row).
- **Worker role** — does the actual work. Normally **born-bound**: spawned by a coordinator with
  `hera_spawn_worker`, which creates the argus task (worktree + session) AND the role + binding in one
  transaction. A born-bound worker does *not* need to `hera_join` — its session opens already bound.
- **Freelance role** — a helper that attaches itself to an existing orchestrator on its own
  initiative (no coordinator spawned it). Created via `hera_join` attach mode with `kind=freelance`.
- **Binding** — the live link between *this argus task* and *one role*. Invariants:
  - **One live binding per `(task, orchestrator)`** — enforced by a partial unique index.
  - **A task MAY hold several live bindings at once** — one per orchestrator (e.g. a worker in A that
    promotes itself to coordinator of nested orchestrator B). When a task holds **2+** live bindings,
    you **must** pass `orchestrator=<name>` to every tool so hera knows which role you are acting as;
    omitting it returns an ambiguity error listing your options.

## 3. The twelve tools

All take `cwd`. `orchestrator` is optional with exactly one live binding and **required** with 2+.
Arg names below are exact — do not invent others. The nine coordination tools come first; the three
**plan-DAG authoring** tools are grouped at the end of this section.

### Bootstrap / join

- **`hera_new_orchestrator(cwd, name, coordinator_role_name, [prompt])`** — "I am the coordinator."
  Creates (or fetches, idempotent-by-name) the orchestrator, creates the named coordinator role, and
  binds this task to it. Returns the orchestrator name, role name, `binding_id`, and argus task id.
  Rejects if this task already holds a live binding under that orchestrator (use `hera_join` to
  retrieve it). The canonical "become an orchestrator" entry point — don't `hera_join` first; there is
  nothing to join yet.

- **`hera_join(cwd, [orchestrator], [role_name], [kind], [prompt], [status])`** — two modes:
  - **Claim mode** (`role_name` omitted) — retrieves this task's existing live binding + role and its
    unread message count. Use right after a born-bound worker terminal opens to read your assigned
    role/mission. Pass `orchestrator=` if the task has 2+ bindings. Claim mode does **not** consume the
    inbox.
  - **Attach mode** (`role_name` + `kind` supplied) — creates a **new** role under an *existing*
    orchestrator (`orchestrator` required) and binds this task to it. `kind` must be `worker` or
    `freelance` (`coordinator` is rejected — use `hera_new_orchestrator`). Optional `prompt` (stored on
    the role) and `status` (`idle`/`working`/`blocked`/`done`). Use to join a team nobody spawned you
    into.

- **`hera_spawn_worker(cwd, prompt, [orchestrator], [role_name], [project], [branch], [backend], [model])`**
  — spawn a new **born-bound** worker task + session under the caller's orchestrator. **Caller must hold
  a live coordinator binding.** Creates an argus task (worktree + session) and, transactionally, a worker
  role + binding pre-bound to it; an orientation prefix naming the coordinator + orchestrator is prepended
  to the prompt automatically. Args:
  - `prompt` (**required**) — the full task prompt for the worker. The verbatim prompt is also stored on
    the role row.
  - `project` — defaults to the **coordinator's own task project** (authoritative, not `role.ArgusProject`).
  - `branch` — base branch passed to argus task creation. **Defaults to the project default — see the
    base-branch gotcha in §6.**
  - `backend` — defaults to project default.
  - `model` — per-worker model override, scoped to the worker's resolved backend (claude: opus/sonnet/
    haiku; codex: e.g. gpt-5; pi: its ids). Empty = backend default. Match it to task complexity.
  - `role_name` — derived from a prompt slug if omitted; uniquified within the orchestrator.
  - `orchestrator` — disambiguates when the calling task holds multiple live coordinator bindings.

  Returns the orchestrator, worker role name, `binding_id`, argus task id, and project.

### Messaging (idle-gated bus)

- **`hera_send(cwd, body, tldr, status, [to], [in_reply_to], [orchestrator])`** — message another role
  **in the same orchestrator**. `body` and `tldr` are **required**; `tldr` is a one-line summary ≤120
  chars, written from the recipient's perspective (see §5). **`status` is REQUIRED for worker/freelance
  senders** (one of `idle`/`working`/`blocked`/`done`/`failed`) and is applied to the sender's role
  **synchronously** before the message is sent — it is never delivered async. Omitting `status` as a
  worker/freelance sender is an error; coordinator senders may omit it. Worker/freelance senders may
  omit `to` — it default-routes to the orchestrator's coordinator. **Coordinators must supply an
  explicit `to`.** `in_reply_to` threads a reply to a prior message id. Returns the `message_id`,
  recipient, and delivery mode. Caps: 64 KiB body, 500 unread per recipient, 50 sends/min/sender.
  Cross-orchestrator messaging is not possible — `to` always resolves within your own orchestrator.

- **`hera_inbox(cwd, [orchestrator])`** — fetch all unread messages addressed to your role, oldest
  first. **Reading IS acknowledgment**: this both cancels pending pane deliveries AND marks the
  messages read — no separate `hera_mark_read` needed for normal consumption. Call it whenever you get a
  doorbell.

- **`hera_mark_read(cwd, message_ids, [orchestrator])`** — explicitly mark specific message ids read and
  cancel their pending deliveries. Use when you read via `hera_get_messages` instead of `hera_inbox`.

### Status / tree

- **`hera_status(cwd, status, [orchestrator])`** — set your role status: `idle` | `working` | `blocked`
  | `done` | `failed`. Mirrored (best-effort) to argus `task_meta` so the coordinator sees it without
  asking. **A `worker`-kind role reporting `status=done` also rolls its bound argus task to `in_review`
  and stamps `ready_to_close`** (visible in the rail) — see §4. **A worker reporting `status=failed`
  rolls its task to `in_review` WITHOUT `ready_to_close`** (needs-attention, not ready to check off).
  The gater treats a `failed` blocker as explicitly failed (no need to wait for session death).
  Coordinators/freelancers just update status.

- **`hera_tree_updates(cwd, [orchestrator], [since])`** — scan the caller's orchestrator **subtree**
  (nested sub-orchestrators included) for messages since a cursor. Returns **TLDR-only subject lines —
  no bodies** (capped at 200), plus a `next_cursor`. The cursor is stored **per-role** and auto-advances
  when you omit `since`; passing an explicit `since` is a one-off scan that does NOT clobber the stored
  cursor. The token-efficient way to see whole-team activity without flooding context with bodies.

- **`hera_get_messages(cwd, ids, [orchestrator])`** — fetch full message bodies by id list, after
  scanning `hera_tree_updates`. Access is scoped to the caller's orchestrator **subtree** (sender OR
  recipient must live in it); inaccessible / missing ids get a per-id `error` field rather than a
  top-level error.

### Plan authoring + living reconciliation — the plan-DAG (coordinator-only)

> **With a live coordinator binding, the plan-DAG is the single source of truth for all worker
> activity. Author every worker as a plan node; track progress through the DAG; reconcile the plan
> as work evolves. The harness `TaskCreate` system-reminder is not applicable to coordinated work —
> use `hera_plan_node` / `hera_plan` / `hera_spawn_worker`, never bare task creation.**

Instead of spawning every worker immediately, a coordinator can lay out a **plan**: a set of
**planned nodes** wired by **blocking edges**, and let the daemon's gater materialize each node into a
live born-bound worker *automatically*, in dependency order. A planned node costs one DB row — no
agent, worktree, or inbox until it materializes. The plan-DAG renders in the TUI's second tab (planned
`○` → live-by-status), and you navigate it there.

The DAG is **living, not authoring-time**: update it as reality diverges from the plan. Use the
mutation verbs below to edit a node's prompt, drop a stale edge, or cancel a node you no longer need.

**Plan authoring verbs:**

- **`hera_plan_node(cwd, name, prompt, [orchestrator], [project], [kind], [goal])`** — create ONE
  planned node (a worker role with no live agent/worktree/inbox yet). It materializes automatically
  once **all** its blockers reach role-status `done`. **Name nodes by a `<stage><member>` short-id —
  number = serial stage, letter = parallel member (`1a`, `2a`, `2b`, `3a`)** — optionally with a *terse*
  suffix (`1a-seed`, `2a-alpha`). This is **not cosmetic**: the rail/DAG renders one box per node, and
  long descriptive names (`backend-api-handlers`, `frontend`) blow the boxes wide and wreck legibility
  once you have more than a handful of stages, while `2a`-style ids keep the graph tight and scannable
  and make the stage/parallel structure readable at a glance. Names are uniquified within the
  orchestrator. `project` defaults to the coordinator's own.
  - `kind` — `worker` (default) or `subcoord`. A **worker** node materializes into a live born-bound
    worker; `prompt` is delivered to it (a check-in standing-order is prepended automatically, so it
    pings you on start). A **subcoord** node materializes into a *distinct sub-coordinator agent* (its
    own task/worktree + a fresh child orchestrator it coordinates) — see "sub-coordinator nodes" below.
  - `goal` — **required for `kind=subcoord`** (and used instead of `prompt`): the objective handed to
    the sub-coordinator. You hand only the goal — you do NOT name its child orchestrator or author its
    sub-plan; the sub-coordinator owns its own decomposition.

- **`hera_block(cwd, blocked, blocker, [orchestrator])`** — add a blocking edge: `blocked` waits on
  `blocker` reaching role-status `done` before it materializes. Coordinator-only; both roles must be in
  your orchestrator. **Rejected** if it would create a cycle, if the roles are in different
  orchestrators, or if `blocker` is a **coordinator** role (a coordinator never reaches `done`, so it
  would be a permanently-unsatisfiable dependency).

- **`hera_plan(cwd, nodes, [edges], [orchestrator])`** — submit a WHOLE graph in one **transactional**
  call: `nodes` = `[{name, prompt, [project], [kind], [goal]}]` (per-node `kind` = `worker` default |
  `subcoord`; a `subcoord` node needs `goal`, not `prompt`), `edges` = `[{blocked, blocker}]`
  referencing nodes by name (or existing roles). Nodes are created first, then edges (cycle-checked,
  single-orchestrator).
  **All-or-nothing** — any cycle / cross-orchestrator / coordinator-blocker / validation error rolls
  back the entire graph (no orphan nodes). This is the way to author a multi-stage plan at once rather
  than many `hera_plan_node` + `hera_block` calls. **Name every node by its `<stage><member>` short-id
  (`1a`, `2a`, `2b`, `3a`)** so the rendered DAG stays tight — descriptive names blow the boxes wide
  (see `hera_plan_node` above for the rationale).

**Plan mutation verbs (reconcile as you go — coordinator-only):**

- **`hera_plan_node_update(cwd, name, [prompt], [project], [orchestrator])`** — edit a **planned**
  node's prompt and/or project. Rejected once the node has materialized (the prompt was already
  delivered to the worker). Use this when you discover the original spec needs revision before the
  node spawns.

- **`hera_unblock(cwd, blocked, blocker, [orchestrator])`** — drop one blocking edge. Idempotent
  (dropping a missing edge is a no-op). To re-point an edge: `hera_unblock` (old blocker) then
  `hera_block` (new blocker).

- **`hera_plan_node_cancel(cwd, name, [orchestrator])`** — cancel a planned node: it will never
  materialize, its dependents proceed (no longer gated on it), and it stays visible in the plan as a
  grey ✕. Rejected if the node has already materialized (use the task lifecycle to stop a running
  worker). Cancelled nodes are kept in the DAG to show what was dropped — not silently deleted.

**How materialization + branch-stacking works** (you don't drive it — the gater does, ~60s tick):

- A node with **no remaining blockers** materializes into a born-bound worker (same as
  `hera_spawn_worker` would produce).
- **Non-root nodes stack automatically**: a materializing node is branched off its most-recently-`done`
  blocker's branch, so a *linear* chain produces cleanly stacked PRs.
- **Fan-in stacks on ONE blocker, not a merge of all.** A node with multiple blockers bases off the
  *single* most-recently-`done` blocker's branch — it does **not** merge the others in. In a diamond
  (`3a` blocked by both `2a` and `2b`), `3a` starts from whichever of `2a`/`2b` materialized later and
  is **missing the other's work** unless those two were themselves stacked. For true fan-in, either keep
  the stages a linear chain, or have the fan-in node merge the branches itself via a self-rebase step
  (next bullet).
- **`done` gates materialization, but `done` ≠ merged/integrated.** A worker reaching role-status `done`
  rolls its task to `in_review` (*not* merged) — so the gater materializes the dependent the instant the
  blocker *reports* done, which is **before** you've reviewed or merged anything. The dependent stacks on
  the blocker's worker branch as it stood at `done`. That's exactly right for a linear stack where that
  branch *is* the integration point; but if your workflow merges upstream work into a separate feature
  branch before cutting the next stage, the materialized node will be racing ahead of your merge — bake a
  **self-rebase + self-guard** step into its prompt (see §6) so it pulls the integration branch and
  verifies its prerequisites before building.
- **Root nodes** (no blockers) resolve their base branch as: explicit orchestrator `base_branch` →
  the coordinator role's bound-task branch → the project default. So a plan rooted on your feature
  branch stacks on it (pass `base_branch` to `hera_new_orchestrator` to override the root).
- **Respond to check-ins promptly.** Each node check-ins on materialization via `hera_send`; you pull
  it from `hera_inbox` and reply (e.g. `"go"`). A node whose blocker genuinely **failed** is HELD and
  pings you — decide whether to `hera_unblock`, `hera_plan_node_cancel` the held node, or dispatch a
  replacement via `hera_spawn_worker`.

**Standing order — keep the DAG reconciled:**

- After every worker interaction, check whether the plan still reflects reality. If a worker's scope
  changed, `hera_plan_node_update` its prompt. If an edge is no longer needed, `hera_unblock` it. If a
  node was superseded, `hera_plan_node_cancel` it.
- A worker reopening (re-engaging on rework after `done`/`failed`) reports `working` on its next
  `hera_send` by requirement — the DAG self-corrects. No coordinator action needed for a simple reopen.

**Sub-coordinator nodes (`kind=subcoord`).** Use a subcoord node when a plan stage is itself a *sub-team*
rather than a single unit of work — a chunk big enough to warrant its own coordinator and its own
fan-out. It is the **declarative** form of "worker promotion" (§7): instead of spawning a worker that
later calls `hera_new_orchestrator` on itself, you author the sub-team as a plan node up front, and the
gater materializes it as a *distinct coordinator agent* when its blockers finish. Mechanics:
- It occupies the parent DAG exactly like any node — it is a worker role in **your** orchestrator, so
  blocking edges, gating, hold/ping, and branch-stacking all treat it identically. Its worker-role
  `done` gates the parent's dependents.
- At materialization it becomes **one new agent** (own task + worktree) that is simultaneously a worker
  in your orchestrator AND the coordinator of a freshly-created child orchestrator (named automatically,
  de-collided) — so it nests under you in the rail/tree via the multi-binding bridge, never sharing your
  task. **One claude instance = one rail element** holds.
- You hand it only the `goal`. It runs its own planning (often `/brainstorm` → its own `hera_plan`) and
  spawns its own workers. Bake rich context into the goal so it needs little back-and-forth; it can
  still `hera_send` you for guidance.
- Keep it an explicit choice — default to plain worker nodes for ordinary stages. Don't spin up
  middle-management for a stage one worker can do.

## 4. Decision rules

- **Starting a coordination effort?** `hera_new_orchestrator`. Don't `hera_join` first.
- **A coordinator spawned you (fresh born-bound worker terminal)?** `hera_join(cwd)` to read your role
  + mission, then `hera_status(working)`. You are already bound — no attach needed.
- **Joining a team that didn't spawn you?** `hera_join` attach mode with explicit `role_name` + `kind`.
- **`new_orchestrator` vs `join`:** `new_orchestrator` makes you a coordinator of a *new* orchestrator;
  `join` claims/attaches a role under an *existing* one. A worker can do BOTH — stay a worker in the
  parent and `hera_new_orchestrator` to become a coordinator of a nested team (multi-binding).
- **`spawn_worker` vs adopt:** native hera has **no adopt step** — workers are born bound at spawn time.
  (The old `depends_on`-driven auto-adopt watcher was retired with the DAG.) To delegate, just
  `hera_spawn_worker`.
- **Spawn now vs plan a DAG:** use `hera_spawn_worker` when you want a worker running *immediately*.
  Use the **plan-DAG** (`hera_plan`, or `hera_plan_node` + `hera_block`) when work runs in **stages /
  dependency order** — author planned nodes wired by blocking edges and let the gater materialize them
  as their blockers finish (auto-stacking each stage's branch on the prior). Lay the whole graph out
  with one `hera_plan` call; respond to each node's check-in via `hera_inbox`. **Reconcile the DAG
  as work evolves** — edit nodes, drop stale edges, cancel superseded nodes rather than abandoning the
  plan.
- **When a stage's base needs *integration* (not just a branch-stack), or a downstream prompt depends on
  an upstream's *discovered* contract:** the plan-DAG still works — but the gater materializes on
  blocker-`done`, *ahead of your merge* (see §3), so don't author the graph and walk away. Two safe
  prompt-side patterns, used together, make plan-mode robust here: **self-rebase** — the node's first step
  is `git merge --no-edit origin/<your-integration-branch>` to pull whatever you've integrated so far;
  **self-guard** — the node greps for the API routes / files / symbols it depends on and, if absent,
  `hera_send`s you to wait instead of building against a phantom contract. Reserve pure incremental
  `hera_spawn_worker` (spawn the next stage by hand only after you've merged the prior) for when even that
  is too racy — i.e. a hard human/coordinator decision gate must sit between phases.
- **Worker node vs sub-coordinator node:** a plain plan node (`kind=worker`) is a single unit of work.
  Make it `kind=subcoord` (with a `goal`) only when the stage is a *sub-team* — large enough to deserve
  its own coordinator that plans and fans out its own workers. It's the declarative alternative to a
  worker promoting itself mid-task (§7). Default to worker nodes.
- **This task holds 2+ bindings?** Pass `orchestrator=` on EVERY tool call.
- **Got a doorbell?** Call `hera_inbox(cwd=$PWD)` immediately — the content is in the inbox, not the
  doorbell line.
- **Want whole-team state?** `hera_tree_updates(cwd=$PWD)`, then `hera_get_messages(ids=[…])` for the
  ones worth reading.
- **How completion flows back:** a worker finishing sends a closing `hera_send(status="done", …)` — the
  synchronous status apply rolls its task to `in_review` + `ready_to_close`, visible in the rail. A
  worker that cannot complete sends `hera_send(status="failed", …)` — rolls to `in_review` WITHOUT
  `ready_to_close` (needs attention, not ready to check off); the gater holds any dependent planned
  nodes and pings you. Both rolls are idempotent and only fire when the task is still `in_progress`.
  The live session is left running.
- **Don't** use `hera_send` to talk to the human — the human reads the coordinator's own agent pane;
  the bus is role-to-role only.

## 5. TLDR discipline

Every `hera_send` requires a `tldr` ≤120 chars, written from the recipient's perspective. It is shown
in the doorbell, returned by `hera_tree_updates`, and stored permanently.

- Good: `"PR #47 open, tests green, needs review"` / `"Blocked on missing API key — need rotation"`
- Bad: `"update"` (says nothing) / `"Done with the work"` (no specifics) / multi-line.

## 6. Gotchas worth calling out

- **`hera_send` requires `status` for worker/freelance senders — omitting it is an error.** The status
  is applied synchronously before the send completes; it never rides the async delivery bus. This means
  every `hera_send` call doubles as a role-status heartbeat. There is no default; the error message on
  omission names the valid values (`idle`/`working`/`blocked`/`done`/`failed`).
- **Spawned workers default to the project's stale default branch, NOT the coordinator's branch.**
  `hera_spawn_worker`'s `branch` defaults to the *project* default (e.g. an old `master`/`main`), not the
  coordinator's current worktree branch. If the worker must build on the coordinator's (or a sibling's)
  work, **pass `branch=` explicitly and verify ancestry** — otherwise the worker starts from stale code.
- **Bake all requirements into the initial `hera_spawn_worker` prompt.** Mid-flight `hera_send` to a
  worker is often missed: delivery is idle-gated and best-effort, so a busy worker never receives it and
  an idle/finished worker may not act on it. Put the full spec in the spawn prompt; verify via the branch
  diff and re-dispatch a fresh worker if one idled out without the requirement.
- **Plan-DAG nodes materialize on blocker-`done`, which races ahead of your merge — make node prompts
  self-defending.** A node spawns the instant its blockers *report* `done` (their tasks roll to
  `in_review`, not merged), so a node that depends on upstream output can start before you've reviewed or
  integrated it. Give any such node two prompt-side guards: a **self-rebase** first step
  (`git merge --no-edit origin/<integration-branch>`) to pull integrated work, and a **self-guard** check
  that greps for the prerequisite routes/files/symbols and `hera_send`s you to wait if they're missing,
  rather than building blind. This is the standard mitigation for the fan-in and contract-discovery cases
  in §3/§4 — it is what makes plan-mode safe for stacked work, so you rarely need to fall back to driving
  every stage by hand.
- **The message bus is idle-gated and best-effort for *delivery*, durable for *storage*.** Storage always
  succeeds (the row is committed); live pane delivery soft-fails (logged, never rolled back) when the
  recipient has no live binding or never becomes idle. `hera_inbox` always returns the durable rows, so
  the recipient can always catch up by reading — don't assume a sent message was seen just because it sent.
- **`worker done` must keep the role messageable.** Native deliberately does NOT auto-archive a role on
  `hera_status(done)` (the external plugin did). An archived role drops out of name-keyed recipient
  resolution, so auto-archiving a still-live worker would make the coordinator's `hera_send` to it bounce.
  Done flips the task to in_review; it does not archive the role.
- **Coordinators must name a recipient.** `hera_send` from a coordinator with `to` omitted errors —
  only worker/freelance senders get the default-to-coordinator routing.

## 7. Worked workflows

### (a) Bootstrap an orchestrator, spawn two workers, collect results

You are a coordinator-to-be in your argus sandbox:

1. `hera_new_orchestrator(cwd=$PWD, name="checkout-revamp", coordinator_role_name="coord", prompt="Coordinate the checkout revamp")`.
2. Spawn workers, each with the FULL spec baked in and an explicit base branch:
   - `hera_spawn_worker(cwd=$PWD, role_name="cart-api", branch="argus/<base>", prompt="<complete cart-API spec…>")`
   - `hera_spawn_worker(cwd=$PWD, role_name="checkout-ui", branch="argus/<base>", prompt="<complete checkout-UI spec…>")`
3. Poll progress without flooding context: `hera_tree_updates(cwd=$PWD)` → scan TLDRs →
   `hera_get_messages(cwd=$PWD, ids=[…])` for the interesting ones.
4. When a worker reports `done` (its task rolls to in_review + ready_to_close in the rail), review its
   branch/PR, then reply or spawn the next stage. To stack work, branch the next worker off the prior
   worker's branch via `branch=`.

### (b) A spawned worker reports completion

You opened in a born-bound worker terminal:

1. `hera_join(cwd=$PWD)` → read your role name, mission (role prompt), and unread count.
2. `hera_status(cwd=$PWD, status="working")`.
3. Do the work in your worktree. If you hit a fork that needs the coordinator's call:
   `hera_send(cwd=$PWD, status="working", body="<question + context>", tldr="Need decision: X vs Y for the cart schema")`
   (no `to` needed — default-routes to the coordinator), then check `hera_inbox(cwd=$PWD)` on the
   doorbell for the answer. **Always supply `status` on every `hera_send` — it is required for
   worker/freelance senders.**
4. Land your work (open a PR via iris, or leave commits for the coordinator to pull).
5. `hera_send(cwd=$PWD, status="done", body="<summary + PR link>", tldr="cart-api done, PR #47, tests green")`
   — the synchronous status apply rolls your task to in_review + ready_to_close so the coordinator
   sees you finished. If you cannot complete: `hera_send(cwd=$PWD, status="failed", body="<reason>", …)`.

### (c) Author a staged plan-DAG and let it self-materialize

You are a coordinator and the work has clear stages (a seed, a parallel fan-out, a fan-in):

1. Bootstrap (if you haven't): `hera_new_orchestrator(cwd=$PWD, name="<feature>", coordinator_role_name="coord")`.
   To root the plan on your current feature branch, pass `base_branch="argus/<your-branch>"`.
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
   **Fan-in caveat:** `3a-final` bases off whichever of `2a`/`2b` finished later — it does NOT auto-merge
   the other half. Give `3a-final`'s prompt a self-rebase first step (`git merge --no-edit` the sibling/
   integration branch) so it actually has both halves before it builds (see §6).
3. Watch it fill in the second-tab plan-DAG (planned `○` → live). Respond to each node's check-in:
   `hera_inbox(cwd=$PWD)` on the doorbell → reply `hera_send(cwd=$PWD, to="<node>", body="go", tldr="go")`.
4. If a node is HELD behind a genuinely failed blocker, the gater pings you — use `hera_unblock` to
   drop the edge, `hera_plan_node_cancel` to cancel the held node, or `hera_spawn_worker` to dispatch
   a replacement. (A coordinator-as-blocker edge is rejected at authoring time, so you can't wedge the
   graph on a never-`done` coordinator.)
5. **Reconcile as work unfolds.** If a worker's scope changed: `hera_plan_node_update` its prompt before
   it materializes. If an edge is obsolete: `hera_unblock`. If a node was superseded: `hera_plan_node_cancel`.
   Keep the DAG a live mirror of the actual plan.

### Worker promotion: becoming a sub-coordinator

> If you already know up front (at plan-authoring time) that a stage is a sub-team, prefer the
> **declarative** form: a `kind=subcoord` plan node (§3). The gater then materializes the
> sub-coordinator for you when its blockers finish. The runtime promotion below is for when a worker
> discovers the need *mid-task*.

If a worker realizes mid-task it needs its own team (cross-repo work, real parallelism, a long sub-task):

1. `hera_new_orchestrator(cwd=$PWD, name="<sub-team>", coordinator_role_name="coord", prompt="…")` — now
   this session is a coordinator of a nested orchestrator AND still a worker in the parent (multi-binding;
   pass `orchestrator=` on subsequent calls).
2. `hera_spawn_worker(...)` to dispatch into the right project.
3. Report the sub-orchestrator name back to the parent coordinator via `hera_send`. Prefer using Claude's
   native sub-agents for in-session parallelism; reserve `hera_spawn_worker` for work where the session
   itself (separate worktree / repo / sandbox) is the unit.

## 8. Composition with sibling argus tools

Hera owns **identity, messaging, and coordination** — nothing else. Reach the rest through their own MCP
tools:

- **iris** (`mcp__argus__iris_*`) — host-side git/gh. A worker codes + commits locally in its worktree,
  then uses iris to push / open a PR / merge back. **Use `iris_gh_pr_create` rather than `gh pr create`**
  so the PR is stamped onto the task's `pr` meta namespace — that is what the Hera rail's PR indicator
  reads (best-effort, never fetched by the view).
- **plannotator-argus** (`mcp__argus__plannotator_*`) — review UI. A coordinator routes a worker's output
  to review there; hera carries the *decision* and the *handoff message*, plannotator carries the *review
  surface*.

These are orthogonal — hera does not wrap them and they do not wrap hera. Pick per op: iris when an action
touches the host, plannotator when it's a review surface, hera when it's about roles or messaging.
