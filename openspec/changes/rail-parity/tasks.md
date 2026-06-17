# Tasks: rail-parity

One commit per stage so the branch can be split during review. Tests land with
each stage (TDD: red → green). `make pre-pr` must pass before pushing.

## 1. Bridging breadth (load-bearing prerequisite)

- [x] 1.1 Add `HeraEndReasonReparented`/`HeraEndReasonUserDeleted` constants and a `ListHeraLatestBindings()` (one binding per role, max id) method to `internal/db/hera.go`.
- [x] 1.2 Rewrite `internal/db/hera_subtree.go` `heraSubtreeOrchIDs` SQL to bridge on each role's LATEST binding (not live-only), excluding teardown reasons; keep the archived-orch prune + cycle guard.
- [x] 1.3 Add `ListHeraLatestBindings` to `internal/tui/hera/reader.go`; add `BridgeTaskID`/`LinkEndReason` to `RoleView` and `CoordBridgeTaskID()` to `OrchView` in `model.go`; populate from latest bindings in `BuildModel`.
- [x] 1.4 Port `internal/tui/hera/tree.go` `workerTaskSet`/`heraTreeNodes` to bridge on `BridgeTaskID` + teardown guard + `CoordBridgeTaskID`.
- [x] 1.5 Tests: db subtree latest-binding bridge + teardown exclusion; tree.go bridge over ended-but-not-torn-down bindings.

## 2. Fold coordinator into the orchestrator header

- [x] 2.1 `appendOrch` SKIPS the coordinator-kind role when listing children.
- [x] 2.2 `drawOrchRow` carries the coordinator's status glyph (the header IS the coordinator).
- [x] 2.3 Tests: no `coord` child row renders; header shows the coordinator status glyph.

## 3. Nest the rail

- [x] 3.1 Rewrite `buildRows`/`appendOrch` to nest sub-orchestrators under their bridging worker row, recursively, consuming the corrected subtree. Cycle guard (visited set) + archived-bridge traversal (dim, don't drop).
- [x] 3.2 Verify the Details DAG still resolves orchs by ID (untouched).
- [x] 3.3 Tests: SimulationScreen + structural assertions against `docs/OLD-RAIL-SNAPSHOT.md` shape (roots vs nested, cycle-prune, archived-dim).

## 4. Per-coordinator Archive (N) expando

- [x] 4.1 Render archived roles under each coordinator's agents in a collapsed-by-default expando (separate from the bottom archived-orchestrator section).
- [x] 4.2 Carry the GUARD-destructive lessons on the archive path (confirm before archiving live work; teardown ends ALL prior links by role id).
- [x] 4.3 Tests: expando renders + collapses; live-work archive is confirm-gated.

## 5. Spinner

- [x] 5.1 `statusIcon` returns an animated `widget.SpinnerFrame` glyph for a running (working) agent; static otherwise.
- [x] 5.2 Tests: working role yields a spinner frame that advances with the frame counter; non-working roles stay static.

## 6. PR indicator on rail rows

- [x] 6.1 Thread `prMeta` into the rail; render a `PR` cell on managed role rows from the "pr" namespace url.
- [x] 6.2 Tests: role with a "pr" url renders the PR cell; without, no cell (no name shift).

## 7. Validate

- [x] 7.1 `openspec validate --strict` (LOCAL only — never wired into CI/make).
- [x] 7.2 `make pre-pr` green.

## 8. Bridge-fix follow-up: nest done sub-teams (rail under-nesting)

The rail still rendered ~24 top-level coordinators on real data because the
in-memory bridge consumed children via roles the rail never nested under. Two
causes, both in the same class (consume ≠ place → safety-swept flat), neither a
liveness/teardown issue:

- [x] 8.1 Coordinator-spawned sub-teams: `coordBridgeParentOf` + `Model.coordBridgeChildren` (shared coord bridge task, earlier coord role id = parent); fold into `consumedSet` and `BridgeSubtree`. `appendOrchWorkers` nests coord-spawned children under the parent header. `tree.go` BFS reaches them too.
- [x] 8.2 Archived bridging workers: an archived worker bridging a not-yet-placed child renders in place (dimmed) via `workerBridgeChild` instead of hoisting to the per-coordinator Archive expando, so its live child nests; archived leaf workers still fold. Active child subtree stays non-dimmed.
- [x] 8.3 Tests: coord-spawned nest + cycle direction (model + rail render + tree); archived-bridging-worker in-place nest + archived-leaf still hoists; ≥20-nested-under-6-roots large-shape regression. All prior rail-nesting tests stay green.
- [x] 8.4 Verified against a COPY of live `~/.argus/data.sql`: rail renders 7 roots (was ~24), deeply nested — no data migration.
- [x] 8.5 `openspec validate --strict` + `make pre-pr` green.
