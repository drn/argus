# hera-view delta: rail parity

## REMOVED Requirements

### Requirement: Rail structure is a flat, single-level orchestrator list (area 1)

**Reason:** Replaced by inline sub-orchestrator nesting (see ADDED "Rail nests sub-orchestrators under their bridging worker row"). The flat rail was the headline parity regression per `docs/RAIL-PARITY-ANALYSIS.md`.

## ADDED Requirements

### Requirement: Rail nests sub-orchestrators under their bridging worker row (area 1)

The system SHALL build the rail as a nested tree of display rows from the read-only model. Each root orchestrator (one with no bridging parent in the rendered set) renders at depth 0; an expanded orchestrator's directly-bound roles render at `depth+1`; and a sub-orchestrator renders indented beneath the row that bridges it, recursively. There are two nesting shapes:

- **Worker-bridged child:** when a parent WORKER row's bridge task equals the child's coordinator bridge task, the child's workers nest one level beneath that worker row (the worker row IS the child's coordinator surrogate — no separate child header).
- **Coordinator-spawned child:** when the parent's COORDINATOR is also the child's coordinator (the shared-coordinator shape — there is no worker row to host it), the child nests as its OWN collapsible sub-orchestrator header directly under the parent, at the worker depth, recursively.

Nesting consumes the corrected subtree (see "Multi-binding bridge keys off the latest binding"). A visited-orchestrator (`placed`) set guards cycles so each orchestrator is placed at most once. An archived sub-orchestrator reached through a bridge renders dimmed in place (it is NOT dropped from its parent's subtree, distinct from the bottom Archive section which lists archived ROOT orchestrators).

An archived WORKER that bridges a not-yet-placed child renders in place (its row dimmed) rather than being hoisted into the per-coordinator Archive expando, so its child sub-team still nests — a done sub-coordinator must not strand its live subtree at the top level. Only archived LEAF workers (bridging no unplaced child) fold into the expando. The archived bridging worker's ROW dims, but its child subtree dims only from inherited dim or the child's own archived state (an active child under an archived bridging worker stays normal).

A PINNED orchestrator is ALWAYS a top-level root (rendered in the Pinned section), even when a worker bridges it — user pin intent wins over nesting. After the root pass, a safety sweep places any active orchestrator left unplaced by a pure bridge cycle as a top-level root, so a cycle-orphaned orchestrator never vanishes from the rail.

Derived from: `internal/tui/hera/rail.go` (`buildRows`, `appendOrch`, `appendOrchWorkers`, `appendWorkerRow`, `workerBridgeChild`), `internal/tui/hera/model.go` (`coordBridgeChildren`), `docs/OLD-RAIL-SNAPSHOT.md` (target layout: 6 roots / 19 nested).

#### Scenario: Sub-orchestrator nests under its bridging worker

- **WHEN** worker task `T` under orchestrator P is also the coordinator (bridge task) of child orchestrator C
- **THEN** C renders indented beneath P's worker row for `T`, and C is not also rendered as a top-level root

#### Scenario: Cycle is placed once

- **WHEN** two orchestrators bridge each other (a cycle)
- **THEN** the visited-set places each orchestrator exactly once and the rail build terminates

#### Scenario: Pure-cycle orphan still surfaces as a root

- **WHEN** every orchestrator in a bridge cycle is consumed (bridged by another) so none qualifies as a root
- **THEN** the safety sweep renders the unplaced orchestrators as top-level roots — nothing vanishes from the rail

#### Scenario: Pinned orchestrator stays top-level when bridged

- **WHEN** a pinned orchestrator's coordinator task is also a worker under another orchestrator (it is bridged)
- **THEN** it still renders in the Pinned section at depth 0, not nested under the bridging worker

#### Scenario: Archived bridge renders dimmed in place

- **WHEN** a sub-orchestrator reached through a bridge is archived
- **THEN** it renders nested under its parent in the dimmed style rather than being dropped or hoisted to the bottom Archive section

#### Scenario: Coordinator-spawned sub-team nests as a sub-header

- **WHEN** the parent's coordinator is also the child's coordinator (no worker row bridges the child)
- **THEN** the child renders as its own collapsible sub-orchestrator header indented under the parent, foldable with Space, and not as a top-level root

#### Scenario: Archived bridging worker nests its live child in place

- **WHEN** a parent worker is archived but still bridges a not-yet-placed (live) child orchestrator
- **THEN** the worker row renders in place dimmed (not hoisted into the Archive expando) and its child nests beneath it, with the live child subtree rendered in the normal (non-dimmed) style

#### Scenario: Archived leaf worker folds into the expando

- **WHEN** an archived worker bridges no unplaced child orchestrator
- **THEN** it still folds into the per-coordinator `Archive (N)` expando rather than rendering in place

### Requirement: Multi-binding bridge keys off the latest binding with a teardown guard (area 1)

The system SHALL determine the parent→child bridge from each role's LATEST binding regardless of liveness, not the live binding alone. The in-memory rail/tree bridge SHALL match `db.SubtreeOrchIDs` exactly: a parent orchestrator P nests a child orchestrator C when C's coordinator's latest-binding task ALSO has a non-teardown latest binding under P through ANY of P's roles — a WORKER role (a spawned worker that became a sub-coordinator) OR P's own COORDINATOR role (the coordinator-spawned sub-team that `hera_new_orchestrator` creates, where one coordinator agent runs both P and C). The earlier in-memory bridge honoured only worker roles, so coordinator-spawned sub-teams rendered flat as extra top-level roots; matching `SubtreeOrchIDs`' ANY-parent-side-binding join closes that gap.

When P and C share the SAME coordinator bridge task (so `SubtreeOrchIDs` would symmetrically include each from the other — an A↔B cycle), the rail breaks the symmetry deterministically: the orchestrator whose coordinator role has the LOWER role id is the parent (it was created first), and the later one is the spawned sub-team that nests under it.

An ended binding still bridges UNLESS its `end_reason` is an operator-teardown reason (`reparented` or `user_deleted`); every other end reason (`argus_deleted`, `task_missing`, normal session end) leaves the structural link intact. The parent-side role's ARCHIVED state does NOT break the bridge (`SubtreeOrchIDs` has no archived-role filter on the parent side) — an archived worker that bridges a live child still nests it (see the rail-nesting requirement). This rule is applied identically by the DB-side `SubtreeOrchIDs` (TLDR roll-up) and the in-memory `workerTaskSet`/`heraTreeNodes`/`coordBridgeParentOf` (rail + Details tree). Archived CHILD orchestrators are still pruned as descendants in `SubtreeOrchIDs` and in the coordinator-bridge path.

Derived from: `internal/db/hera_subtree.go` (`heraSubtreeOrchIDs`), `internal/db/hera.go` (`ListHeraLatestBindings`, teardown-reason constants), `internal/tui/hera/tree.go` (`workerTaskSet`, `heraTreeNodes`), `internal/tui/hera/model.go` (`RoleView.BridgeTaskID`/`LinkEndReason`, `OrchView.CoordBridgeTaskID`, `coordBridgeParentOf`, `coordBridgeChildren`, `consumedSet`).

#### Scenario: Ended-but-not-torn-down bridge still nests

- **WHEN** a coordinator's binding has ended for a non-teardown reason (its task completed) and a parent worker's latest binding points at that coordinator's task
- **THEN** the child orchestrator still bridges and nests under the parent

#### Scenario: Torn-down link does not nest

- **WHEN** a parent worker's latest binding ended with reason `reparented` or `user_deleted`
- **THEN** that worker does NOT bridge the child orchestrator (the link is stale)

#### Scenario: Coordinator-spawned sub-team nests under the parent

- **WHEN** one coordinator agent's task is the coordinator of BOTH orchestrator P and orchestrator C (P's coordinator role id is lower than C's)
- **THEN** C nests under P as a sub-orchestrator and is NOT also rendered as a top-level root

#### Scenario: Shared-coordinator cycle is broken by earliest role id

- **WHEN** orchestrators P and C share a coordinator bridge task (a symmetric A↔B link)
- **THEN** only the orchestrator with the lower coordinator role id is a root and the other nests under it (never both as co-roots, never a hang)

### Requirement: Coordinator folds into the orchestrator header (area 3)

The system SHALL NOT render an orchestrator's coordinator-kind role as its own child row. `appendOrch` SHALL skip the coordinator role when listing children, and the orchestrator header row SHALL carry the coordinator's status glyph (the header IS the coordinator). A worker-less orchestrator therefore renders header-only.

Derived from: `internal/tui/hera/rail.go` (`appendOrch` coordinator skip, `drawOrchRow` coordinator glyph).

#### Scenario: No redundant coord child row

- **WHEN** an orchestrator with a coordinator role and one worker is expanded
- **THEN** the rail shows the orchestrator header plus the single worker row, with no separate `coord` child row

#### Scenario: Header carries the coordinator status glyph

- **WHEN** the orchestrator's coordinator role has a status
- **THEN** the orchestrator header row renders that coordinator's status glyph

### Requirement: Per-coordinator archive expando (area 2)

The system SHALL render an orchestrator's archived roles in a per-coordinator `Archive (N)` expando under that orchestrator's active agents, collapsed by default. This is distinct from the bottom Archive section, which lists archived ROOT orchestrators. The expando appears only when the orchestrator has at least one archived role and toggles with Space like other collapsible rows.

Derived from: `internal/tui/hera/rail.go` (per-orchestrator archive expando in `appendOrch`).

#### Scenario: Archived roles fold under their coordinator

- **WHEN** an orchestrator has active workers and archived workers
- **THEN** an `Archive (N)` expando renders under the active workers, collapsed by default, listing the archived roles dimmed when expanded

### Requirement: Running agents animate a spinner glyph (area 3)

The system SHALL render a running (hera status `working`) role's status glyph as an animated spinner frame from the active spinner (`widget.SpinnerFrame`), advancing with the wall-clock frame counter, rather than a static glyph. Non-running states (idle, blocked, done, ready_to_close, unbound) remain static. `ready_to_close` still takes precedence over the spinner.

Derived from: `internal/tui/hera/rail.go` (`statusIcon` spinner branch), `internal/tui/widget/spinnerstate.go` (`SpinnerFrame`).

#### Scenario: Working role spins

- **WHEN** a role's hera status is `working` and it is not ready_to_close
- **THEN** its status glyph is the active spinner's frame for the current animation frame, and the glyph differs across frames

#### Scenario: Non-working role is static

- **WHEN** a role's hera status is `idle` or `done`
- **THEN** its status glyph does not animate

### Requirement: PR indicator on rail role rows (area 3)

The system SHALL render a `PR` indicator on a managed (non-coordinator) rail role row when that role's bound task carries a non-empty `url` in the daemon-populated `task_meta` "pr" namespace. The indicator is best-effort, read once per refresh via `ListMetaByNamespace("pr")` and threaded into the rail; it is never fetched by the view. It reuses the same cached `prMeta` the Details roster reads.

Derived from: `internal/tui/hera/rail.go` (PR cell in `drawRoleRow`), `internal/tui/hera/page.go` (`doRefresh` reads "pr", passes `prMeta` to the rail).

#### Scenario: PR mark on a managed rail row

- **WHEN** a managed role's bound task has a non-empty "pr" url
- **THEN** its rail row renders a `PR` indicator

### Requirement: Ctrl+D on a bridging row cascades the nested sub-team (area 7)

The system SHALL, when Ctrl+D is pressed on a worker row that bridges a nested sub-orchestrator, tear down the WHOLE nested sub-team rooted at that child — the child orchestrator and every orchestrator nested beneath it — behind a confirmation modal. The modal MUST be explicitly destructive: it states how many orchestrators and agents are removed and how many worktrees + branches are destroyed. On confirm, each orchestrator's sole-bound managed tasks are destroyed (session stopped, worktree + branch removed, binding ended) and the orchestrator is deleted (cascading its roles/bindings); a task bound in another (non-subtree) orchestrator — e.g. a bridge task still held by the parent — is PRESERVED (multi-binding safety). A non-bridging row keeps the conservative single-role delete.

Derived from: `internal/tui/hera/rail.go` (`Selection.BridgeChildOrchID`), `internal/tui/hera/model.go` (`BridgeSubtree`), `internal/tui/heraactions.go` (`heraCascadeDeleteSubtree`, `heraDoCascadeDelete`).

#### Scenario: Cascade warns and tears down the subtree

- **WHEN** Ctrl+D is pressed on a worker row that nests a sub-team and the operator confirms
- **THEN** the confirm modal states the number of orchestrators/agents/worktrees affected, the child orchestrator and its sole-bound tasks are destroyed, and a bridge task still bound under the parent is preserved

#### Scenario: Non-bridging row keeps conservative delete

- **WHEN** Ctrl+D is pressed on a worker row that nests no sub-team
- **THEN** only that role is removed (the underlying task is destroyed only if sole-bound), with no subtree cascade

## MODIFIED Requirements

### Requirement: Status-icon precedence on role rows (area 3)

The system SHALL choose a role row's status glyph by this precedence: (1) `ready_to_close` wins over everything with a distinct review glyph; otherwise (2) the hera role status when present — `working` renders the ACTIVE SPINNER's animated frame (see "Running agents animate a spinner glyph"), while `blocked`, `done`, and `idle` each map to a distinct STATIC glyph/style; otherwise (3) binding presence (`Live`) renders a "live" glyph; otherwise (4) an unbound/dimmed glyph. `ready_to_close` is read from the task-addressed `task_meta` "hera" namespace, not the hera tables.

Derived from: `internal/tui/hera/rail.go` (`statusIcon`), `internal/tui/hera/model.go` (`buildRoleView` reads `ready_to_close`).

#### Scenario: ready_to_close overrides status

- **WHEN** a role's bound task carries `meta:hera.ready_to_close=true` AND the role status is working
- **THEN** the row renders the review/ready glyph, not the working spinner

#### Scenario: Working renders the animated spinner

- **WHEN** a role has a status of `working` and is not ready_to_close
- **THEN** the row renders the active spinner's frame (animated), not a static glyph

#### Scenario: Hera role status drives the glyph

- **WHEN** a role has a status row of `blocked` and is not ready_to_close
- **THEN** the row renders the needs-input/blocked glyph (static)

#### Scenario: Live-but-statusless role

- **WHEN** a role holds a live binding but has no status row and is not ready_to_close
- **THEN** the row renders the in-review "live" glyph rather than the unbound glyph

### Requirement: Orchestration tree projects the role hierarchy in-memory (area 6)

The system SHALL project the embedded graph's nodes from the rail's already-built model via `heraTreeNodes` — a pure in-memory read with no DB call and no provider seam. The graph renders the role hierarchy (coordinator → workers → sub-coordinators), NOT the retired `depends_on` edges. Each worker gets a synthetic edge to its orchestrator's coordinator; a sub-coordinator collapses to one node keyed by task ID, carrying both a parent edge (its worker role under the parent) and child edges (its own workers). The subtree is discovered by multi-binding BFS keyed off the LATEST binding with the teardown guard: orchestrator C is a child of P when C's coordinator's bridge task is bound as a non-coordinator worker (by bridge task, live or ended-but-not-torn-down) under P. Archived orchestrators are pruned as descendants; the coordinator root takes no self-edge (cycle-safe). Node colour comes from the bound task's argus status/result.

Node DISCOVERY uses the broadened latest-binding bridge, but node EMISSION is intentionally LIVE-only: only roles with a live binding become graph nodes, and a worker's synthetic edge targets its orchestrator's LIVE coordinator task. So a descendant discovered solely through an ended (non-torn-down) coordinator binding contributes no node until it regains a live coordinator — the graph shows live structure, while the rail (which renders finished rows read-only) shows the fuller bridged tree. This live-only emission is deliberate, not a discovery/render mismatch.

Derived from: `internal/tui/hera/tree.go` (`heraTreeNodes`, `workerTaskSet`), `internal/tui/hera/page.go` (`rebuildDAG`), `internal/tui/hera/model.go` (`BridgeTaskID`/`CoordBridgeTaskID`, `TaskStatus`/`TaskResult`).

#### Scenario: Coordinator + workers renders a real tree

- **WHEN** a coordinator with bound workers is selected
- **THEN** the tree shows each worker as a child node of the coordinator root

#### Scenario: Sub-coordinator bridges two subtrees

- **WHEN** a worker task under P is also the coordinator of child orchestrator C
- **THEN** that task collapses to one node holding both its parent edge (under P) and its child edges (C's workers)

#### Scenario: Bridge survives an ended coordinator binding

- **WHEN** child C's coordinator binding has ended for a non-teardown reason and its bridge task is a worker under P
- **THEN** C is still discovered as a descendant of P

#### Scenario: No orchestrator selected yields an empty graph

- **WHEN** no orchestrator is selected
- **THEN** the tree renders empty without panic
