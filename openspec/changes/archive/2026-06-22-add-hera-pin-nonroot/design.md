# Design: Pin non-root Hera rail items, rendered with a lineage breadcrumb

## Context

The native Hera rail renders a read-only `Model` snapshot. Orchestrators are partitioned by `BuildModel` into `Model.Pinned` / `Active` / `Archived` (`[]OrchView`); the rail's Pinned section (`rail.go:521-526`) iterates `Model.Pinned` only. Roles live inside each `OrchView.Roles` and are rendered nested under their orchestrator.

The mutation surface for pinning a non-root role **already exists and works**:

- `P` → `HeraPage.OnPinToggle` → `App.heraPinToggle` → `Ops.PinToggle` (`ops.go:128`) already branches on `sel.Role` and calls `db.PinHeraRole` / `UnpinHeraRole`.
- `db.PinHeraRole` (`hera.go:484`) stamps `hera_roles.pinned_at` (idempotent via `COALESCE`) and clears `archived_at` (pin and archive are mutually exclusive).

The gap is read-side only: nothing projects `hera_roles.pinned_at` into the model, and the rail never renders a pinned role as pinned. Net effect today: pinning a worker / agent / sub-coordinator silently writes `pinned_at` and the rail looks unchanged — a silent-failure bug.

The out-of-tree Hera plugin (`anutron/hera`, since merged into argus) already shipped this feature: `internal/view/rail_list.go`, tagged **BUG-025** (two-line breadcrumb entry) and **BUG-021** (pinned sub-coordinator hoists its subtree). This change is a faithful **port** of that proven design onto native's model.

### Prior-art reference (plugin)

- `collectPinnedRoles()` walks the whole orchestrator tree, accumulating an `ancestorPath` (`"root › sub › "`), and returns every role that "floats out" (`rolePinnedOut`: pinned AND its coordinator not itself pinned).
- A floated pinned role renders as TWO rows: `railRowPinnedBreadcrumb` (selectable, line 1: dimmed icon + ancestry trail) immediately followed by a `railRowRole` with `isBreadcrumbContinuation` (non-selectable, line 2: bright name + age).
- `drawPinnedBreadcrumbRow` left-truncates the trail with a leading `…` (`truncRunesLeft`) so the nearest parent stays visible.
- A pinned sub-coordinator floats with its whole subtree (children rendered beneath it inside the Pinned block; not re-rendered in the active tree).
- Section order: pinned orchestrators → pinned roles → pinned freelancers, wrapped in `PinnedSep` / `PinnedEnd` rules.

## Goals / Non-Goals

**Goals:**

- Pinning a non-root role (worker / agent / freelance / sub-coordinator) renders it in the Pinned block with a lineage breadcrumb, end-to-end.
- A pinned sub-coordinator hoists its whole nested subtree into the Pinned block (full plugin parity).
- Single placement: a floated pinned role/subtree renders exactly once.
- Fix the silent-failure path (pin currently writes the DB and renders nothing).
- Match native's existing rendering invariants (no `screen.Sync()`, full-rect coverage, cursor re-pin by stable id, filter/collapse correctness).

**Non-Goals:**

- No DB schema change (`pinned_at` exists on `hera_roles`).
- No new keybinding; `P` is already bound and documented as "toggle pin".
- No `railViewState` (fold/selection persistence) change — pin state is DB-backed.
- No change to the unmanaged-freelance concept (native's Freelance section is already explicit `kind=freelance` hera roles, which carry their own `pinned_at`, so they float via the same path; the plugin's separate in-memory `pinnedFreelance` map is not needed).
- No new mutation surface (`Ops.PinToggle` already handles roles).

## Decisions

### D1 — Project per-role pin into `RoleView.Pinned`, not a precomputed `Model.PinnedRoles`

`buildRoleView` will set `RoleView.Pinned = role.PinnedAt != nil`. The Pinned block's role list is then collected at **rail build time** by a new `Rail.collectPinnedRoles()`, mirroring the plugin's `railList.collectPinnedRoles`.

