**Design doc:** `openspec/changes/add-hera-plan-view/design.md`

## 1. Tests (Prove-It)

- [ ] 1.1 Write failing tests for `db.ListHeraBlocks(orchID)` from the `data-persistence` scenarios (returns all edges deterministically; empty without error; excludes archived/nuked endpoints)
- [ ] 1.2 Write failing tests for the `heraPlanNodes` projection (planned + live roles → nodes; `hera_blocks` → edges; `RoleView.Planned` discriminator; degenerate no-plan → flat live stage)
- [ ] 1.3 Write failing `planview` widget tests for: short-id parse + fallback label; parallel-group collapse (contiguous + `[first–last +N]`); partial-dep `↘`; stage = computed longest-path (short-id number disagrees)
- [ ] 1.4 Write failing nav tests: `↑↓` stage change collapses a fanned group; `←→` slot move; `Enter`/`Space` fan-out/collapse; member walk + step-off-edge exit
- [ ] 1.5 Write failing tests for the master-detail header (node view vs group view content) and exact height budgeting
- [ ] 1.6 Write failing tests for sub-coordinator drill-in (Enter pushes child orch projection; Esc pops; plain leaf Enter still jumps to agent view; drillable marker present)
- [ ] 1.7 Confirm every `it should X` criterion in the deltas has a failing test before implementing

## 2. Data plumbing

**Depends on:** Stage 1

- [x] 2.1 Implement `db.ListHeraBlocks(orchID) ([]HeraBlock, error)` in `internal/db/hera_plan.go` (single query, deterministic order, archived/nuked-endpoint exclusion)
- [x] 2.2 Add `ListHeraBlocks` to the `HeraReader` interface (`internal/tui/hera/reader.go`); `*db.DB` satisfies it, remote nil-reader degrades unchanged
- [x] 2.3 Add `RoleView.Planned` (worker-kind, `!Live`, never-bound) and `OrchView.Blocks []db.HeraBlock`; populate in `BuildModel`/`buildRoleView` (one edge read per build)
- [x] 2.4 Write `heraPlanNodes(orch *OrchView) ([]planview.Node, []planview.Edge)` — pure in-memory projection over the built model (parallel to the retired `heraTreeNodes`)

## 3. planview widget — layout + render

**Depends on:** Stage 1

- [x] 3.1 Create `internal/tui/planview` with `Node`/`Edge` types and a `Widget` (bordered panel, branch-change contract, theme palette), importing `dagview`'s layer-assignment math for stage placement
- [x] 3.2 Short-id parse (`name` prefix → stage digits + member letters) with truncated-name fallback
- [x] 3.3 Parallel-group detection (same stage, shared blocker set, no internal edges) + collapse to `[first–last]` / `[first–last +N]` with aggregate state counts
- [x] 3.4 Render: chips with state glyph/colour (planned violet `○`, live by task status, failed red `✕`), single-line edges, partial-dep `↘` on box and feeding member
- [x] 3.5 Degenerate no-plan render: live roles as one flat edgeless stage with a "no plan" hint

## 4. planview widget — navigation

**Depends on:** Stage 3

- [x] 4.1 Cursor over `(stage, slot, member)`; `↑↓` stage (collapse fanned group), `←→` slot move
- [x] 4.2 `Enter`/`Space` fan-out/collapse on a group; inside a group `←→` member walk; step-off-edge exits+collapses to the adjacent slot (or clamps)
- [x] 4.3 Branch-change signature folds in stage/slot/member cursor + fanned group + current orchestrator (ghost-prevention, log-only, no `Sync`)

## 5. planview widget — master-detail header

**Depends on:** Stage 3

- [x] 5.1 Fixed-height header region above the diagram; exact height budgeting (mirror `DetailsView.ContentHeight` discipline)
- [x] 5.2 Node view (name / description = prompt first line / feeds) and group view (range·title / members / downstream)

## 6. planview widget — sub-coordinator drill-in

**Depends on:** Stage 3, Stage 4

- [x] 6.1 Detect a sub-coordinator node via the rail bridge (`CoordBridgeTaskID`/`bridgeIndex`); render the drillable marker
- [x] 6.2 Orchestrator nav stack: `Enter` on a sub-coord pushes + re-projects the child plan DAG; `Esc` pops; header title reflects the current orch
- [x] 6.3 Plain leaf `Enter` keeps the `OnEnter` jump-to-agent-view behaviour (disjoint by cursor target type)

## 7. Wire into the Hera view + retire the tree + docs

**Depends on:** Stage 2, Stage 4, Stage 5, Stage 6

- [ ] 7.1 Re-point `internal/tui/hera/page.go` (`rebuildDAG`/`drawDetailsRegion`/`handleDetailsKey`) at the plan widget fed by `heraPlanNodes`; retitle `" Plan "`; roster stacks over the plan graph (same geometry as roster-over-tree)
- [ ] 7.2 Retire `internal/tui/hera/tree.go` + `tree_test.go` (and `dag_test.go` references to `heraTreeNodes`); keep `dagview` as the layout library
- [ ] 7.3 Update the help modal (`internal/tui/modal/help.go`) + assert the new actions in `help_test.go` (`Enter`/`Space` fan-out + drill-in, `Esc` drill-out); mirror into the README Reference keybinding table
- [ ] 7.4 uxlog the projection build (node/edge counts), drill-in/out, and the degenerate no-plan branch (consistent `[hera]`/`[planview]` prefix)
- [ ] 7.5 Update `context/knowledge/gotchas/dag-rendering.md` + `gotchas/hera-view.md` for the plan-view projection, grouping, partial-dep, and drill-in
- [ ] 7.6 `PATH="$HOME/go/bin:$PATH" GIT_CONFIG_GLOBAL=/dev/null make pre-pr` green (vuln stdlib-only OK; run `make test-cover-gate` separately if vuln short-circuits)
