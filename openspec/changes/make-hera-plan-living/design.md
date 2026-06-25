## Context

The hera plan-DAG (`add-hera-plan-substrate`) gave coordinators a durable plan:
**planned nodes** (bindingless worker roles) wired by **`hera_blocks` edges**,
materialized into live workers by `internal/heragater` once every blocker reaches
hera role-status `done`. Live dogfooding shows the DAG atrophies. Three confirmed
root causes (file:line anchors from a substrate read):

1. **Role-status is decoupled from the message bus.** `hera_send`
   (`internal/mcp/hera.go:99`) carries `to`/`body`/`tldr`/`in_reply_to` only — no
   status. Role status changes *only* via an explicit `hera_status` call
   (`internal/mcp/hera.go:723`), which also rolls a worker's task to `in_review`
   (BUG-050, `RollHeraWorkerToReview`). The gater gates on
   `HeraRoleStatusFor(blockerID).Status == done`
   (`internal/heragater/heragater.go:241`). So a worker that says "I'm done" in
   message prose stays `working`, and the DAG never advances.

2. **The plan tools are create-only.** `hera_plan_node` / `hera_block` /
   `hera_plan` (`internal/mcp/hera_plan.go`) only ADD. There is no verb to edit a
   node, drop an edge, or cancel a node. `hera_blocks` has no `RemoveHeraBlock`
   (edges only vanish via FK cascade on role delete). When reality diverges from
   the authored plan the coordinator cannot reconcile it, so it abandons the DAG.