Rationale (deviation from the brief's literal `Model.PinnedRoles []RoleView`):

- The rail needs **pointers** into its own `r.model` for each pinned role (so `Selection.Role` feeds the panes / mutations on the live projection). A value slice on `Model` can't supply those; collecting in the rail walks `r.model` and yields stable `&OrchView.Roles[j]` pointers.
- The breadcrumb is computed from `canonicalParents()` (a rail-consulted primitive that already drives nesting), so co-locating the collection with `buildRows` keeps the breadcrumb trail consistent with how the rail actually nests the subtree.
- `RoleView.Pinned` is also needed per-role inside `appendOrchWorkers` to decide the float-skip, so the primitive flag belongs on `RoleView` regardless.

Alternative considered: `Model.PinnedRoles []RoleView` populated in `BuildModel` (the brief's wording). Rejected because of the pointer-stability requirement above and because the ancestry trail must agree with `canonicalParents` (rail-level). `RoleView.Pinned` + rail collection is strictly more aligned with the proven plugin structure.

### D2 — Two-line breadcrumb entry: selectable line 1, non-selectable continuation line 2

Add a row kind `rrPinnedBreadcrumb` (selectable; the cursor anchors here) and a `breadcrumbCont bool` field on `railRow` for the line-2 `rrRole` continuation (non-selectable). A `breadcrumb string` field on `railRow` carries the trail text for line 1.

- `selectable()`: `rrPinnedBreadcrumb` → true; `rrRole` with `breadcrumbCont` → false.
- `currentRef()` / `restoreCursor()` / `SelectByTaskID()`: a `rrPinnedBreadcrumb` row maps to `role.RoleID`; the continuation `rrRole` is excluded (`!breadcrumbCont`) so it never shadows the anchor. Cursor restore therefore re-pins onto the breadcrumb line by the same stable role id the existing `selection_ref` already persists.
- Draw: a `rrPinnedBreadcrumb` row renders dimmed icon + left-truncated ancestry (`truncRunesLeft`); the continuation row renders the name in `StyleSelected` when the **preceding** breadcrumb row is the cursor (the Draw loop passes `selected || (breadcrumbCont && idx-1 == cursor)`).

Rationale: this is exactly the plugin's cursor model — one logical selection per pinned item, anchored on the line the operator sees the icon + lineage on. `j`/`k` step breadcrumb→breadcrumb (continuation lines are skipped), so navigation feels like one row per item.

### D3 — Breadcrumb lineage = full `canonicalParents()` chain to `role.OrchID`

The trail for a pinned role is the names of the orchestrators on the canonical chain from the **root** down to and including the role's own orchestrator (`role.OrchID`), joined `" › "` with a trailing `" › "` (e.g. `bug-bash-2 › alpha › `). This is computed by walking `canonicalParents()` upward from `role.OrchID`, collecting `OrchByID(...).Name` at each hop, then reversing.

- **Multi-binding is not ambiguous here.** A `hera_roles` row has exactly one `orchestrator_id`; multi-binding fan-out is one *task* surfacing through *distinct role rows* (one per orchestrator). Pinning pins a single role row, so its lineage parent is unambiguously `role.OrchID`.
- **Determinism for nested parents.** When the role's orchestrator is itself a nested sub-coordinator reachable from more than one bridge-parent, `canonicalParents()` already assigns it ONE deterministic parent (the same primitive the rail nests by), so the trail is stable and fold-independent.

Alternative considered: immediate-parent-only trail. Rejected — Aaron's reference shows the full `root › parent` trail, and the plugin computed the full ancestry.

### D4 — Single placement via a float-skip set; pinned sub-coordinator hoists its subtree

`collectPinnedRoles()` returns the floated roles AND a `pinnedFloat` set of their role ids. `buildRows` renders the Pinned section FIRST (it already does), so:

- `appendOrchWorkers` / `appendWorkerRow` skip a worker row whose role id is in `pinnedFloat` (it floated out). A pinned role whose containing orchestrator is **itself pinned** does NOT float (it stays nested under the pinned root) — mirroring the plugin's `rolePinnedOut` (float only when the coordinator is not pinned).
- For a pinned **bridging sub-coordinator** worker row, the Pinned section renders its breadcrumb + name and then nests its **child orchestrator's subtree** beneath it (via `appendOrchWorkers` on the bridged child), marking `placed[child.ID] = true`. Because the Pinned section runs before the active-tree passes, the child is already `placed`, so the active-tree loops and the `structuralReach` safety sweep both leave it folded (it never double-renders or leaks flat). `structuralReach` is unaffected: the child's canonical-parent chain still reaches a root, so it's never treated as a cycle-orphan.

Rationale: native's single-placement guarantee for orchestrators is the `placed` set; rendering the hoisted subtree in the Pinned pass and marking `placed` reuses that guarantee. The worker ROW (a role, not tracked by `placed`) is suppressed in the active tree by `pinnedFloat`. This is the minimal change that preserves every existing nesting invariant (canonical parent, structuralReach, cycle-safety).

### D5 — Fold + Ctrl+D parity on a pinned sub-coordinator breadcrumb row

The pinned sub-coordinator's breadcrumb row carries `collOrchID = child.ID` so:

- `Space` toggles `collapsed[child.ID]` (fold the hoisted subtree in place), matching the plugin's `orchCollapsed(childOrch)`.
- `Selection()` sets `BridgeChildOrchID = child.ID`, so `Ctrl+D` cascades the whole hoisted sub-team (same conservative cascade as when nested), and `Left` parent-nav treats it as a coordinator landing point.

### D6 — Section order and headers

Pinned block order: pinned orchestrators (existing, with subtrees) → pinned roles (two-line entries) → [pinned freelancers fold into the same role path since native freelance roles carry `pinned_at`]. The "Pinned" section header (and its rule) now renders when `len(Model.Pinned) > 0 OR any pinned non-root role floats`. Under an active filter the header is pruned when nothing in the block is visible.

### D7 — Orphan handling

A pinned role's orchestrator is resolved via `OrchByID(role.OrchID)`. In practice it always resolves: the FK guarantees the orchestrator row exists, and a nuked orchestrator cascades `NukeRole` over its roles, so a nuked parent's roles are themselves nuked (`nuked_at` set) and `BuildModel` already skips nuked roles. Defensively, if a pinned role's orchestrator cannot be resolved in the model (unexpected), the role is **not floated** (skipped from the Pinned block) and the skip is logged via `uxlog` — it is never rendered context-free. No "(orphaned)" placeholder is introduced (YAGNI; the state is unreachable in normal operation).

## Risks / Trade-offs

- **Sub-coordinator hoist interacts with native's intricate nesting (`canonicalParents` / `structuralReach` / `placed`).** → Mitigation: render the hoisted subtree in the Pinned pass and mark `placed` so all downstream passes treat it as already-placed; add regression tests at ≥2 nesting levels in BOTH fold states (mirrors `TestRail_HiddenSubCoordCollapsesSubtreeIntoExpando`).
- **Two-line entries shift cursor/restore math.** → Mitigation: the continuation line is non-selectable and excluded from `currentRef`/`restoreCursor`/`SelectByTaskID`; cursor anchors on line 1 by role id (the existing persisted ref), so restore is unchanged in spirit. Covered by a cursor-anchoring test.
- **Filter visibility for floated roles.** → Mitigation: `collectPinnedRoles` applies the active filter (skip non-matching floated roles), matching the plugin; the Pinned header prunes when empty.
- **Breadcrumb width on a narrow rail.** → Mitigation: left-truncate with a leading `…` (nearest parent stays visible), rune-aware (`truncRunesLeft`) per the dag-rendering rune-vs-byte rule; the name line truncates the name independently.

## Migration Plan

No migration. `pinned_at` already exists on `hera_roles`; any rows pinned today (which currently render nothing) will simply start rendering correctly after this change. Single-user, no backwards-compat concerns. Rollback is a code revert (no schema/data change).

## Open Questions

None outstanding. The one design fork (sub-coordinator subtree hoist vs leaf-only) was resolved with Aaron in favor of **full parity (hoist subtree)**.

## Alternatives considered

- **`Model.PinnedRoles []RoleView` populated in `BuildModel`** (brief's literal wording) — rejected for pointer-stability and breadcrumb-consistency reasons (see D1).
- **Single-line `parent › name` entry** — rejected; Aaron's reference and the plugin both use the two-line breadcrumb entry, which affords a fuller lineage trail on a narrow rail.
- **Immediate-parent-only breadcrumb** — rejected in favor of the full canonical chain (D3).
- **Leaf-only pinning (defer sub-coord hoist)** — rejected by Aaron in favor of full parity.

## Discovery findings

- The mutation path (`P` → `Ops.PinToggle` → `db.PinHeraRole`) already handles roles; only the read projection + render were missing.
- A `hera_roles` row has a single `orchestrator_id`; multi-binding is per-task across distinct role rows, so a pinned role's breadcrumb parent is unambiguous.
- Pin state survives restart purely via `pinned_at` (DB); `railViewState` only persists fold/selection, so no persistence change is needed and cursor-restore already keys on role id.
- Native freelance roles are explicit `kind=freelance` hera rows with their own `pinned_at`, so they float through the same role path (no separate in-memory pin map like the plugin needed).

## Acceptance criteria

Pinned-role projection:

- It should set `RoleView.Pinned` true for a role whose `hera_roles.pinned_at` is set and false otherwise.

Two-line render:

- It should render a floated pinned role as a selectable breadcrumb line (dimmed icon + lineage trail) immediately followed by a non-selectable name line.
- It should left-truncate an over-wide breadcrumb trail with a leading `…`, keeping the nearest parent visible.
- It should anchor the cursor on the breadcrumb line and skip the continuation line during `j`/`k` navigation.

Single placement & hoist:

- It should render a floated pinned leaf role exactly once (in the Pinned block, not under its parent in the active tree).
- It should keep a pinned role nested under its coordinator when that coordinator is itself pinned (no float).
- It should hoist a pinned sub-coordinator's whole subtree into the Pinned block and render that subtree exactly once (not also in the active tree), in both collapsed and expanded fold states.

Section & lineage:

- It should show the "Pinned" header when pinned non-root roles exist even if no orchestrator is pinned.
- It should compute the breadcrumb lineage as the full `canonicalParents` chain from root to the role's orchestrator.

Cursor & ops:

- It should re-pin the cursor onto a pinned role's breadcrumb line by role id after a rebuild.
- It should let `P` on a floated pinned role unpin it (returning it to its parent subtree).
