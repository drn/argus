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
in the TUI's second tab, and agents drive it entirely through the nine `mcp__argus__hera_*` MCP
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

## 3. The nine tools

All take `cwd`. `orchestrator` is optional with exactly one live binding and **required** with 2+.
Arg names below are exact — do not invent others.

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

- **`hera_send(cwd, body, tldr, [to], [in_reply_to], [orchestrator])`** — message another role **in the
  same orchestrator**. `body` and `tldr` are **required**; `tldr` is a one-line summary ≤120 chars,
  written from the recipient's perspective (see §5). Worker/freelance senders may omit `to` — it
  default-routes to the orchestrator's coordinator. **Coordinators must supply an explicit `to`.**
  `in_reply_to` threads a reply to a prior message id. Returns the `message_id`, recipient, and
  delivery mode. Caps: 64 KiB body, 500 unread per recipient, 50 sends/min/sender. Cross-orchestrator
  messaging is not possible — `to` always resolves within your own orchestrator.

- **`hera_inbox(cwd, [orchestrator])`** — fetch all unread messages addressed to your role, oldest
  first. **Reading IS acknowledgment**: this both cancels pending pane deliveries AND marks the
  messages read — no separate `hera_mark_read` needed for normal consumption. Call it whenever you get a
  doorbell.

- **`hera_mark_read(cwd, message_ids, [orchestrator])`** — explicitly mark specific message ids read and
  cancel their pending deliveries. Use when you read via `hera_get_messages` instead of `hera_inbox`.

### Status / tree

- **`hera_status(cwd, status, [orchestrator])`** — set your role status: `idle` | `working` | `blocked`
  | `done`. Mirrored (best-effort) to argus `task_meta` so the coordinator sees it without asking.
  **A `worker`-kind role reporting `status=done` also rolls its bound argus task to `in_review` and
  stamps `ready_to_close`** (visible in the rail) — see §4. Coordinators/freelancers just update status.

- **`hera_tree_updates(cwd, [orchestrator], [since])`** — scan the caller's orchestrator **subtree**
  (nested sub-orchestrators included) for messages since a cursor. Returns **TLDR-only subject lines —
  no bodies** (capped at 200), plus a `next_cursor`. The cursor is stored **per-role** and auto-advances
  when you omit `since`; passing an explicit `since` is a one-off scan that does NOT clobber the stored
  cursor. The token-efficient way to see whole-team activity without flooding context with bodies.

- **`hera_get_messages(cwd, ids, [orchestrator])`** — fetch full message bodies by id list, after
  scanning `hera_tree_updates`. Access is scoped to the caller's orchestrator **subtree** (sender OR
  recipient must live in it); inaccessible / missing ids get a per-id `error` field rather than a
  top-level error.

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
- **This task holds 2+ bindings?** Pass `orchestrator=` on EVERY tool call.
- **Got a doorbell?** Call `hera_inbox(cwd=$PWD)` immediately — the content is in the inbox, not the
  doorbell line.
- **Want whole-team state?** `hera_tree_updates(cwd=$PWD)`, then `hera_get_messages(ids=[…])` for the
  ones worth reading.
- **How completion flows back:** a worker finishing calls `hera_status(done)` (and usually a closing
  `hera_send` to the coordinator). For a `worker`-kind role that rolls its argus task to `in_review` +
  `ready_to_close`, which the coordinator sees in the rail without asking. The roll is idempotent and
  only fires when the task is still `in_progress` — it never auto-completes or clobbers a human-set
  status. The live session is left running.
- **Don't** use `hera_send` to talk to the human — the human reads the coordinator's own agent pane;
  the bus is role-to-role only.

## 5. TLDR discipline

Every `hera_send` requires a `tldr` ≤120 chars, written from the recipient's perspective. It is shown
in the doorbell, returned by `hera_tree_updates`, and stored permanently.

- Good: `"PR #47 open, tests green, needs review"` / `"Blocked on missing API key — need rotation"`
- Bad: `"update"` (says nothing) / `"Done with the work"` (no specifics) / multi-line.

## 6. Gotchas worth calling out

- **Spawned workers default to the project's stale default branch, NOT the coordinator's branch.**
  `hera_spawn_worker`'s `branch` defaults to the *project* default (e.g. an old `master`/`main`), not the
  coordinator's current worktree branch. If the worker must build on the coordinator's (or a sibling's)
  work, **pass `branch=` explicitly and verify ancestry** — otherwise the worker starts from stale code.
- **Bake all requirements into the initial `hera_spawn_worker` prompt.** Mid-flight `hera_send` to a
  worker is often missed: delivery is idle-gated and best-effort, so a busy worker never receives it and
  an idle/finished worker may not act on it. Put the full spec in the spawn prompt; verify via the branch
  diff and re-dispatch a fresh worker if one idled out without the requirement.
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
   `hera_send(cwd=$PWD, body="<question + context>", tldr="Need decision: X vs Y for the cart schema")`
   (no `to` needed — default-routes to the coordinator), then check `hera_inbox(cwd=$PWD)` on the
   doorbell for the answer.
4. Land your work (open a PR via iris, or leave commits for the coordinator to pull).
5. `hera_send(cwd=$PWD, body="<summary + PR link>", tldr="cart-api done, PR #47, tests green")` then
   `hera_status(cwd=$PWD, status="done")` — which rolls your task to in_review + ready_to_close so the
   coordinator sees you finished.

### Worker promotion: becoming a sub-coordinator

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
