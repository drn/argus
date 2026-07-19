## Context

Aaron asked for a "kanban" status specific to top-level Hera coordinators — backlog/active/done/blocked — grouped in the rail with dividers, cycled by a new hotkey. Two existing axes already live on `HeraOrchestrator` and must NOT be confused with the new one:

- `pinned_at` / `archived_at` (`P` key, `a`/`Ctrl+D` for roles) — visibility/placement flags, mutually exclusive with each other, already independent of everything else.
- `hera_role_status` (`idle`/`working`/`blocked`/`done`/`failed`, the `s`/`S` keys, `ActHeraStatAdv`/`ActHeraStatRev`) — a per-ROLE ladder (any role, not just a top-level coordinator's header) that drives real behavior (rolling a worker to `in_review` on `done`). Two of its four values (`blocked`, `done`) collide in *name* with two kanban values, which is exactly the confusion Aaron's brief calls out — the new axis gets its own Go type (`db.HeraKanbanStatus`), never `db.HeraRoleStatusValue`, so the two can never be typo'd into each other at a call site.

The new axis is scoped to the ORCHESTRATOR row, not any role, and only meaningful for a TOP-LEVEL (root) orchestrator — a nested/bridged sub-coordinator has no independent rail slot to render a kanban group under (it always renders inline under its parent's subtree; see the "Rail keybindings" and "Rail sections" requirements' existing depth/canonical-parent machinery).

## Goals

- A 4-state, operator-set "what's this orchestration's real-world status" signal per top-level coordinator, fully independent of task lifecycle and role status.
- Rail grouping that triages at a glance: active work stays exactly where it is today (headerless, top of the non-pinned list); backlog/blocked/done recede into labeled, clearly separated groups below it.
- Zero behavior change for anyone who never touches the new hotkey — every existing orchestrator defaults to `active` and renders byte-identical to today.

## Non-Goals

- No change to `pinned_at`/`archived_at` semantics or precedence (pin still wins placement; archive still wins visibility into the bottom expando).
- No change to `hera_role_status`, the `s`/`S` ladder, or `RollHeraWorkerToReview`/`ClearHeraReadyToClose` — completely untouched.
- No kanban status (data, grouping, or hotkey) for nested/non-root coordinators, workers, or freelance roles. **Design default, flagged for coordinator confirmation per the task brief:** only a true root (no canonical parent, per `Model.canonicalParents()`) is eligible. If Aaron actually wants nested sub-coordinators included too, that is a materially different (and materially harder — nested coordinators have no independent rail slot to group under) follow-up, not a same-PR tweak.
- No CHECK constraint on the new column (see Decision 2) and no collapse/fold state for the new Backlog/Blocked/Done group headers (they are plain, always-expanded labels like "Pinned" — the individual orchestrator headers under them keep their existing independent collapse).
- No new REST mutation endpoint — `GET /api/hera` gains the read field; mutating kanban status over REST falls under the same already-documented, already-named standing exception ("hera mutations are TUI-only") as every other Hera rail mutation. Not re-litigated here.

## Decisions

### D1 — Persist on `HeraOrchestrator`, not `task_meta`

Considered both precedents this repo already has for "attach state to a hera concept": the `task_meta` sidecar (task-addressed: `namespace="hera"`, e.g. `ready_to_close`, `context_size`) and a direct DB column on `hera_orchestrators` (`pinned_at`, `archived_at`, `nuked_at`, `base_branch`).

**Chose the DB column.** Kanban status is a property of the *orchestrator row itself*, not of whichever task happens to be bound as its coordinator right now — a coordinator's binding can end and restart (recycle, revive) without the orchestrator's identity changing, and `task_meta` cascades away entirely on task delete/archive (`gotchas/misc.md`: "`task_meta` cascade is asymmetric with archive"). Anchoring kanban status to the task would make it disappear exactly when a coordinator is torn down and rebuilt, which defeats the point of a durable "where does this effort stand" marker. The DB column also puts kanban status in the same place, with the same access pattern (`HeraOrchestrator(id)` read, dedicated `SetHeraOrchestratorKanbanStatus` write, `MutateStore` interface entry), as pin/archive/nuke — no new read path, no new interface shape for `Ops` to learn.

### D2 — No CHECK constraint on the new column

`hera_role_status.status` already carries a `CHECK (status IN (...))` — but that constraint only takes effect via `CREATE TABLE IF NOT EXISTS`, which is a no-op against an existing on-disk DB; SQLite cannot `ALTER ... ADD CONSTRAINT` or widen an existing CHECK. The `make-hera-plan-living` change hit this directly (widening `hera_role_status` to add `failed` silently no-oped on the live dogfood DB — a parked follow-up, not shipped). `hera_orchestrators` is an EXISTING table (unlike `hera_role_status`, which was net-new when its CHECK was written), so a CHECK added only to the `CREATE TABLE IF NOT EXISTS` branch would be **inert for every already-existing installation** — exactly the kind of silent, misleading half-migration that bit the prior change. The existing `nuked_at`/`base_branch` columns on this same table already establish the answer for "new column, existing table": plain `TEXT`, no CHECK, Go-level enum validation only (`db.HeraKanbanStatus`'s constructors/setters are the only writers). Consistent, and doesn't manufacture a second silent-no-op migration bug in the same table.

### D3 — Default `active`, not `backlog`

An orchestrator with no explicit kanban status is, almost by definition, one someone is actively driving (that's why native Hera exists at all — nothing sits around undriven for long). Defaulting new/existing/unmigrated rows to `backlog` would make every pre-existing orchestrator silently vanish from the headerless top group into a labeled "Backlog" bucket the instant this ships — a surprising, unrequested reshuffle of the rail's current appearance for zero operator action. Defaulting to `active` keeps `buildRows()`'s current unconditional-headerless-Active-list behavior exactly as it is today until an operator deliberately presses `m`/`M`.

### D4 — Rail-order grouping lives inside the existing "Active" bucket, not as new top-level sections

Aaron's stated rail order is "pinned, active, backlog, blocked, done" — note this does *not* mention where Freelance/Archive sit, because those are unaffected: Pinned still always wins placement (unaffected by kanban), Archive still always sits at the bottom (unaffected by kanban). The four kanban values partition exactly what today is the single flat "Active" bucket (`Model.Active`, everything not pinned and not archived). Concretely, in `Rail.buildRows()`:

- The existing two-pass "Active" render (roots, then the true-cycle-orphan safety sweep) is repeated once per kanban value, in order `active → backlog → blocked → done`, each pass filtered to top-level orchestrators (`!consumed[id]`, i.e. absent from `canonicalParents()`) whose `KanbanStatus` matches.
- The `active` group renders exactly as today: no header, and the existing Pinned→Active divider rule (`pinnedRendered && this group produced ≥1 row`) is unchanged.
- The `backlog`/`blocked`/`done` groups each render a plain `"Backlog (N)"` / `"Blocked (N)"` / `"Done (N)"` `rrSectionHeader` (non-selectable, no collapse — mirroring the plain "Pinned" label, not the collapsible Freelance/Archive fold headers), unconditionally preceded by an `rrRule` divider whenever that group has ≥1 row — the SAME unconditioned-divider convention the existing Freelance/Archive sections already use (they always lead with a divider when non-empty, regardless of what rendered above). This sidesteps having to track "was anything rendered above" state across three more group boundaries and reuses a pattern the codebase already trusts.
- A group with zero matching orchestrators renders nothing at all — no header, no divider (mirrors "Archive section omitted when nothing archived").
- Nested/bridged orchestrators are invisible to this whole scheme: `appendOrch`'s recursive calls for coord-spawned/worker-bridged children pass along the same `depth`/`canonical` machinery unchanged, so a nested sub-coordinator's own `KanbanStatus` value is simply never consulted for placement (only true roots are).

### D5 — `m`/`M` gating: exactly a top-level coordinator header, cyclic (wrapping) not clamped

Reuses the existing `Selection` plumbing (`rail.Selection()` already resolves `(Role, Orch)` off the cursor) with one new boolean, `Selection.TopLevelOrch`, stamped from the same `canonical` map `buildRows` already computes (an orchestrator absent from `canonicalParents()`). The mutation fires only when `sel.Role == nil && sel.Orch != nil && sel.TopLevelOrch` — i.e. the cursor rests on an orchestrator HEADER row that is a true root, never a role row (worker, freelance, or a bridging sub-coordinator row, which carries `sel.Role != nil` even though it *visually* looks like a coordinator), and never a nested orchestrator header reached via `appendOrch`'s recursive placement.

Unlike `Ops.StepStatus`'s `heraStatusLadder` (which CLAMPS at both ends — advancing past `done` is a no-op), the new `kanbanOrder` ladder WRAPS in both directions, per Aaron's explicit "wrapping to active" instruction — a deliberately different stepping rule, so it gets its own small ladder/index helpers rather than reusing or parameterizing `ladderIndex`/`heraStatusLadder` (which exist specifically to clamp; overloading them with a wrap flag would make the one existing clamped caller (`StepStatus`) harder to read for no shared benefit — two four-line functions is cheaper than one branchy one).

Pinned and archived top-level coordinators are still legitimate `m`/`M` targets (kanban status is independent of both), and Aaron's brief explicitly asks that a pinned coordinator's *placement* never move — it doesn't, since pin placement is resolved entirely before kanban grouping ever runs.

## Open Questions

- Nested-coordinator scope (Non-Goals) is a design default, not a confirmed decision — flagged to the coordinator/Aaron in the same message carrying this proposal, per the task brief.
