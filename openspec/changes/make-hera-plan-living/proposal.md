# Make the hera plan-DAG a living source of truth

## Why

The hera plan-DAG (`add-hera-plan-substrate`, planned nodes + `hera_blocks` edges
+ the gater) atrophies in real use. Three root causes, confirmed by reading the
code:

- **Role-status is decoupled from the message bus.** `hera_send` carries no
  status; role status changes only via an explicit `hera_status` call. A worker
  that says "I'm done" in prose stays `working`, so the gater (which gates on
  role-status `done`) never advances the DAG. This is the root cause of the
  "dead DAG."
- **The plan tools are create-only.** `hera_plan` / `hera_plan_node` /
  `hera_block` only ADD. There is no verb to edit a node, cancel one, drop an
  edge, or fold a manually-spawned worker into the plan. When reality diverges
  the coordinator cannot reconcile the DAG, so it abandons it.
- **The gater never re-arms.** The held-ping dedup (`heldPings`) is set once and
  never cleared, so a recovered-then-re-failed blocker is never re-reported and a
  recovery is never announced. A materialized node also leaves the planned set
  permanently, so a blocker that reopens after its dependent already spawned can
  never re-gate it.

Intent is also ambiguous: the skill frames `hera_plan` as authoring-time, and the
harness `TaskCreate` reminder competes, so coordinators split state across two
trackers and the DAG dies.

## What Changes

Three phases, each shipping as its own PR.

### Phase A — make role-status trustworthy

- **Required status on every worker→coord message.** `hera_send` gains a
  `status` field that is **required** when the sender is a worker (or freelance)
  addressing a coordinator, drawn from the role-status enum. The status is
  applied **synchronously** when the send is processed — never via the
  best-effort delivery path. (**BREAKING** for the `hera_send` tool signature.)
- **New `failed` role-status.** Add `failed` as a fifth role-status value with a
  red ✕ rail glyph, so message + rail + gater stay 1:1. A worker self-declares
  defeat instead of the gater inferring it from a dead session; the gater treats
  a `failed` blocker as a held dependency explicitly.
- **Reopen is structural, not observed.** Because status is required on every
  worker→coord message, a re-engaged worker reports `working` again on its next
  message by enforcement — no separate daemon watcher, no reliance on the worker
  remembering. (An activity-observed auto-reopen was considered and dropped as
  redundant with the required status; see design.md.)
- **Gater re-arm + recovery notice.** Clear the held-ping dedup when a blocker's
  outcome stops being `failed`; emit a one-time "unblocked" notice when a
  previously-held node's blocker recovers. (A notice for an already-running
  node whose blocker reopens was considered and dropped — that case is physics,
  not actionable.)

### Phase B — make the plan mutable

- `hera_plan_node_update(name, [prompt], [project])` — edit a planned node;
  rejected once it has materialized.
- `hera_unblock(blocked, blocker)` — drop one blocking edge (re-point = unblock +
  block).
- `hera_plan_node_cancel(name)` — move a planned node to a single **cancelled**
  terminal state (grey ✕): kept visible in the DAG, excluded from
  materialization, dependents proceed.
- Supporting store + render work: `RemoveHeraBlock`, a node prompt-edit, a
  cancelled marker, and planview rendering of cancelled nodes.

_Deferred:_ `hera_plan_adopt` (fold an already-running worker into a planned-node
slot) was scoped out — it is the heaviest verb and targets the same two-tracker
root cause as the Phase C authority statement. Revisit only if dogfooding shows
the docs + mutation verbs are insufficient.

### Phase C — declare the DAG authoritative

- Update the in-repo hera skill: with a live coordinator binding the plan-DAG is
  the single source of truth; reconcile through `hera_plan*` / adopt rather than
  the harness task list; document the new verbs and the required send-status.
- Update the gotcha files (`orchestration.md`, `dag-rendering.md`,
  `hera-view.md`, `messaging.md`).

## Capabilities

### New Capabilities

_None._ The plan-DAG substrate lives in existing capabilities; this change
extends them.

### Modified Capabilities

- `hera-messaging`: `hera_send` gains the required worker→coord status field and
  its synchronous-apply semantics.
- `hera-coordination`: the role-status enum gains `failed` (with explicit failure
  gating); the structural reopen, the gater re-arm + recovery notice, the three
  plan-mutation verbs (`hera_plan_node_update`, `hera_unblock`,
  `hera_plan_node_cancel`), the cancelled-node store semantics, and the authority
  statement are added. (The supporting store ops — `RemoveHeraBlock`, the node
  prompt-edit, the `cancelled_at` marker — are specified within these
  requirements rather than as a separate `data-persistence` delta.)
- `hera-view`: the rail gains a `failed` glyph and planview gains the cancelled
  node rendering.

## Impact

- **Code:** `internal/mcp/hera.go` + `internal/mcp/hera_plan.go` (tool defs +
  handlers), `internal/db/hera.go` + `internal/db/hera_plan.go` + `schema.go`
  (status enum, cancelled marker, edge-remove, prompt-edit), `internal/heragater`
  (re-arm + recovery notice), `internal/hera/service.go` (synchronous
  status-on-send), `internal/tui/hera` +
  `internal/tui/widget/rolestatusicon.go` (failed glyph, cancelled rendering).
- **Specs:** deltas for `hera-messaging`, `hera-coordination`, `hera-view`,
  `mcp-server`, `data-persistence`.
- **Docs:** `.claude/skills/hera/SKILL.md`, the four gotcha files, README
  Reference appendix (new MCP verbs + the `hera_send` status param), help modal
  only if a TUI key changes (none planned).
- **No web/API surface change** beyond what already exists; TUI + MCP + daemon.