3. **The gater never re-arms.** `holdAndPing` (`heragater.go:406`) already
   dedups the held notice per `(node, blocker)` via `heldPings` — but that key is
   set once and **never cleared**. A blocker that recovers then re-fails is never
   re-reported, and a recovery is never announced. (The brief's "re-emits every
   tick" was inaccurate; the real defect is the missing re-arm.)

Intent is also ambiguous: the skill frames `hera_plan` as authoring-time, and the
harness `TaskCreate` system-reminder competes, so coordinators split state across
two trackers.

This change makes role-status a trustworthy signal, makes the plan mutable, and
declares the DAG authoritative — in three independently-shippable phases.

## Goals / Non-Goals

**Goals:**

- A worker finishing or failing reliably advances the DAG, without anyone
  remembering a second call.
- A coordinator can reconcile a plan to reality: edit a node, drop an edge, cancel
  a node — keeping the DAG a live mirror, not a stale authoring snapshot.
- The gater's held-state self-heals: a recovered blocker clears the hold and tells
  the coordinator; a re-failure re-pings.
- The skill states unambiguously that the plan-DAG is the source of truth for a
  bound coordinator.
- Message + rail + gater share one 1:1 status vocabulary.

**Non-Goals:**

- **No `hera_plan_adopt`.** Folding a manually-spawned/harness worker into a
  planned slot is deferred — heaviest verb, most edge cases, and the Phase C
  authority statement targets the same two-tracker root cause far more cheaply.
  Revisit only if dogfooding shows docs + verbs are insufficient.
- **No activity-observed auto-reopen daemon watcher.** Considered, dropped:
  redundant with the now-required send-status (reopen becomes structural), and it
  adds a render-flap risk plus a direct conflict with self-reported `failed`.
- **No notice for an already-running node whose blocker reopens.** That case is
  physics — a spawned worker cannot be un-spawned — so the notice would be
  unactionable noise.
- **No declarative whole-plan re-submit/diff.** Narrow explicit mutation verbs are
  chosen over a "re-send the desired plan and we reconcile" call: a diff the
  coordinator's model must construct correctly is exactly the failure mode to
  avoid.
- No web/SPA surface (the plan-DAG is TUI + MCP only).

## Decisions

### D1 — `hera_send` carries a required status (worker→coord), applied synchronously

`hera_send` gains a `status` parameter. It is **required** when the resolved
sender role is a `worker` or `freelance` addressing a coordinator; optional/ignored
for a coordinator sender. Its value is the role-status enum (D2). When present the
status is applied to the sender's role **synchronously inside the send handler**
(via the same `UpsertHeraRoleStatus` path `hera_status` uses, including the
BUG-050 worker→in_review roll), *before* the call returns — it never rides the
best-effort `notify.Notifier` delivery path.

- **Why required, not optional-with-default:** a default (`working`) lets a
  forgetful agent silently report `working` forever, reintroducing the exact
  staleness for the `done`/`failed` cases. Erroring on omission forces the
  heartbeat. (Confirmed with Aaron.)
- **Why synchronous, not event-driven:** delivery is async, idle-gated, 5-minute-
  deadline, soft-fail (`internal/notify`), so it can never be trusted for status.
  Applying status in the handler decouples the authoritative state change from
  best-effort delivery.
- **Alternatives considered:** (a) auto-infer "done-type" from message prose —
  rejected, relies on NLP heuristics over the unreliable delivery path; (b) keep
  status fully separate and only nag — rejected, "remember the second call" is the
  failing behavior today.

### D2 — Add `failed` as a fifth role-status (1:1 with the rail)

The role-status enum (`internal/db/hera.go`, `hera_role_status.status` CHECK in
`schema.go:484`) grows from `idle|working|blocked|done` to add `failed`. The rail
gets a red ✕ glyph for it (`internal/tui/widget/rolestatusicon.go`), keeping
message + rail + gater 1:1 at five states. The send-status vocabulary (D1) is
exactly this enum.

- **Why a real status, not inference:** today the gater *infers* failure (a
  blocker whose session ended without `done` →
  `blockerFailed`, `heragater.go:240`). Inference only catches crashes; it misses
  "I tried and give up but I'm still alive," and it is late (waits for session
  death). An explicit self-reported `failed` is timely and unambiguous.
- **Gater coupling:** `blockerOutcome` returns `blockerFailed` *explicitly* when
  `HeraRoleStatusFor(blocker).Status == failed`, taking precedence over the
  inference path. A `failed` worker is held + pinged like any failed blocker.
- **Task roll:** a worker reporting `failed` rolls its bound task to `in_review`
  (so it leaves `in_progress` and surfaces for coordinator attention) but does
  **not** stamp `ready_to_close` (a failed task is not ready to check off). This
  is a sibling of `RollHeraWorkerToReview`; the rail then shows the red ✕ (role-
  status `failed`) rather than the review ✓ (`ready_to_close` wins only when set).
- **Alternative considered:** strict 4-state (reuse today's rail) — rejected by
  Aaron in favor of explicit defeat reporting, which is core to the "living" goal.

### D3 — Reopen is structural (a consequence of D1), not a separate mechanism

When a coordinator hands rework to a `done`/`failed` worker (its session is left
running after a finish), the worker engages and — because every worker→coord
message now *requires* a status (D1) — reports `working` on its next message by
enforcement. That self-report flips the role `done`/`failed → working`. No daemon
watcher, no coordinator status-write, no reliance on the worker *remembering* to
re-report (the requirement is the enforcement).

- **Accepted trade-off:** a brief staleness window — between the coordinator's
  "fix X" and the worker's next message, the role reads `done`/`failed`. Impact is
  bounded: the gater ticks at minute granularity, and a dependent that would
  materialize during the window has almost always already materialized on the
  first `done` (the wedge is physics, D4). If dogfooding shows premature
  materialization in this window, the deferred activity-observed reopen is the
  enhancement.
- **Why not coordinator-writes-recipient-status:** breaks the "a role owns its
  status" invariant; the gater would trust a status the worker never asserted —
  the stale-status drift BUG-003 warned against.

### D4 — Gater re-arm + one-time recovery notice; the materialization wedge is left as physics

On each tick, after classifying planned nodes:

- **Re-arm:** for any `(node, blocker)` key currently in `heldPings`, if the
  blocker's outcome is no longer `blockerFailed` (it recovered to working/done, or
  the edge/role vanished), **clear the key**. A later re-failure then re-pings
  (the dedup re-arms).
- **Recovery notice:** when such a key clears *because the blocker recovered*,
  emit a one-time "unblocked: node X's blocker Y recovered" message to the
  coordinator (same `ping` seam as `holdAndPing`, sent FROM the held node's role
  so the self-send guard never trips — `orchestration.md:61`).
- **The wedge stays:** a node that already materialized has left the planned set
  permanently (`ListHeraPlannedNodes` filters `NOT EXISTS (binding)`), and a
  running worker cannot be un-spawned. No notice (Non-Goals). Planned (not-yet-
  materialized) dependents already re-wait correctly because the gater re-reads
  current blocker status every tick.

### D5 — Narrow plan-mutation verbs (planned-node scope)

Three coordinator-only verbs (`internal/mcp/hera_plan.go`, guarded by the existing
`heroPlanCoordinatorGuard`):

- **`hera_plan_node_update(cwd, name, [prompt], [project], [orchestrator])`** —
  edit a *planned* node's prompt/project. Rejected once the node has materialized
  (`HeraRoleHasBinding` true) — the prompt is already delivered. New store fn
  `UpdateHeraPlannedNode` / `SetHeraRolePrompt`.
- **`hera_unblock(cwd, blocked, blocker, [orchestrator])`** — drop one
  `hera_blocks` edge. New store fn `RemoveHeraBlock(blocked, blocker)`. Re-point =
  unblock + block; no separate verb. Idempotent (dropping a missing edge is a
  no-op success).
- **`hera_plan_node_cancel(cwd, name, [orchestrator])`** — move a planned node to
  a single **cancelled** terminal state: it is kept in the DAG (renders grey ✕),
  excluded from materialization, and its dependents proceed (it no longer gates
  them). Cancelling a node that already materialized is rejected (use the task
  lifecycle to stop a running worker).

**Principle — verbs operate on the *plan*, not live workers.** A materialized node
is a running agent managed by the existing task lifecycle (stop/delete). The
mutation verbs touch planned nodes + edges only; this keeps "edit the plan"
cleanly separate from "kill an agent."

**Cancelled representation:** a `cancelled_at` timestamp column on `hera_roles`
(mirrors the existing `archived_at`/`nuked_at`/`pinned_at` pattern,
`schema.go:446`). `ListHeraPlannedNodes` adds `AND cancelled_at IS NULL` so the
gater never materializes a cancelled node. The gater's `blockerOutcome` treats a
cancelled blocker as *no longer gating* (the dependent proceeds) — equivalently,
the dependent's edge to a cancelled blocker is ignored. Rendering keeps the role
(it is not archived/deleted) so planview can draw it grey ✕.

- **Why a column, not delete:** Aaron chose "kept, visible" over hard delete — a
  reconciled plan should show what was dropped, not silently shrink. One state
  (cancelled), not two (cancelled + superseded): the "why" rides in the
  coordinator's message; a second terminal state earned no distinct behavior.

### D6 — The skill declares the DAG authoritative

`.claude/skills/hera/SKILL.md` states firmly: with a live coordinator binding the
plan-DAG is the single source of truth; author and reconcile all worker activity
through `hera_plan*` (now including update/unblock/cancel); treat the harness
`TaskCreate` reminder as not applicable to coordinated work. Plus: document the
required send-status and the new verbs, and add a "keep the DAG reconciled"
standing order.

- The harness `TaskCreate` system-reminder is Claude Code behavior we cannot
  suppress, but the skill is the lever that tells coordinators where truth lives.
  Risk is that docs alone under-change behavior — which argues *for* the verbs
  (D5), not against the docs.

## Phase breakdown

Each phase ships as its own PR (the coordinator merges; squash-only on drn/argus).

- **Phase A — status trust:** D1 (required synchronous send-status), D2 (`failed`
  status + rail glyph + gater coupling + task roll), D4 (gater re-arm + recovery
  notice). D3 is a consequence of D1, not separate code. Touches `mcp/hera.go`,
  `hera/service.go`, `db/hera.go`+`schema.go`, `heragater`, `widget/rolestatusicon.go`.
- **Phase B — mutable plan:** D5 (three verbs + `RemoveHeraBlock` +
  `SetHeraRolePrompt` + `cancelled_at` + planview cancelled rendering). Touches
  `mcp/hera_plan.go`, `db/hera_plan.go`+`schema.go`, `tui/hera`+`tui/planview`,
  README Reference (new MCP verbs).
- **Phase C — authority + docs:** D6 (skill) + gotcha updates
  (`orchestration.md`, `dag-rendering.md`, `hera-view.md`, `messaging.md`) +
  README Reference (send-status param). Docs only; no behavioral code.

Phase B depends on Phase A only for the shared `cancelled`/status vocabulary in
rendering; the verbs themselves are independent. Phase C depends on A + B existing.

## Data model changes

- `hera_role_status.status` CHECK gains `'failed'` (`schema.go:484`).
- `hera_roles` gains `cancelled_at TEXT` + `idx_hera_roles_cancelled`
  (`schema.go:446`), mirroring `archived_at`.
- No new tables. `hera_blocks` unchanged (edge-remove is a DELETE on the existing
  table).
- Single-user, breaking-changes-OK: the `status` CHECK change is an additive enum
  widening; the new column is nullable. Per repo policy a fresh schema is fine —
  no migration shim.

## Risks / Trade-offs

- **[Breaking `hera_send` signature]** → required status is a hard break for any
  caller. Acceptable: single-user, and the skill + tool description carry the new
  contract; the error message on omission names the valid values.
- **[Reopen staleness window, D3]** → bounded by gater tick granularity and the
  wedge; documented as accepted. Mitigation path (deferred observed-reopen) is
  known.
- **[`failed` blocker held forever if coordinator ignores it]** → same as today's
  failed-blocker behavior; the re-arm + recovery notice (D4) and the cancel verb
  (D5) are the escape hatches (cancel the held node, or fix/unblock the blocker).
- **[Cancelled-blocker gate semantics]** → a dependent of a cancelled node
  proceeds; if the dependent genuinely needed that output the coordinator made a
  judgment call. Documented; the coordinator owns the decision.
- **[Synchronous status in send handler]** → must reuse the exact
  `RollHeraWorkerToReview` invariants (worker-kind only, in_progress-gated,
  idempotent, soft-fail) so it never clobbers human-set state. Pinned by tests.

## Migration Plan

No data migration. Schema is created fresh (repo policy). Phases land as separate
PRs; each is independently revertable. `SW_VERSION` untouched (no static assets).

## Open Questions (for Plannotator review)

- **OQ1:** Should a `freelance`→coord message also require status, or only
  `worker`→coord? (Design assumes both worker and freelance, since both can be
  gated; confirm.)
- **OQ2:** `failed` task roll — roll to `in_review` without `ready_to_close`
  (design choice) vs. leave the task `in_progress` so rework needs no revive.
  Design picks `in_review`/no-ready-to-close for "needs attention" semantics.
- **OQ3:** Recovery notice wording/threshold — emit only on failed→recovered, or
  also on any held→unheld transition? Design emits only on the failed→recovered
  clear.

## Acceptance criteria (map to scenarios in the deltas)

**D1 — required synchronous send-status:**
- it should reject a worker→coord `hera_send` with no `status`
- it should apply the sender's status synchronously before the send returns
- it should roll a worker's task to in_review when the sent status is `done`
- it should not require `status` from a coordinator sender

**D2 — `failed` status:**
- it should accept `failed` as a role-status value
- it should render a worker with role-status `failed` as the red ✕ rail glyph
- it should hold + ping a dependent whose blocker reports `failed`
- it should roll a `failed` worker's task to in_review without ready_to_close

**D4 — gater re-arm + recovery:**
- it should clear the held dedup when a held node's blocker stops being failed
- it should re-ping after a blocker recovers then re-fails
- it should emit a one-time "unblocked" notice when a held node's blocker recovers

**D5 — mutation verbs:**
- it should edit a planned node's prompt
- it should reject editing a node that has materialized
- it should drop a blocking edge (and no-op on a missing edge)
- it should cancel a planned node so it never materializes and dependents proceed
- it should keep a cancelled node visible in the plan (not delete it)
- it should reject all three verbs from a non-coordinator caller

**D6 — authority:** (docs; no testable scenario)
