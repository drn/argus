package hera

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func twoOrchModel() Model {
	return Model{
		Active: []OrchView{
			{ID: 1, Name: "orch-1", Roles: []RoleView{
				{RoleID: 11, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t11"},
				{RoleID: 12, OrchID: 1, Name: "wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "t12"},
			}},
			{ID: 2, Name: "orch-2", Roles: []RoleView{
				{RoleID: 21, OrchID: 2, Name: "coord2", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t21"},
			}},
		},
	}
}

func TestRail_BuildRowsAndCursorNav(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())

	// The coordinator folds into its orchestrator header, so orch-1 renders a
	// header + its single worker, and orch-2 (coordinator-only) is header-only.
	// With the "Active (2)" group header (add-kanban-focus-fold): rule(0),
	// header(1), orch-1(2), wkr(3), orch-2(4) = 5 rows; 3 of them selectable.
	testutil.Equal(t, r.Rows(), 5)

	// Cursor starts at 2 (first selectable row — the header itself never is).
	testutil.Equal(t, r.CursorIndex(), 2)
	testutil.Equal(t, r.SelectedOrch().Name, "orch-1")

	r.CursorDown() // → worker wkr (coord folded into the header)
	testutil.Equal(t, r.Selected().Name, "wkr")
	r.CursorDown() // → orch-2 header (coordinator-only)
	testutil.Equal(t, r.SelectedOrch().Name, "orch-2")
	testutil.Nil(t, r.Selected()) // header carries no role
	r.CursorDown()                // at bottom, no move
	testutil.Equal(t, r.SelectedOrch().Name, "orch-2")

	r.CursorUp()
	testutil.Equal(t, r.Selected().Name, "wkr")
}

func TestRail_ToggleCollapseHidesRoles(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.Rows(), 5)

	// Cursor on orch-1 header; collapse it → its worker row vanishes.
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 4) // rule + "Active (2)" header + orch-1 (collapsed) + orch-2

	// Expand again.
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 5)
}

func TestRail_SelectionChangedFires(t *testing.T) {
	r := NewRail()
	fired := 0
	r.SetOnSelectionChanged(func() { fired++ })
	r.SetModel(twoOrchModel())
	r.CursorDown()
	r.CursorDown()
	testutil.Equal(t, fired, 2)
}

func TestRail_CoordinatorFoldsIntoHeader(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())

	// No row is the coordinator role itself — the only child row is the worker.
	for i := 0; i < r.Rows(); i++ {
		row := r.rows[i]
		if row.role != nil {
			testutil.Equal(t, row.role.Kind == db.HeraKindCoordinator, false)
		}
	}
	// orch-1 expanded shows exactly its worker (no coord child row); + rule +
	// "Active (2)" header (add-kanban-focus-fold).
	testutil.Equal(t, r.Rows(), 5)

	// SubtreeAgentCount excludes the folded coordinator: orch-1 has 1 worker.
	testutil.Equal(t, r.model.SubtreeAgentCount(1), 1)
}

func TestRail_OrchHeaderCarriesCoordinatorGlyph(t *testing.T) {
	// The header status glyph reflects the coordinator's status. Use blocked
	// (a static glyph) rather than working (an animated spinner whose frame
	// depends on wall-clock time) so the assertion is deterministic.
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 10)

	r := NewRail()
	r.SetFocused(true)
	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "orch", Roles: []RoleView{
		{RoleID: 11, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, HasStatus: true, Status: db.HeraStatusBlocked, TaskID: "tc"},
	}}}})
	r.SetRect(0, 0, 40, 10)
	r.Draw(sim)
	sim.Show()

	// The coordinator's blocked glyph must appear somewhere on the header row.
	// Row 3 = the orch header (row 1 = the leading rule, row 2 = the "Active
	// (1)" group header — add-kanban-focus-fold).
	wantGlyph, _ := statusIcon(&RoleView{HasStatus: true, Status: db.HeraStatusBlocked, Live: true}, false, 0)
	found := false
	for x := 0; x < 40; x++ {
		s, _, _ := sim.Get(x, 3)
		if s == string(wantGlyph) {
			found = true
			break
		}
	}
	testutil.Equal(t, found, true)
}

// depthOf returns the depth of the first row whose role/orch name matches, or -1.
func (r *Rail) depthOf(name string) int {
	for _, row := range r.rows {
		switch {
		case row.role != nil && row.role.Name == name:
			return row.depth
		case row.orch != nil && row.orch.Name == name:
			return row.depth
		}
	}
	return -1
}

func (r *Rail) hasOrchHeader(name string) bool {
	for _, row := range r.rows {
		if row.kind == rrOrch && row.orch != nil && row.orch.Name == name {
			return true
		}
	}
	return false
}

func TestRail_NestsSubOrchestratorUnderBridgingWorker(t *testing.T) {
	// Root R has worker w bound to tc; child C's coordinator is also tc, so C
	// nests under w. C's own worker wc renders one level deeper still.
	root := orchView(1, "R", "tr", wk("w", "tc"))
	child := orchView(2, "C", "tc", wk("wc", "twc"))
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{root, child}})

	// rule(0), "Active (1)" header(1), R(2), w(3, bridges C), wc(4). C never
	// renders as a top-level header.
	testutil.Equal(t, r.Rows(), 5)
	testutil.Equal(t, r.depthOf("R"), 0)
	testutil.Equal(t, r.depthOf("w"), 1)
	testutil.Equal(t, r.depthOf("wc"), 2)
	testutil.Equal(t, r.hasOrchHeader("C"), false) // consumed → nested, not a root

	// The bridging worker row keeps its PARENT context (conservative selection):
	// it is the worker role (not a coordinator), so mutations act on the worker,
	// not the child orchestrator. Nesting is purely visual.
	r.cursor = 3
	sel := r.Selection()
	testutil.Equal(t, sel.Role != nil, true)
	testutil.Equal(t, sel.Role.Name, "w")
	testutil.Equal(t, sel.IsCoordinator(), false)
}

func TestRail_NestingCollapseFoldsChildSubtree(t *testing.T) {
	root := orchView(1, "R", "tr", wk("w", "tc"))
	child := orchView(2, "C", "tc", wk("wc", "twc"))
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{root, child}})
	testutil.Equal(t, r.Rows(), 5)

	// Cursor on the bridging worker row → Space folds the child subtree (wc hides).
	r.cursor = 3
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 4) // rule + header + R header + w (child's wc folded away)
	testutil.Equal(t, r.depthOf("wc"), -1)
}

func TestRail_NestingCycleTerminatesAndPlacesOnce(t *testing.T) {
	// A↔B mutually bridge (A's worker→B's coord task, B's worker→A's coord task).
	// Both are consumed; the safety sweep + placed guard must render each once
	// without hanging.
	a := orchView(1, "A", "ta", wk("wa", "tb"))
	b := orchView(2, "B", "tb", wk("wb", "ta"))
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{a, b}})

	// A as root, wa bridges B (nested), wb nested under B; B's wb bridges A but A
	// is already placed → no further nesting. 1 header + 2 worker rows, + rule +
	// "Active (1)" group header (add-kanban-focus-fold).
	testutil.Equal(t, r.Rows(), 5)
	headers := 0
	for _, row := range r.rows {
		if row.kind == rrOrch {
			headers++
		}
	}
	testutil.Equal(t, headers, 1) // each orchestrator placed once
}

func TestRail_NestsCoordinatorSpawnedSubteam(t *testing.T) {
	// Coordinator task T coordinates BOTH P (coord role 100) and Q (coord role
	// 200) — the coordinator-spawned sub-team shape. Q has no worker row in P to
	// bridge it (the parent's coordinator IS the bridge), so it must nest as its
	// own sub-orchestrator header directly under P. This is the real under-nesting
	// bug: before the fix Q renders as a second top-level root.
	p := coordOf(1, "P", 100, "T",
		RoleView{RoleID: 101, Name: "pw", Kind: db.HeraKindWorker, Live: true, TaskID: "tpw", BridgeTaskID: "tpw"})
	q := coordOf(2, "Q", 200, "T",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tqw"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, q}})

	// rule(0), "Active (1)" header(1), P header(2), pw worker(3), Q nested
	// header(4), qw worker(5).
	testutil.Equal(t, r.Rows(), 6)
	testutil.Equal(t, r.depthOf("P"), 0)
	testutil.Equal(t, r.depthOf("pw"), 1)
	testutil.Equal(t, r.depthOf("Q"), 1) // nested under P, NOT a top-level root
	testutil.Equal(t, r.depthOf("qw"), 2)
	testutil.Equal(t, r.hasOrchHeader("Q"), true) // renders as a collapsible sub-orch header

	// Exactly one depth-0 root header (P); Q is nested.
	roots := 0
	for _, row := range r.rows {
		if row.kind == rrOrch && row.depth == 0 {
			roots++
		}
	}
	testutil.Equal(t, roots, 1)
}

// TestRail_SelectionTopLevelOrch pins add-hera-kanban-status's m/M gating:
// Selection.TopLevelOrch is true ONLY for a root orchestrator header (no
// canonical parent), false for a nested sub-orchestrator header reached only
// through one, and false for a plain role row.
func TestRail_SelectionTopLevelOrch(t *testing.T) {
	p := coordOf(1, "P", 100, "T",
		RoleView{RoleID: 101, Name: "pw", Kind: db.HeraKindWorker, Live: true, TaskID: "tpw", BridgeTaskID: "tpw"})
	q := coordOf(2, "Q", 200, "T",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tqw"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, q}})

	// rule(0), "Active (1)" header(1), P header(2): root, no canonical parent.
	r.cursor = 2
	sel := r.Selection()
	testutil.Equal(t, sel.Role == nil, true)
	testutil.Equal(t, sel.Orch.Name, "P")
	testutil.Equal(t, sel.TopLevelOrch, true)
	testutil.Equal(t, sel.KanbanTarget() != nil, true)

	// pw worker row(3): a role selection, never a kanban target regardless of
	// TopLevelOrch (which still reflects P, the role's containing orchestrator).
	r.cursor = 3
	sel = r.Selection()
	testutil.Equal(t, sel.Role.Name, "pw")
	testutil.Nil(t, sel.KanbanTarget())

	// Q's nested header(1 depth, row 2): canonical parent is P, so NOT top-level.
	for r.rows[r.cursor].orch == nil || r.rows[r.cursor].orch.Name != "Q" {
		r.CursorDown()
	}
	sel = r.Selection()
	testutil.Equal(t, sel.Role == nil, true)
	testutil.Equal(t, sel.Orch.Name, "Q")
	testutil.Equal(t, sel.TopLevelOrch, false)
	testutil.Nil(t, sel.KanbanTarget())
}

func TestRail_CoordSpawnedSubteamCollapses(t *testing.T) {
	p := coordOf(1, "P", 100, "T")
	q := coordOf(2, "Q", 200, "T",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tqw"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, q}})
	// rule(0), "Active (1)" header(1), P header(2), Q nested header(3), qw(4).
	testutil.Equal(t, r.Rows(), 5)

	// Move the cursor onto the nested Q header and fold it → qw hides.
	for r.rows[r.cursor].orch == nil || r.rows[r.cursor].orch.Name != "Q" {
		r.CursorDown()
	}
	r.ToggleCollapse()
	testutil.Equal(t, r.depthOf("qw"), -1)
}

// rootHeaderCount counts depth-0 orchestrator headers (top-level roots).
func rootHeaderCount(r *Rail) int {
	n := 0
	for _, row := range r.rows {
		if row.kind == rrOrch && row.depth == 0 {
			n++
		}
	}
	return n
}

// TestRail_CollapsedParentDoesNotLeakCoordChildToTop is the regression for the
// rail under-nesting bug: collapsing a PARENT orchestrator must FOLD its
// coordinator-spawned child away, NOT leak the child to a depth-0 root. Before
// the structuralReach guard, the child — left unplaced because its parent was
// collapsed — fell through the safety sweep (loop 2 keyed only on `!placed`) and
// was re-placed at depth 0. It reproduces ONLY with the parent collapsed, which
// is why expanded-fold renders looked correct and masked the bug for sessions.
func TestRail_CollapsedParentDoesNotLeakCoordChildToTop(t *testing.T) {
	p := coordOf(1, "P", 100, "T")
	q := coordOf(2, "Q", 200, "T",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tqw"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, q}})

	// Expanded: Q nests under P (depth 1); exactly one depth-0 root.
	testutil.Equal(t, r.depthOf("Q"), 1)
	testutil.Equal(t, rootHeaderCount(r), 1)

	// Collapse the PARENT P.
	for r.rows[r.cursor].orch == nil || r.rows[r.cursor].orch.Name != "P" {
		r.CursorDown()
	}
	r.ToggleCollapse()

	// Q must stay folded away — not leaked to a depth-0 root.
	testutil.Equal(t, r.depthOf("Q"), -1)
	testutil.Equal(t, rootHeaderCount(r), 1) // only P
}

// TestRail_CollapsedParentDoesNotLeakWorkerBridgedChild covers the same leak via
// the worker→coordinator bridge shape: collapsing the parent root must fold the
// bridged child's subtree, not surface the child as a new depth-0 header.
func TestRail_CollapsedParentDoesNotLeakWorkerBridgedChild(t *testing.T) {
	root := orchView(1, "R", "tr", wk("w", "tc"))
	child := orchView(2, "C", "tc", wk("wc", "twc"))
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{root, child}})
	testutil.Equal(t, r.hasOrchHeader("C"), false) // nested under w when expanded

	// Collapse the parent root R.
	for r.rows[r.cursor].orch == nil || r.rows[r.cursor].orch.Name != "R" {
		r.CursorDown()
	}
	r.ToggleCollapse()

	// The child subtree folds; C must NOT leak to a top-level header.
	testutil.Equal(t, r.depthOf("w"), -1)
	testutil.Equal(t, r.hasOrchHeader("C"), false)
	testutil.Equal(t, rootHeaderCount(r), 1) // only R
}

// TestRail_CollapsedGrandparentFoldsWholeSubtree covers multi-level nesting
// (rail-debug review edge #2): collapsing a GRANDPARENT must fold the ENTIRE
// subtree — neither the child nor the grandchild may leak to the top. P
// coord-spawns Q; Q's worker bridges R (mixing both nesting paths).
func TestRail_CollapsedGrandparentFoldsWholeSubtree(t *testing.T) {
	p := coordOf(1, "P", 100, "T")
	q := coordOf(2, "Q", 200, "T",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tr"})
	rr := coordOf(3, "R", 300, "tr",
		RoleView{RoleID: 301, Name: "rw", Kind: db.HeraKindWorker, Live: true, TaskID: "trw", BridgeTaskID: "trw"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, q, rr}})

	// Expanded: P(0) → Q(1, coord-spawned) → qw(2, bridges R) → rw(3). One root.
	testutil.Equal(t, r.depthOf("Q"), 1)
	testutil.Equal(t, r.depthOf("rw"), 3)
	testutil.Equal(t, rootHeaderCount(r), 1)

	// Collapse the grandparent P.
	for r.rows[r.cursor].orch == nil || r.rows[r.cursor].orch.Name != "P" {
		r.CursorDown()
	}
	r.ToggleCollapse()

	// Entire subtree folds — neither Q, qw, R, nor rw leaks to the top.
	testutil.Equal(t, r.depthOf("Q"), -1)
	testutil.Equal(t, r.depthOf("rw"), -1)
	testutil.Equal(t, r.hasOrchHeader("R"), false)
	testutil.Equal(t, rootHeaderCount(r), 1) // only P
}

// TestRail_EnsureAncestorsExpandedRevealsNestedLeaf is BUG-007: a worker leaf
// buried under collapsed ANCESTOR coordinators is invisible to SelectByTaskID
// (which scans only the currently-built rows), so the plan view's leaf-Enter join
// silently no-ops. EnsureAncestorsExpanded must uncollapse the WHOLE canonical
// ancestor chain (multi-level, not just the immediate parent) so the row builds
// and the join lands. Reuses the grandparent fold shape: P → Q (coord-spawned) →
// qw (bridges R) → R → rw; collapsing the top grandparent P folds the whole chain.
func TestRail_EnsureAncestorsExpandedRevealsNestedLeaf(t *testing.T) {
	p := coordOf(1, "P", 100, "T")
	q := coordOf(2, "Q", 200, "T",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tr"})
	rr := coordOf(3, "R", 300, "tr",
		RoleView{RoleID: 301, Name: "rw", Kind: db.HeraKindWorker, Live: true, TaskID: "trw", BridgeTaskID: "trw"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, q, rr}})

	// Collapse the top grandparent P → the whole subtree (Q, qw, R, rw) folds.
	r.seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.Name == "P" })
	r.ToggleCollapse()
	testutil.Equal(t, r.depthOf("rw"), -1)
	// Pre-expand the leaf's row is not built, so a join attempt no-ops.
	testutil.Equal(t, r.SelectByTaskID("trw"), false)

	// Resolve the leaf's containing orchestrator from the FULL model (fold-
	// independent) and expand its ancestor chain.
	orchIDs := r.Model().OrchIDsForTask("trw")
	testutil.DeepEqual(t, orchIDs, []int64{3})
	r.EnsureAncestorsExpanded(orchIDs[0])

	// Every ancestor on the chain (R, Q, P) is now expanded and persisted like a
	// user toggle, the leaf row is built across all levels, and the join lands.
	testutil.Equal(t, r.OrchCollapsed(1), false)
	testutil.Equal(t, r.OrchCollapsed(2), false)
	testutil.Equal(t, r.OrchCollapsed(3), false)
	testutil.Equal(t, r.depthOf("rw") >= 0, true)
	testutil.Equal(t, r.SelectByTaskID("trw"), true)
	testutil.Equal(t, r.Selected().TaskID, "trw")
}

// TestRail_EnsureAncestorsExpandedNoOpWhenVisible: expanding when nothing is
// folded leaves the rows untouched (no spurious rebuild) and still selectable.
func TestRail_EnsureAncestorsExpandedNoOpWhenVisible(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())
	// twoOrchModel seeds first-run-collapsed; expand orch-1 so its worker is shown.
	r.EnsureAncestorsExpanded(1)
	testutil.Equal(t, r.OrchCollapsed(1), false)
	testutil.Equal(t, r.SelectByTaskID("t12"), true)
	rowsBefore := r.Rows()
	r.EnsureAncestorsExpanded(1) // already expanded → no change
	testutil.Equal(t, r.Rows(), rowsBefore)
	// A zero id is a guarded no-op.
	r.EnsureAncestorsExpanded(0)
	testutil.Equal(t, r.Rows(), rowsBefore)
}

// TestModel_OrchIDsForTask covers the fold-independent task→orchestrator resolver
// the leaf-Enter join uses, including the multi-binding fan-out (same task under
// two orchestrators returns both ids) and the empty-input guard.
func TestModel_OrchIDsForTask(t *testing.T) {
	m := Model{Active: []OrchView{
		coordOf(1, "A", 10, "ta", RoleView{RoleID: 11, Name: "w", Kind: db.HeraKindWorker, Live: true, TaskID: "shared"}),
		coordOf(2, "B", 20, "tb", RoleView{RoleID: 21, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "shared"}),
	}}
	testutil.DeepEqual(t, m.OrchIDsForTask("shared"), []int64{1, 2})
	testutil.DeepEqual(t, m.OrchIDsForTask("ta"), []int64{1})
	testutil.Nil(t, m.OrchIDsForTask(""))
	testutil.Nil(t, m.OrchIDsForTask("missing"))
}

// seekCursor parks the cursor on the first row matching pred (for fold tests).
func (r *Rail) seekCursor(t *testing.T, pred func(railRow) bool) {
	t.Helper()
	for i := range r.rows {
		if pred(r.rows[i]) {
			r.cursor = i
			return
		}
	}
	t.Fatalf("no row matched the predicate")
}

// collOrchIDForRole returns the collOrchID of the first role row with the given
// name, or 0 (so a test can prove which worker row actually hosts a bridged child).
func (r *Rail) collOrchIDForRole(name string) int64 {
	for _, row := range r.rows {
		if row.role != nil && row.role.Name == name {
			return row.collOrchID
		}
	}
	return 0
}

// TestRail_MultiParentChildNestsDeterministically is the multi-parent fold-
// migration regression (the live native-hera-parity quirk). One coordinator task T
// coordinates BOTH update-argus (coord role 20) and native-hera-parity (coord role
// 30) AND is the rail-debug worker under hera-rail — so native-hera-parity is
// reachable via a coordinator-spawn parent (update-argus) AND, transitively, the
// worker-bridge under hera-rail. Before the canonical-parent fix the child rendered
// under whichever path the build reached first, so folding one parent relocated the
// whole subtree to the other. The canonical rule (prefer coordinator-spawn; the
// worker-bridge resolves to the shared-coordinator clique root) pins ONE structure:
// hera-rail → rail-debug → update-argus → native-hera-parity, fold-independent.
func TestRail_MultiParentChildNestsDeterministically(t *testing.T) {
	heraRail := coordOf(1, "hera-rail", 10, "t-hr",
		RoleView{RoleID: 11, Name: "rail-debug", Kind: db.HeraKindWorker, Live: true, TaskID: "T", BridgeTaskID: "T"})
	updateArgus := coordOf(2, "update-argus", 20, "T",
		RoleView{RoleID: 21, Name: "ua-wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "t-ua", BridgeTaskID: "t-ua"})
	nativeParity := coordOf(3, "native-hera-parity", 30, "T",
		RoleView{RoleID: 31, Name: "np-wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "t-np", BridgeTaskID: "t-np"})
	// native-hera-parity precedes update-argus in the slice so the OLD first-wins
	// bridge index would point the rail-debug worker straight at native-hera-parity
	// (the migration trigger); the canonical rule must ignore slice order.
	m := Model{Active: []OrchView{heraRail, nativeParity, updateArgus}}

	r := NewRail()
	r.SetModel(m)

	// (1) Expanded: native-hera-parity nests under its CANONICAL coordinator-spawn
	// parent update-argus, which itself worker-bridges under hera-rail's rail-debug
	// row. Exactly one root; update-argus has no header (it IS the rail-debug row).
	testutil.Equal(t, rootHeaderCount(r), 1)
	testutil.Equal(t, r.hasOrchHeader("hera-rail"), true)
	testutil.Equal(t, r.hasOrchHeader("update-argus"), false) // worker-bridged → no header
	testutil.Equal(t, r.hasOrchHeader("native-hera-parity"), true)
	testutil.Equal(t, r.depthOf("native-hera-parity"), 2)
	testutil.Equal(t, r.depthOf("np-wkr"), 3)
	// The rail-debug worker row carries update-argus as its bridged child.
	testutil.Equal(t, r.collOrchIDForRole("rail-debug"), updateArgus.ID)

	// (2) Collapse the CANONICAL parent (update-argus, folded on the rail-debug
	// row): the child folds away and does NOT reappear as a root or elsewhere.
	r.seekCursor(t, func(row railRow) bool { return row.collOrchID == updateArgus.ID && row.role != nil })
	r.ToggleCollapse()
	testutil.Equal(t, r.depthOf("native-hera-parity"), -1)
	testutil.Equal(t, r.depthOf("np-wkr"), -1)
	testutil.Equal(t, rootHeaderCount(r), 1) // still only hera-rail, no leak

	// Re-expand and confirm it returns to the same place (not a new parent).
	r.ToggleCollapse()
	testutil.Equal(t, r.depthOf("native-hera-parity"), 2)
	testutil.Equal(t, rootHeaderCount(r), 1)

	// (3) Collapse the worker-bridge ancestor (hera-rail): the whole chain folds;
	// native-hera-parity must NOT leak to a top-level root.
	r.seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.Name == "hera-rail" })
	r.ToggleCollapse()
	testutil.Equal(t, r.depthOf("native-hera-parity"), -1)
	testutil.Equal(t, r.hasOrchHeader("native-hera-parity"), false)
	testutil.Equal(t, rootHeaderCount(r), 1) // only hera-rail
}

// TestRail_MultiWorkerBridgeParentsPickLowestOrchID covers the sibling form of the
// multi-parent quirk: a child whose coordinator task is bridged by a worker in TWO
// separate root orchestrators. The canonical rule picks the lowest orchestrator id
// deterministically (not the slice-order-first one), so collapsing the OTHER parent
// never moves the child.
func TestRail_MultiWorkerBridgeParentsPickLowestOrchID(t *testing.T) {
	c := orchView(2, "C", "tc", wk("cw", "tcw"))
	pw1 := coordOf(5, "Pw1", 50, "t-pw1",
		RoleView{RoleID: 51, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tc", BridgeTaskID: "tc"})
	pw2 := coordOf(3, "Pw2", 30, "t-pw2",
		RoleView{RoleID: 31, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tc", BridgeTaskID: "tc"})
	// Pw1 (id 5) precedes Pw2 (id 3): the OLD first-reached-wins logic nested C
	// under Pw1; the canonical rule must pick Pw2 (lowest orchestrator id).
	build := func() *Rail {
		r := NewRail()
		r.SetModel(Model{Active: []OrchView{pw1, pw2, c}})
		return r
	}

	// (1) Expanded: C's worker nests under Pw2's worker (lowest id), not Pw1's.
	r := build()
	testutil.Equal(t, rootHeaderCount(r), 2)               // Pw1 + Pw2 are both roots
	testutil.Equal(t, r.collOrchIDForRole("w2"), c.ID)     // Pw2's worker hosts C
	testutil.Equal(t, r.collOrchIDForRole("w1"), int64(0)) // Pw1's worker hosts nothing
	testutil.Equal(t, r.depthOf("cw"), 2)

	// (2) Collapse the CANONICAL parent Pw2 → C folds away (and does not migrate
	// to the non-canonical Pw1).
	r.seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.Name == "Pw2" })
	r.ToggleCollapse()
	testutil.Equal(t, r.depthOf("cw"), -1)
	testutil.Equal(t, r.collOrchIDForRole("w1"), int64(0)) // still not under Pw1

	// (3) On a fresh rail, collapse the NON-canonical parent Pw1 → C stays put
	// under Pw2, unaffected.
	r2 := build()
	r2.seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.Name == "Pw1" })
	r2.ToggleCollapse()
	testutil.Equal(t, r2.depthOf("cw"), 2)              // unchanged
	testutil.Equal(t, r2.collOrchIDForRole("w2"), c.ID) // still under Pw2
}

// TestRail_CollapsedParentDoesNotLeakArchivedBridgedChild is the exact live
// repro (rail-debug review): a child bridged by an ARCHIVED (finished) worker —
// which renders in place rather than folding into the Archive expando — must
// still stay folded when its parent is collapsed, not leak to the top (the
// 0e-team-under-collapsed-sherlock-mvp shape).
func TestRail_CollapsedParentDoesNotLeakArchivedBridgedChild(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		RoleView{RoleID: 101, Name: "aw", Kind: db.HeraKindWorker, Archived: true, Live: false,
			BridgeTaskID: "tc", LinkEndReason: ""}) // ended, non-teardown → still bridges
	c := coordOf(2, "C", 200, "tc",
		RoleView{RoleID: 201, Name: "cw", Kind: db.HeraKindWorker, Live: true, TaskID: "tcw", BridgeTaskID: "tcw"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, c}})

	// BUG-022 Q3: the HIDDEN (archived) bridging worker folds into P's Archive
	// expando (collapsed by default), dragging C's subtree in with it — so by
	// default both are hidden, and C never leaks to a top-level root.
	testutil.Equal(t, r.depthOf("aw"), -1)
	testutil.Equal(t, r.depthOf("cw"), -1)
	testutil.Equal(t, r.hasOrchHeader("C"), false)
	testutil.Equal(t, rootHeaderCount(r), 1) // only P

	// Open P's Archive expando → aw nests under it and C's worker nests beneath aw,
	// one level deeper (structure retained inside the expando).
	for r.rows[r.cursor].archiveOwner != 1 {
		r.CursorDown()
	}
	r.ToggleCollapse()
	awDepth := r.depthOf("aw")
	testutil.Equal(t, awDepth > 0, true)
	testutil.Equal(t, r.depthOf("cw"), awDepth+1)
	testutil.Equal(t, r.hasOrchHeader("C"), false) // still nested, never a top-level root
	testutil.Equal(t, rootHeaderCount(r), 1)
}

// TestRail_HiddenSubCoordCollapsesSubtreeIntoExpando is the explicit BUG-022 Q3
// ≥2-level case: parent P → hidden sub-coord B (a bridging worker) → agent C.
// Hiding B collapses B AND C into P's Archive expando — C renders nested beneath
// B inside the expando when open, is hidden when the expando is collapsed, and is
// NEVER hoisted to a top-level root in either fold state.
func TestRail_HiddenSubCoordCollapsesSubtreeIntoExpando(t *testing.T) {
	// P (root) has a coordinator + an ARCHIVED worker B bridging child orch (B is a
	// hidden sub-coordinator). The child orch's coordinator task is B's bridge task,
	// and the child holds agent C.
	p := coordOf(1, "P", 100, "tp",
		RoleView{RoleID: 101, Name: "B", Kind: db.HeraKindWorker, Archived: true, Live: true,
			TaskID: "tb", BridgeTaskID: "tb"})
	child := coordOf(2, "B", 200, "tb",
		RoleView{RoleID: 201, Name: "C", Kind: db.HeraKindWorker, Live: true, TaskID: "tc", BridgeTaskID: "tc"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, child}})

	// Collapsed-by-default expando: B and C are both hidden; C is NOT a top-level root.
	testutil.Equal(t, r.depthOf("B"), -1)
	testutil.Equal(t, r.depthOf("C"), -1)
	testutil.Equal(t, rootHeaderCount(r), 1) // only P
	expandoFound := false
	for i := range r.rows {
		if r.rows[i].kind == rrArchiveExpando && r.rows[i].archiveOwner == 1 {
			expandoFound = true
		}
	}
	testutil.Equal(t, expandoFound, true)

	// Open the expando → B nests under it and C nests one level deeper, both dimmed.
	for r.rows[r.cursor].archiveOwner != 1 {
		r.CursorDown()
	}
	r.ToggleCollapse()
	bDepth := r.depthOf("B")
	testutil.Equal(t, bDepth > 0, true)
	testutil.Equal(t, r.depthOf("C"), bDepth+1) // C nested beneath B inside the expando
	testutil.Equal(t, rootHeaderCount(r), 1)    // C never hoisted to a top-level root
	for i := range r.rows {
		if r.rows[i].role != nil && (r.rows[i].role.Name == "B" || r.rows[i].role.Name == "C") {
			testutil.Equal(t, r.rows[i].dim, true) // whole hidden subtree dims
		}
	}
}

func TestRail_LargeShapeSixRootsManyNested(t *testing.T) {
	// Mirror the real rail shape: 6 roots, each with 3 worker-bridged children
	// and 1 coordinator-spawned sub-team = 24 nested orchestrators. The bug made
	// all 30 render as top-level roots; after the fix exactly 6 are roots.
	var active []OrchView
	var roleID int64 = 1000
	nextRole := func() int64 { roleID++; return roleID }
	var orchID int64
	nextOrch := func() int64 { orchID++; return orchID }

	for k := 0; k < 6; k++ {
		rootCoordTask := fmt.Sprintf("rootcoord-%d", k)
		rootCoordID := nextRole()
		root := OrchView{ID: nextOrch(), Name: fmt.Sprintf("root-%d", k), Roles: []RoleView{
			{RoleID: rootCoordID, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: rootCoordTask, BridgeTaskID: rootCoordTask},
		}}
		for c := 0; c < 3; c++ {
			childTask := fmt.Sprintf("wkrchild-%d-%d", k, c)
			root.Roles = append(root.Roles, RoleView{
				RoleID: nextRole(), Name: fmt.Sprintf("w-%d-%d", k, c), Kind: db.HeraKindWorker,
				Live: true, TaskID: childTask, BridgeTaskID: childTask,
			})
			active = append(active, OrchView{ID: nextOrch(), Name: childTask, Roles: []RoleView{
				{RoleID: nextRole(), Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: childTask, BridgeTaskID: childTask},
			}})
		}
		// Coordinator-spawned sub-team: the SAME root coordinator task, later id.
		active = append(active, OrchView{ID: nextOrch(), Name: fmt.Sprintf("coordchild-%d", k), Roles: []RoleView{
			{RoleID: nextRole(), Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: rootCoordTask, BridgeTaskID: rootCoordTask},
		}})
		active = append(active, root)
	}
	m := Model{Active: active}

	// 6 roots, 24 nested.
	consumed := m.consumedSet(m.bridgeIndex())
	testutil.Equal(t, len(consumed), 24)

	r := NewRail()
	r.SetModel(m)
	roots := 0
	for _, row := range r.rows {
		if row.kind == rrOrch && row.depth == 0 {
			roots++
		}
	}
	testutil.Equal(t, roots, 6)
}

func TestRail_ArchivedBridgingWorkerHoistsSubtreeToExpando(t *testing.T) {
	// BUG-022 Q3: Root R (active) has an ARCHIVED (HIDDEN) worker w bridging an
	// ACTIVE child C. Hiding w folds it into R's Archive expando and drags C's
	// subtree in with it; C is NEVER safety-swept to a top-level root. When the
	// expando is opened, w nests under it and C's worker nests one level deeper,
	// both dimmed (the whole hidden subtree is de-emphasized inside the expando).
	root := OrchView{ID: 1, Name: "R", Roles: []RoleView{
		{RoleID: 10, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tr", BridgeTaskID: "tr"},
		{RoleID: 11, Name: "w", Kind: db.HeraKindWorker, Live: true, Archived: true, TaskID: "tc", BridgeTaskID: "tc"},
	}}
	child := orchView(2, "C", "tc", wk("wc", "twc"))
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{root, child}})

	// Collapsed by default: w + wc hidden; C not a top-level root; an expando exists.
	testutil.Equal(t, r.depthOf("w"), -1)
	testutil.Equal(t, r.depthOf("wc"), -1)
	testutil.Equal(t, r.hasOrchHeader("C"), false)
	expandoFound := false
	for _, row := range r.rows {
		if row.kind == rrArchiveExpando && row.archiveOwner == 1 {
			expandoFound = true
		}
	}
	testutil.Equal(t, expandoFound, true)
	testutil.Equal(t, rootHeaderCount(r), 1) // only R is a root

	// Open the expando → w nests, C's worker nests one level deeper, both dimmed.
	for r.rows[r.cursor].archiveOwner != 1 {
		r.CursorDown()
	}
	r.ToggleCollapse()
	wDepth := r.depthOf("w")
	testutil.Equal(t, wDepth > 0, true)
	testutil.Equal(t, r.depthOf("wc"), wDepth+1)
	testutil.Equal(t, r.hasOrchHeader("C"), false)
	testutil.Equal(t, rootHeaderCount(r), 1)
	for _, row := range r.rows {
		if row.role != nil && (row.role.Name == "w" || row.role.Name == "wc") {
			testutil.Equal(t, row.dim, true) // hidden subtree dims inside the expando
		}
	}
}

func TestRail_ArchivedLeafWorkerStillHoistsToExpando(t *testing.T) {
	// An archived worker that bridges NOTHING is a finished leaf and still folds
	// into the per-coordinator Archive expando (the in-place rule is only for
	// archived workers that bridge a live child).
	o := OrchView{ID: 1, Name: "o", Roles: []RoleView{
		{RoleID: 11, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
		{RoleID: 13, Name: "old-leaf", Kind: db.HeraKindWorker, Archived: true, Live: true, TaskID: "t13", BridgeTaskID: "t13"},
	}}
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{o}})
	testutil.Equal(t, r.depthOf("old-leaf"), -1) // hidden under collapsed expando
	found := false
	for i := range r.rows {
		if r.rows[i].kind == rrArchiveExpando && r.rows[i].archiveOwner == 1 {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

func TestRail_ArchivedBridgeNestsDimmedInPlace(t *testing.T) {
	// Active root R bridges archived child C via worker w. C must nest dimmed
	// under w (not dropped, not hoisted to the bottom Archive section).
	root := orchView(1, "R", "tr", wk("w", "tc"))
	child := orchView(2, "C", "tc", wk("wc", "twc"))
	child.Archived = true
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{root}, Archived: []OrchView{child}})

	// wc nests under w (depth 2) and is dimmed; no bottom Archive expando (C is
	// placed via the bridge, so it is not an archived root).
	testutil.Equal(t, r.depthOf("wc"), 2)
	for _, row := range r.rows {
		if row.role != nil && row.role.Name == "wc" {
			testutil.Equal(t, row.dim, true) // archived placement dims the subtree
		}
		testutil.Equal(t, row.kind == rrArchiveExpando, false) // no bottom archive section
	}
}

func TestRail_PerCoordinatorArchiveExpando(t *testing.T) {
	o := OrchView{ID: 1, Name: "o", Roles: []RoleView{
		{RoleID: 11, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
		{RoleID: 12, Name: "active-wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "t12"},
		{RoleID: 13, Name: "old-wkr", Kind: db.HeraKindWorker, Archived: true, TaskID: "t13"},
	}}
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{o}})

	// rule(0) + "Active (1)" header(1) + o header(2) + active-wkr(3) +
	// per-coord "Archive (1)" expando(4); the archived role is hidden under
	// the collapsed-by-default expando.
	testutil.Equal(t, r.Rows(), 5)
	testutil.Equal(t, r.depthOf("old-wkr"), -1)
	var expando *railRow
	for i := range r.rows {
		if r.rows[i].kind == rrArchiveExpando && r.rows[i].archiveOwner == 1 {
			expando = &r.rows[i]
		}
	}
	testutil.Equal(t, expando != nil, true)
	testutil.Contains(t, expando.label, "Archive (1)")

	// Move cursor to the expando and expand it → the archived role appears dimmed.
	for r.rows[r.cursor].archiveOwner != 1 {
		r.CursorDown()
	}
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 6)
	for i := range r.rows {
		if r.rows[i].role != nil && r.rows[i].role.Name == "old-wkr" {
			testutil.Equal(t, r.rows[i].dim, true)
		}
	}
}

func TestModel_BridgeSubtree(t *testing.T) {
	// R → C → G chain plus an unrelated orchestrator. BridgeSubtree(C) returns
	// {C, G}; BridgeSubtree(R) returns {R, C, G}; an unknown id returns nil.
	r := orchView(1, "R", "tr", wk("w", "tc"))
	c := orchView(2, "C", "tc", wk("wc", "tg"))
	g := orchView(3, "G", "tg", wk("wg", "twg"))
	other := orchView(9, "Other", "to", wk("wo", "two"))
	m := Model{Active: []OrchView{r, c, g, other}}

	names := func(os []*OrchView) []string {
		out := make([]string, len(os))
		for i, o := range os {
			out[i] = o.Name
		}
		return out
	}
	testutil.DeepEqual(t, names(m.BridgeSubtree(2)), []string{"C", "G"})
	testutil.DeepEqual(t, names(m.BridgeSubtree(1)), []string{"R", "C", "G"})
	testutil.Nil(t, m.BridgeSubtree(999))
}

func TestModel_BridgeSubtreeIncludesCoordSpawnedChild(t *testing.T) {
	// P coordinates a worker-bridged child C and a coordinator-spawned sub-team S
	// (shared coord task T, later coord id). The Ctrl+D cascade must reach both.
	p := coordOf(1, "P", 100, "T",
		RoleView{RoleID: 101, Name: "w", Kind: db.HeraKindWorker, Live: true, TaskID: "tc", BridgeTaskID: "tc"})
	c := orchView(2, "C", "tc", wk("wc", "twc"))
	s := coordOf(3, "S", 300, "T")
	m := Model{Active: []OrchView{p, c, s}}
	names := func(os []*OrchView) []string {
		out := make([]string, len(os))
		for i, o := range os {
			out[i] = o.Name
		}
		return out
	}
	testutil.DeepEqual(t, names(m.BridgeSubtree(1)), []string{"P", "C", "S"})
}

func TestRail_SelectionCarriesBridgeChild(t *testing.T) {
	root := orchView(1, "R", "tr", wk("w", "tc"))
	child := orchView(2, "C", "tc", wk("wc", "twc"))
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{root, child}})

	// Cursor on the bridging worker row → Selection carries the child orch id so
	// Ctrl+D can cascade; a non-bridging row carries 0. Rows: rule(0), "Active
	// (1)" header(1), R(2), w(3), wc(4).
	r.cursor = 3 // the "w" bridging row
	testutil.Equal(t, r.Selection().BridgeChildOrchID, child.ID)
	r.cursor = 4 // "wc" leaf (no bridge)
	testutil.Equal(t, r.Selection().BridgeChildOrchID, int64(0))
}

func TestRail_PRIndicatorOnManagedRow(t *testing.T) {
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "o", Roles: []RoleView{
		{RoleID: 11, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
		{RoleID: 12, Name: "wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "twk"},
	}}}})

	// No PR cache → no indicator.
	testutil.Equal(t, r.rolePR(&r.model.Active[0].Roles[1]), false)

	// An actionable review state on the worker's task flags it; a task without
	// one does not.
	r.SetPRMeta(map[string]map[string]string{"twk": {"url": "https://example/pr/1", "state": "awaiting-review"}})
	testutil.Equal(t, r.rolePR(&r.model.Active[0].Roles[1]), true)
	testutil.Equal(t, r.rolePR(&RoleView{TaskID: "other"}), false)

	// Render must draw the "PR" tag somewhere on the worker row without panicking.
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 10)
	r.SetRect(0, 0, 40, 10)
	r.Draw(sim)
	sim.Show()
	found := false
	for y := 0; y < 10 && !found; y++ {
		for x := 0; x+1 < 40; x++ {
			a, _, _ := sim.Get(x, y)
			b, _, _ := sim.Get(x+1, y)
			if a == "P" && b == "R" {
				found = true
				break
			}
		}
	}
	testutil.Equal(t, found, true)
}

func TestRail_RolePR_PRStateTable(t *testing.T) {
	r := NewRail()
	role := &RoleView{TaskID: "twk"}
	cases := []struct {
		name  string
		state string
		want  bool
	}{
		{"awaiting-review", "awaiting-review", true},
		{"changes-requested", "changes-requested", true},
		{"approved", "approved", true},
		{"merged-closed", "merged-closed", false},
		{"draft", "draft", false},
		{"unknown", "unknown", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r.SetPRMeta(map[string]map[string]string{"twk": {"url": "https://example/pr/1", "state": tc.state}})
			testutil.Equal(t, r.rolePR(role), tc.want)
		})
	}
}

func TestRail_FreelanceSectionCollapses(t *testing.T) {
	r := NewRail()
	r.SetModel(Model{
		Active:    []OrchView{{ID: 1, Name: "o"}},
		Freelance: []RoleView{{RoleID: 91, Name: "free-a", Kind: db.HeraKindFreelance}},
	})
	// rule + "Active (1)" header + orch header + rule + freelance header + 1
	// freelance role = 6.
	testutil.Equal(t, r.Rows(), 6)

	// Move cursor to the freelance header and collapse it.
	r.CursorDown() // freelance header (rules/kanban header are non-selectable, skipped)
	testutil.Equal(t, r.rows[r.cursor].kind, rrSectionHeader)
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 5) // role hidden
}

func TestRail_ArchiveExpandoDefaultCollapsed(t *testing.T) {
	r := NewRail()
	r.SetModel(Model{
		Active:   []OrchView{{ID: 1, Name: "o"}},
		Archived: []OrchView{{ID: 9, Name: "old", Archived: true, Roles: []RoleView{{RoleID: 99, Name: "r"}}}},
	})
	// rule + "Active (1)" header + orch + rule + archive expando = 5 (archived
	// orch hidden by default).
	testutil.Equal(t, r.Rows(), 5)

	// Cursor onto the archive expando and expand it.
	r.CursorDown() // archive expando
	testutil.Equal(t, r.rows[r.cursor].kind, rrArchiveExpando)
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 7) // + archived orch header + its role
}

// pinnedPlusActiveModel: one pinned coordinator-only orchestrator and one active
// coordinator-only orchestrator. Each renders as a single header row, so the only
// thing that can sit between them is the BUG-027 Pinned→Active divider.
func pinnedPlusActiveModel() Model {
	return Model{
		Pinned: []OrchView{{ID: 1, Name: "pinned-orch", Pinned: true, Roles: []RoleView{
			{RoleID: 11, OrchID: 1, Name: "pcoord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tp"},
		}}},
		Active: []OrchView{{ID: 2, Name: "active-orch", Roles: []RoleView{
			{RoleID: 21, OrchID: 2, Name: "acoord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "ta"},
		}}},
	}
}

// ruleIndexes returns the row indices of every rrRule (divider) row.
func (r *Rail) ruleIndexes() []int {
	var idx []int
	for i, row := range r.rows {
		if row.kind == rrRule {
			idx = append(idx, i)
		}
	}
	return idx
}

// TestRail_PinnedSectionDivider pins BUG-027 (updated for add-kanban-focus-fold):
// a horizontal-rule divider sits between the Pinned section and the "Active
// (N)" group header, mirroring the Freelance/Archive dividers — now via the
// SAME uniform per-group divider Active shares with Backlog/Blocked/Done,
// rather than a distinct Pinned→Active special case.
func TestRail_PinnedSectionDivider(t *testing.T) {
	r := NewRail()
	r.SetModel(pinnedPlusActiveModel())

	// Rows: Pinned header, pinned-orch, ─ divider, "Active (1)" header,
	// active-orch = 5.
	testutil.Equal(t, r.Rows(), 5)

	rules := r.ruleIndexes()
	testutil.Equal(t, len(rules), 1) // exactly one divider
	div := rules[0]

	// The divider sits AFTER the last Pinned row and BEFORE Active's own group
	// header: the row above it belongs to the Pinned section (its orch), and
	// the row below it is the "Active (N)" header.
	testutil.Equal(t, r.rows[div-1].kind, rrOrch)
	testutil.Equal(t, r.rows[div-1].orch.Name, "pinned-orch")
	group, ok := r.rows[div+1].kanbanGroupHeader()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, group, db.HeraKanbanActive)

	// The divider AND the (non-selectable) group header are skipped: j/k
	// navigation steps from the pinned orch straight to the active orch.
	testutil.Equal(t, r.SelectedOrch().Name, "pinned-orch")
	r.CursorDown()
	testutil.Equal(t, r.SelectedOrch().Name, "active-orch")
}

// TestRail_NoPinnedDividerWithoutPins: with no pinned section, Active still
// renders its OWN leading divider (add-kanban-focus-fold retired the distinct
// Pinned→Active special case in favor of the uniform per-group divider every
// non-empty kanban group now carries, including Active).
func TestRail_NoPinnedDividerWithoutPins(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel()) // active-only
	testutil.Equal(t, len(r.ruleIndexes()), 1)
}

// TestRail_NoPinnedDividerWithoutActive: a Pinned section with no Active entries
// below it renders no trailing divider.
func TestRail_NoPinnedDividerWithoutActive(t *testing.T) {
	r := NewRail()
	r.SetModel(Model{Pinned: []OrchView{{ID: 1, Name: "pinned-orch", Pinned: true, Roles: []RoleView{
		{RoleID: 11, OrchID: 1, Name: "pcoord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tp"},
	}}}})
	// Rows: Pinned header + pinned-orch = 2, no divider.
	testutil.Equal(t, r.Rows(), 2)
	testutil.Equal(t, len(r.ruleIndexes()), 0)
}

// TestRail_KanbanGrouping pins add-hera-kanban-status + add-kanban-focus-fold:
// the active list is partitioned into Active (N) → Backlog (N) → Blocked (N)
// → Done (N), EACH preceded by its own unconditioned divider (Active is no
// longer a headerless special case), empty groups suppressed entirely, only
// the FOCUSED group's members render (the other three show header+count
// only), and a nested orchestrator's own kanban status never affecting
// placement.
func TestRail_KanbanGrouping(t *testing.T) {
	t.Run("all-active (default/unset) renders a single Active header, no Backlog/Blocked/Done headers", func(t *testing.T) {
		r := NewRail()
		r.SetModel(twoOrchModel()) // neither orchestrator sets KanbanStatus
		headers := 0
		for _, row := range r.rows {
			if group, ok := row.kanbanGroupHeader(); ok {
				headers++
				testutil.Equal(t, group, db.HeraKanbanActive)
			}
		}
		testutil.Equal(t, headers, 1)
		testutil.Equal(t, len(r.ruleIndexes()), 1) // Active's own divider
	})

	t.Run("non-empty backlog/blocked/done render labeled headers with dividers, in rail order; only the focused group (active, by default) shows its member", func(t *testing.T) {
		r := NewRail()
		r.SetModel(Model{Active: []OrchView{
			{ID: 1, Name: "act", KanbanStatus: db.HeraKanbanActive},
			{ID: 2, Name: "bl", KanbanStatus: db.HeraKanbanBacklog},
			{ID: 3, Name: "blk", KanbanStatus: db.HeraKanbanBlocked},
			{ID: 4, Name: "dn", KanbanStatus: db.HeraKanbanDone},
		}})

		// rule(0) "Active (1)"(1) act(2) | rule(3) "Backlog (1)"(4) | rule(5)
		// "Blocked (1)"(6) | rule(7) "Done (1)"(8) — only Active (the focused
		// group by default) renders its member row; the other three show
		// header+count only.
		testutil.Equal(t, r.Rows(), 9)
		testutil.Equal(t, r.rows[0].kind, rrRule)
		testutil.Equal(t, r.rows[1].label, "Active (1)")
		testutil.Equal(t, r.rows[2].orch.Name, "act")

		testutil.Equal(t, r.rows[3].kind, rrRule)
		testutil.Equal(t, r.rows[4].kind, rrSectionHeader)
		testutil.Equal(t, r.rows[4].label, "Backlog (1)")

		testutil.Equal(t, r.rows[5].kind, rrRule)
		testutil.Equal(t, r.rows[6].kind, rrSectionHeader)
		testutil.Equal(t, r.rows[6].label, "Blocked (1)")

		testutil.Equal(t, r.rows[7].kind, rrRule)
		testutil.Equal(t, r.rows[8].kind, rrSectionHeader)
		testutil.Equal(t, r.rows[8].label, "Done (1)")

		testutil.Equal(t, r.hasOrchHeader("bl"), false)
		testutil.Equal(t, r.hasOrchHeader("blk"), false)
		testutil.Equal(t, r.hasOrchHeader("dn"), false)
	})

	t.Run("an empty intermediate group renders neither header nor divider", func(t *testing.T) {
		r := NewRail()
		r.SetModel(Model{Active: []OrchView{
			{ID: 1, Name: "act", KanbanStatus: db.HeraKanbanActive},
			{ID: 2, Name: "dn", KanbanStatus: db.HeraKanbanDone},
		}})
		// rule(0) "Active (1)"(1) act(2) | rule(3) "Done (1)"(4) — no
		// Backlog/Blocked rows at all; dn's own row is hidden (Done isn't the
		// focused group).
		testutil.Equal(t, r.Rows(), 5)
		testutil.Equal(t, len(r.ruleIndexes()), 2)
		testutil.Equal(t, r.rows[4].label, "Done (1)")
		testutil.Equal(t, r.hasOrchHeader("dn"), false)
	})

	t.Run("Active's own divider follows the Pinned section like every other group", func(t *testing.T) {
		r := NewRail()
		r.SetModel(Model{
			Pinned: []OrchView{{ID: 9, Name: "pin"}},
			Active: []OrchView{{ID: 1, Name: "act", KanbanStatus: db.HeraKanbanActive}},
		})
		// Pinned-header(0) pin(1) | rule(2) "Active (1)"(3) act(4)
		testutil.Equal(t, len(r.ruleIndexes()), 1)
		testutil.Equal(t, r.rows[2].kind, rrRule)
		testutil.Equal(t, r.rows[3].label, "Active (1)")
		testutil.Equal(t, r.rows[4].orch.Name, "act")
	})

	t.Run("no stray divider when the active group is empty but Backlog is not (Backlog isn't focused by default)", func(t *testing.T) {
		r := NewRail()
		r.SetModel(Model{
			Pinned: []OrchView{{ID: 9, Name: "pin"}},
			Active: []OrchView{{ID: 2, Name: "bl", KanbanStatus: db.HeraKanbanBacklog}},
		})
		// Pinned-header(0) pin(1) | rule(2) "Backlog (1)" header(3) — Active
		// contributes nothing (zero active-status orchestrators); Backlog's own
		// member row is hidden since Backlog isn't the focused group.
		testutil.Equal(t, len(r.ruleIndexes()), 1)
		testutil.Equal(t, r.rows[2].kind, rrRule)
		testutil.Equal(t, r.rows[3].label, "Backlog (1)")
		testutil.Equal(t, r.Rows(), 4)
		testutil.Equal(t, r.hasOrchHeader("bl"), false)
	})

	t.Run("a nested orchestrator's own kanban status never affects placement", func(t *testing.T) {
		root := orchView(1, "R", "tr", wk("w", "tc"))
		child := orchView(2, "C", "tc", wk("wc", "twc"))
		child.KanbanStatus = db.HeraKanbanDone // nested — must be ignored for grouping
		r := NewRail()
		r.SetModel(Model{Active: []OrchView{root, child}})

		// C never surfaces as a top-level "Done" group; it still nests under R
		// exactly as the pre-kanban behavior (R header, bridging w, nested wc) —
		// now behind R's own "Active (1)" group header.
		testutil.Equal(t, r.Rows(), 5)
		testutil.Equal(t, r.hasOrchHeader("C"), false)
		for _, row := range r.rows {
			if group, ok := row.kanbanGroupHeader(); ok {
				testutil.Equal(t, group, db.HeraKanbanActive)
			}
		}
	})
}

func TestRail_EmptyModel(t *testing.T) {
	r := NewRail()
	r.SetModel(Model{})
	testutil.Equal(t, r.Rows(), 1)
	testutil.Equal(t, r.rows[0].kind, rrEmpty)
	// Nav + collapse on the empty placeholder are safe no-ops.
	r.CursorDown()
	r.ToggleCollapse()
	testutil.Nil(t, r.Selected())
}

func TestRail_CursorRestoredAcrossRebuild(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())
	r.CursorDown() // coord folds into the header, so one step lands on wkr
	testutil.Equal(t, r.Selected().Name, "wkr")

	// Rebuild with the same model — cursor should stay on role 12 (wkr).
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.Selected().Name, "wkr")
}

func TestRail_MouseHandler_Scroll(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())
	r.SetRect(0, 0, 40, 20)
	handler := r.MouseHandler()
	setFocus := func(tview.Primitive) {}

	// Cursor starts on orch-1's header (first selectable row).
	testutil.Equal(t, r.SelectedOrch().Name, "orch-1")

	// The cursor is what scroll drags, not an independent viewport, so a
	// scroll gesture moves the cursor in the SAME direction as the wheel
	// (trackpad "natural" scrolling) — MouseScrollUp advances the cursor.
	consumed, _ := handler(tview.MouseScrollUp, tcell.NewEventMouse(2, 2, tcell.ButtonNone, 0), setFocus)
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, r.Selected().Name, "wkr") // coord folds into the header, so one step lands on wkr

	consumed, _ = handler(tview.MouseScrollDown, tcell.NewEventMouse(2, 2, tcell.ButtonNone, 0), setFocus)
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, r.SelectedOrch().Name, "orch-1")
}

func TestRail_MouseHandler_LeftDownFocuses(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())
	r.SetRect(0, 0, 40, 20)
	handler := r.MouseHandler()

	var focused tview.Primitive
	consumed, _ := handler(tview.MouseLeftDown, tcell.NewEventMouse(2, 2, tcell.Button1, 0), func(p tview.Primitive) { focused = p })
	testutil.Equal(t, consumed, true)
	testutil.Equal(t, focused, tview.Primitive(r))
}

func TestRail_MouseHandler_OutOfRectIgnored(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())
	r.SetRect(0, 0, 40, 20)
	handler := r.MouseHandler()

	before := r.CursorIndex()
	consumed, _ := handler(tview.MouseScrollDown, tcell.NewEventMouse(100, 100, tcell.ButtonNone, 0), func(tview.Primitive) {})
	testutil.Equal(t, consumed, false)
	testutil.Equal(t, r.CursorIndex(), before)
}

func TestStatusIcon_ReadyToCloseWins(t *testing.T) {
	// ready_to_close overrides the role status with the distinct review mark
	// (session NOT running here → not active, so ready_to_close wins).
	icon, _ := statusIcon(&RoleView{ReadyToClose: true, HasStatus: true, Status: db.HeraStatusWorking}, false, 0)
	testutil.Equal(t, icon, theme.IconReview)
}

// TestStatusIcon_ActiveOutranksReadyToClose pins BUG-F (the icon-precedence
// completion of BUG-C): a live worker rolled to in_review with ready_to_close
// stamped by the done-roll that is STILL producing output (running, not
// session-idle) animates the spinner — the honest activity signal (IsActive)
// outranks the now-stale ready_to_close review glyph. When the session goes idle
// the review glyph returns, so the resting close-out state is preserved.
func TestStatusIcon_ActiveOutranksReadyToClose(t *testing.T) {
	widget.SetActiveSpinner("progress")
	defer widget.SetActiveSpinner("progress")

	// Reactivated ready_to_close worker: live binding, running session, producing
	// output (not idle), task rolled to in_review, ready_to_close stamped.
	reactivated := &RoleView{Live: true, SessionRunning: true, TaskStatus: "in_review", ReadyToClose: true}
	g0, _ := statusIcon(reactivated, false, 0)
	g1, _ := statusIcon(reactivated, false, 1)
	testutil.Equal(t, g0, widget.SpinnerFrame(0))
	testutil.Equal(t, g1, widget.SpinnerFrame(1))

	// Resting case preserved: once the session idles (BUG-036 content-idle), IsActive
	// drops false and the ready_to_close review glyph returns.
	resting := &RoleView{Live: true, SessionRunning: true, TaskStatus: "in_review", SessionIdle: true, ReadyToClose: true}
	r0, _ := statusIcon(resting, false, 0)
	testutil.Equal(t, r0, theme.IconReview)
}

func TestStatusIcon_StatusMapping(t *testing.T) {
	cases := []struct {
		status db.HeraRoleStatusValue
	}{
		{db.HeraStatusWorking},
		{db.HeraStatusBlocked},
		{db.HeraStatusDone},
		{db.HeraStatusIdle},
		{db.HeraStatusFailed},
	}
	for _, c := range cases {
		// Each known status yields a non-zero glyph without panicking.
		icon, _ := statusIcon(&RoleView{HasStatus: true, Status: c.status}, false, 0)
		if icon == 0 {
			t.Errorf("status %q produced zero glyph", c.status)
		}
	}
	// No status, no binding → falls back to a dimmed moon.
	icon, _ := statusIcon(&RoleView{}, false, 0)
	if icon == 0 {
		t.Error("fallback produced zero glyph")
	}
	// Bound but statusless → distinct glyph.
	icon2, _ := statusIcon(&RoleView{Live: true}, false, 0)
	if icon2 == 0 {
		t.Error("live-statusless produced zero glyph")
	}
}

// TestStatusIcon_ActiveAnimatesSpinner pins BUG-003: the rail spinner animates
// on REAL session activity (a live binding whose bound argus task is
// in_progress), NOT on the manual/MCP-set hera role-status "working" field
// (which goes stale — it stays "working" after the session idles, stops, or
// dies). A genuinely-active role animates; a stale-working / stopped / idle /
// dead role is static.
func TestStatusIcon_ActiveAnimatesSpinner(t *testing.T) {
	widget.SetActiveSpinner("progress")
	defer widget.SetActiveSpinner("progress")

	// A genuinely active role (live binding + bound task in_progress) renders the
	// active spinner's frame and advances with the frame counter.
	active := &RoleView{Live: true, SessionRunning: true, TaskStatus: "in_progress", HasStatus: true, Status: db.HeraStatusWorking}
	f0, _ := statusIcon(active, false, 0)
	f1, _ := statusIcon(active, false, 1)
	testutil.Equal(t, f0, widget.SpinnerFrame(0))
	testutil.Equal(t, f1, widget.SpinnerFrame(1))
	if f0 == f1 {
		t.Error("active glyph did not advance between frames")
	}

	// Real activity drives the spinner even when the stale role-status disagrees
	// (here it claims idle): the bound task is genuinely in_progress.
	activeStaleStatus := &RoleView{Live: true, SessionRunning: true, TaskStatus: "in_progress", HasStatus: true, Status: db.HeraStatusIdle}
	a0, _ := statusIcon(activeStaleStatus, false, 0)
	testutil.Equal(t, a0, widget.SpinnerFrame(0))

	// STALE working role-status with NO live binding (a stopped/dead session
	// showing "claude --resume …") must NOT animate — this is the bug.
	staleStopped := &RoleView{HasStatus: true, Status: db.HeraStatusWorking}
	s0, _ := statusIcon(staleStopped, false, 0)
	s1, _ := statusIcon(staleStopped, false, 7)
	testutil.Equal(t, s0, s1)
	if s0 == widget.SpinnerFrame(0) {
		t.Error("stale-working stopped role animated the spinner (BUG-003 regression)")
	}

	// BUG-C regression: a DEAD worker whose binding LINGERS (bindings don't end on
	// session exit) stays Live with a stale working status but is NOT in the
	// running set → must NOT animate. This is the case Live && !SessionIdle alone
	// got wrong (a dead session is neither running nor in the idle set).
	deadLingering := &RoleView{Live: true, SessionRunning: false, TaskStatus: "in_review", HasStatus: true, Status: db.HeraStatusWorking}
	x0, _ := statusIcon(deadLingering, false, 0)
	x1, _ := statusIcon(deadLingering, false, 4)
	testutil.Equal(t, x0, x1)
	if x0 == widget.SpinnerFrame(0) {
		t.Error("dead-but-lingering role animated the spinner (BUG-003 regression)")
	}

	// BUG-C: a live role in in_review (e.g. the #707 close-out window) whose
	// session is STILL RUNNING and producing output DOES animate — the spinner is
	// gated on liveness + running + content-idle, not bound-task status.
	// Previously this fell through to the static review glyph and looked parked.
	activeInReview := &RoleView{Live: true, SessionRunning: true, TaskStatus: "in_review", HasStatus: true, Status: db.HeraStatusWorking}
	r0, _ := statusIcon(activeInReview, false, 0)
	r1, _ := statusIcon(activeInReview, false, 1)
	testutil.Equal(t, r0, widget.SpinnerFrame(0))
	testutil.Equal(t, r1, widget.SpinnerFrame(1))

	// BUG-036: a live role whose SESSION is idle (parked fullscreen agent, content
	// stable) is NOT producing → static, even with stale Status==working and any
	// task status.
	staleLiveIdle := &RoleView{Live: true, SessionRunning: true, TaskStatus: "in_review", SessionIdle: true, HasStatus: true, Status: db.HeraStatusWorking}
	d0, _ := statusIcon(staleLiveIdle, false, 0)
	d1, _ := statusIcon(staleLiveIdle, false, 3)
	testutil.Equal(t, d0, d1)
	if d0 == widget.SpinnerFrame(0) {
		t.Error("live-but-session-idle role animated the spinner (BUG-036 regression)")
	}

	// A non-active (idle) role is static across frames.
	idle := &RoleView{HasStatus: true, Status: db.HeraStatusIdle}
	i0, _ := statusIcon(idle, false, 0)
	i1, _ := statusIcon(idle, false, 5)
	testutil.Equal(t, i0, i1)
}

// TestRoleView_IsActive isolates the activity predicate that sources the spinner.
// Post-BUG-C the predicate is liveness + session-RUNNING + content-idle, NOT
// bound-task status: a live, running, content-active worker spins regardless of
// task status (in_progress OR the #707 in_review close-out window); a
// live-but-idle (BUG-036), live-but-dead (BUG-003), or unbound session does not.
func TestRoleView_IsActive(t *testing.T) {
	cases := []struct {
		name string
		role RoleView
		want bool
	}{
		{"live running in_progress active", RoleView{Live: true, SessionRunning: true, TaskStatus: "in_progress"}, true},
		{"live running in_progress but session-idle (BUG-036)", RoleView{Live: true, SessionRunning: true, TaskStatus: "in_progress", SessionIdle: true}, false},
		// BUG-C: a live worker rolled to in_review (#707 close-out window) whose
		// session is STILL RUNNING and producing output must spin, not fall through
		// to the review glyph.
		{"live running in_review active (BUG-C)", RoleView{Live: true, SessionRunning: true, TaskStatus: "in_review"}, true},
		{"live running in_review but session-idle (parked/done)", RoleView{Live: true, SessionRunning: true, TaskStatus: "in_review", SessionIdle: true}, false},
		{"live running complete but active", RoleView{Live: true, SessionRunning: true, TaskStatus: "complete"}, true},
		{"live running no task snapshot, active", RoleView{Live: true, SessionRunning: true, TaskStatus: ""}, true},
		// BUG-C regression: bindings do NOT end on session exit, so a DEAD worker
		// stays Live but drops from the running set — it must NOT spin. This is the
		// case Live && !SessionIdle alone got wrong.
		{"live but NOT running — dead worker, binding lingers (BUG-003)", RoleView{Live: true, SessionRunning: false, TaskStatus: "in_review"}, false},
		{"live but NOT running, in_progress task (BUG-003)", RoleView{Live: true, SessionRunning: false, TaskStatus: "in_progress"}, false},
		{"not live but in_progress task (BUG-003)", RoleView{Live: false, SessionRunning: true, TaskStatus: "in_progress"}, false},
		{"stale working status only", RoleView{HasStatus: true, Status: db.HeraStatusWorking}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.Equal(t, c.role.IsActive(), c.want)
		})
	}
}

// TestStatusIcon_BlockedOutranksActivity confirms an operator/agent "blocked"
// assertion still shows the needs-input glyph even when the bound task is
// technically still in_progress (alive, waiting) — blocked is a deliberate
// signal that must not be masked by the activity spinner.
func TestStatusIcon_BlockedOutranksActivity(t *testing.T) {
	blocked := &RoleView{Live: true, TaskStatus: "in_progress", HasStatus: true, Status: db.HeraStatusBlocked}
	icon, _ := statusIcon(blocked, false, 0)
	testutil.Equal(t, icon, theme.IconNeedsInput)
}

// TestStatusIcon_NeedsInputSources covers the BUG-018 "(?)" triggers + precedence:
// the role's own authoritative NeedsInput flag, the subtree rollup, and the
// precedence against ready_to_close (now LOSES to needs-input, BUG-A) and done
// (loses to rollup).
func TestStatusIcon_NeedsInputSources(t *testing.T) {
	t.Run("own needs-input flag shows (?)", func(t *testing.T) {
		icon, style := statusIcon(&RoleView{Live: true, TaskStatus: "in_progress", NeedsInput: true}, false, 0)
		testutil.Equal(t, icon, theme.IconNeedsInput)
		testutil.Equal(t, style, theme.StyleNeedsInput)
	})
	t.Run("subtree rollup alone does not surface (?) on an otherwise-idle coordinator", func(t *testing.T) {
		// A coordinator with no own signal (idle/working) but a needs-input
		// descendant: the rollup no longer drives the coordinator's own glyph
		// (remove-needs-input-rollup-glyph) — its own status glyph shows instead.
		icon, _ := statusIcon(&RoleView{Live: true, HasStatus: true, Status: db.HeraStatusWorking, SubtreeNeedsInput: true}, false, 0)
		if icon == theme.IconNeedsInput {
			t.Fatalf("expected the coordinator's own status glyph, got needs-input %q", icon)
		}
	})
	t.Run("a done coordinator's own glyph is unaffected by a descendant's rollup", func(t *testing.T) {
		icon, _ := statusIcon(&RoleView{Live: true, HasStatus: true, Status: db.HeraStatusDone, SubtreeNeedsInput: true}, false, 0)
		testutil.Equal(t, icon, '✓')
	})
	t.Run("needs-input wins over ready_to_close (BUG-A)", func(t *testing.T) {
		// A worker stamped ready_to_close (done-roll) that is ALSO genuinely
		// blocked (its OWN signal) must surface "(?)", not the review glyph — the
		// actionable block outranks the now-contradicted "ready to close out" stamp.
		icon, _ := statusIcon(&RoleView{Live: true, ReadyToClose: true, NeedsInput: true}, false, 0)
		testutil.Equal(t, icon, theme.IconNeedsInput)
	})
	t.Run("ready_to_close shows review glyph when not blocked", func(t *testing.T) {
		icon, _ := statusIcon(&RoleView{Live: true, ReadyToClose: true}, false, 0)
		testutil.Equal(t, icon, theme.IconReview)
	})
	t.Run("no needs-input → not (?)", func(t *testing.T) {
		icon, _ := statusIcon(&RoleView{Live: true, TaskStatus: "in_progress"}, false, 0)
		if icon == theme.IconNeedsInput {
			t.Fatalf("expected a non-needs-input glyph, got %q", icon)
		}
	})
}

// TestStatusIcon_Failed pins the D2 (make-hera-plan-living) failed rail glyph.
// A role with status "failed" renders a red ✕ via roleStatusInputs → widget,
// placed below NeedsInput and above Done in precedence.
func TestStatusIcon_Failed(t *testing.T) {
	t.Run("failed renders ✕", func(t *testing.T) {
		icon, style := statusIcon(&RoleView{HasStatus: true, Status: db.HeraStatusFailed}, false, 0)
		testutil.Equal(t, icon, '✕')
		testutil.Equal(t, style, theme.StyleError)
	})

	t.Run("failed is distinct from done ✓", func(t *testing.T) {
		failedIcon, _ := statusIcon(&RoleView{HasStatus: true, Status: db.HeraStatusFailed}, false, 0)
		doneIcon, _ := statusIcon(&RoleView{HasStatus: true, Status: db.HeraStatusDone}, false, 0)
		if failedIcon == doneIcon {
			t.Fatalf("failed glyph %q must differ from done glyph %q", failedIcon, doneIcon)
		}
	})

	t.Run("needs-input beats failed", func(t *testing.T) {
		// ShowsNeedsInput is the role's own needs-input signal only.
		icon, _ := statusIcon(&RoleView{
			HasStatus: true, Status: db.HeraStatusFailed, NeedsInput: true,
		}, false, 0)
		testutil.Equal(t, icon, theme.IconNeedsInput)
	})

	t.Run("ready_to_close beats failed", func(t *testing.T) {
		icon, _ := statusIcon(&RoleView{
			HasStatus: true, Status: db.HeraStatusFailed, ReadyToClose: true,
		}, false, 0)
		testutil.Equal(t, icon, theme.IconReview)
	})

	t.Run("failed appears in TestStatusIcon_StatusMapping set", func(t *testing.T) {
		// All five role-status values yield a non-zero glyph without panicking.
		for _, sv := range []db.HeraRoleStatusValue{
			db.HeraStatusIdle, db.HeraStatusWorking, db.HeraStatusBlocked,
			db.HeraStatusDone, db.HeraStatusFailed,
		} {
			icon, _ := statusIcon(&RoleView{HasStatus: true, Status: sv}, false, 0)
			if icon == 0 {
				t.Errorf("status %q produced zero glyph", sv)
			}
		}
	})
}

// TestContextIndicator_Tiers pins the add-worker-context-indicator delta's
// four-tier ramp (unit-level, mirroring how statusIcon itself is tested
// directly rather than only through a full Draw pass): no glyph under 40%,
// a pale-yellow dot 40-64%, a hot-orange dot 65-89%, a red bang 90%+.
func TestContextIndicator_Tiers(t *testing.T) {
	cases := []struct {
		name      string
		pct       int
		wantGlyph rune
		wantStyle tcell.Style
	}{
		{"under 40 is blank", 0, 0, tcell.StyleDefault},
		{"39 is still blank", 39, 0, tcell.StyleDefault},
		{"40 starts the warm dot", 40, '•', theme.StyleContextWarm},
		{"64 is still warm", 64, '•', theme.StyleContextWarm},
		{"65 starts the hot dot", 65, '•', theme.StyleContextHot},
		{"89 is still hot", 89, '•', theme.StyleContextHot},
		{"90 starts the bang", 90, '!', theme.StyleContextCritical},
		{"100 is still the bang", 100, '!', theme.StyleContextCritical},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			role := &RoleView{Kind: db.HeraKindWorker, Live: true, ContextPercent: c.pct}
			reserve, glyph, style := contextIndicator(role)
			testutil.Equal(t, reserve, true)
			testutil.Equal(t, glyph, c.wantGlyph)
			testutil.Equal(t, style, c.wantStyle)
		})
	}
}

// TestContextIndicator_CoordinatorNeverReserves pins that a coordinator role
// never reserves or renders the slot at all, regardless of context
// percentage — it keeps its live-count badge in that position instead.
func TestContextIndicator_CoordinatorNeverReserves(t *testing.T) {
	role := &RoleView{Kind: db.HeraKindCoordinator, Live: true, ContextPercent: 95}
	reserve, glyph, _ := contextIndicator(role)
	testutil.Equal(t, reserve, false)
	testutil.Equal(t, glyph, rune(0))
}

// TestContextIndicator_FreelanceEligible pins that freelance is eligible on
// the same terms as worker — the exclusion is specifically "coordinator",
// not "anything that isn't worker".
func TestContextIndicator_FreelanceEligible(t *testing.T) {
	role := &RoleView{Kind: db.HeraKindFreelance, Live: true, ContextPercent: 92}
	reserve, glyph, style := contextIndicator(role)
	testutil.Equal(t, reserve, true)
	testutil.Equal(t, glyph, '!')
	testutil.Equal(t, style, theme.StyleContextCritical)
}

// TestContextIndicator_DeadOrArchivedRendersNoGlyph pins that a worker row
// still reserves its slot (for column-alignment stability) when not live or
// when archived, but never actually draws a glyph into it — a stale
// ContextPercent from a previous session must not paint a bang on a dead row.
func TestContextIndicator_DeadOrArchivedRendersNoGlyph(t *testing.T) {
	t.Run("not live", func(t *testing.T) {
		role := &RoleView{Kind: db.HeraKindWorker, Live: false, ContextPercent: 95}
		reserve, glyph, _ := contextIndicator(role)
		testutil.Equal(t, reserve, true)
		testutil.Equal(t, glyph, rune(0))
	})
	t.Run("archived", func(t *testing.T) {
		role := &RoleView{Kind: db.HeraKindWorker, Live: true, Archived: true, ContextPercent: 95}
		reserve, glyph, _ := contextIndicator(role)
		testutil.Equal(t, reserve, true)
		testutil.Equal(t, glyph, rune(0))
	})
}

// TestDrawOrchRow_BareCount pins the hera-view delta's other half: the
// coordinator live-count badge drops its parentheses entirely.
func TestDrawOrchRow_BareCount(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 10)

	r := NewRail()
	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "orch", Roles: []RoleView{
		{RoleID: 11, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
		{RoleID: 12, Name: "wkr1", Kind: db.HeraKindWorker, Live: true, TaskID: "t1"},
		{RoleID: 13, Name: "wkr2", Kind: db.HeraKindWorker, Live: true, TaskID: "t2"},
	}}}})
	r.SetRect(0, 0, 40, 10)
	r.Draw(sim)
	sim.Show()

	// Row 3 = the orch header (row 1 = the leading rule, row 2 = "Active (1)").
	// liveRoleCount excludes the coordinator itself, so 2 live workers -> "2".
	var line string
	for x := 0; x < 40; x++ {
		s, _, _ := sim.Get(x, 3)
		line += s
	}
	testutil.Contains(t, line, "2")
	if strings.ContainsAny(line, "()") {
		t.Errorf("orchestrator header must render a bare count with no parens; got %q", line)
	}
}

// TestRail_OrchHeaderRendersSubtreeCost is the SimulationScreen integration
// proof for add-coordinator-cost-estimate's Decision 5/7: the orchestrator
// header renders the blended subtree cost alongside the existing
// agent-count badge when the subtree has accrued anything measured.
func TestRail_OrchHeaderRendersSubtreeCost(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 10)

	r := NewRail()
	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "orch", Roles: []RoleView{
		{RoleID: 11, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc", CostUSDAccrued: 1.5},
		{RoleID: 12, Name: "wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "twk", CostUSDAccrued: 3.25},
	}}}})
	r.SetRect(0, 0, 40, 10)
	r.Draw(sim)
	sim.Show()

	// Row 3 = the orch header (row 1 leading rule, row 2 group header).
	var line string
	for x := 0; x < 40; x++ {
		s, _, _ := sim.Get(x, 3)
		line += s
	}
	testutil.Contains(t, line, "$4.75") // 1.5 + 3.25
}

// TestRail_OrchHeaderOmitsCostWhenUnmeasured pins the "n/a, not $0.00"
// contract at the rendering layer: an orchestrator whose subtree has never
// accrued anything shows no cost figure at all, not "$0.00".
func TestRail_OrchHeaderOmitsCostWhenUnmeasured(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 10)

	r := NewRail()
	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "orch", Roles: []RoleView{
		{RoleID: 11, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
	}}}})
	r.SetRect(0, 0, 40, 10)
	r.Draw(sim)
	sim.Show()

	var line string
	for x := 0; x < 40; x++ {
		s, _, _ := sim.Get(x, 3)
		line += s
	}
	if strings.Contains(line, "$") {
		t.Errorf("expected no cost figure on an unmeasured orchestrator header; got %q", line)
	}
}

// TestRail_ContextIndicatorRendersOnWorkerRow is the SimulationScreen
// integration proof (mirrors TestRail_PRIndicatorOnManagedRow's shape): the
// bang actually reaches the screen on a critical worker row, and never
// appears on the coordinator's own row.
func TestRail_ContextIndicatorRendersOnWorkerRow(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 10)

	r := NewRail()
	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "orch", Roles: []RoleView{
		{RoleID: 11, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc", ContextPercent: 95},
		{RoleID: 12, Name: "wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "twk", ContextPercent: 95},
	}}}})
	r.SetRect(0, 0, 40, 10)
	r.Draw(sim)
	sim.Show()

	// Row 3 = orch header, row 4 = the worker (row 1 leading rule, row 2 group header).
	foundOnWorker := false
	for x := 0; x < 40; x++ {
		s, _, _ := sim.Get(x, 4)
		if s == "!" {
			foundOnWorker = true
			break
		}
	}
	testutil.Equal(t, foundOnWorker, true)

	for x := 0; x < 40; x++ {
		s, _, _ := sim.Get(x, 3)
		if s == "!" {
			t.Fatalf("coordinator row must never render the context bang; found one at x=%d", x)
		}
	}
}

// TestRail_ContextIndicatorComposesWithPRTag pins that a row eligible for
// both the PR tag and the context bang renders both, per the hera-view
// delta's composition scenario.
func TestRail_ContextIndicatorComposesWithPRTag(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 10)

	r := NewRail()
	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "orch", Roles: []RoleView{
		{RoleID: 11, Name: "wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "twk", ContextPercent: 95},
	}}}})
	r.SetPRMeta(map[string]map[string]string{"twk": {"url": "https://example/pr/1", "state": "awaiting-review"}})
	r.SetRect(0, 0, 40, 10)
	r.Draw(sim)
	sim.Show()

	// Row 4 = the worker row (row 1 leading rule, row 2 group header, row 3 the
	// orch header that always renders even for a coordinator-less orchestrator).
	var line string
	for x := 0; x < 40; x++ {
		s, _, _ := sim.Get(x, 4)
		line += s
	}
	testutil.Contains(t, line, "PR")
	testutil.Contains(t, line, "!")
}

// TestRail_DrawDoesNotPanic exercises every drawRow branch against a real
// SimulationScreen (the required SimulationScreen integration for new widget
// rendering). It covers orchestrators, roles, freelance, archive, rules, and
// the ready_to_close mark.
func TestRail_DrawDoesNotPanic(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 24)

	r := NewRail()
	r.SetFocused(true)
	r.SetModel(Model{
		Pinned: []OrchView{{ID: 5, Name: "pinned", Pinned: true, Roles: []RoleView{
			{RoleID: 50, Name: "p-role", Live: true, ReadyToClose: true},
		}}},
		Active: []OrchView{{ID: 1, Name: "orch-1", Roles: []RoleView{
			{RoleID: 11, Name: "working", HasStatus: true, Status: db.HeraStatusWorking, Live: true},
			{RoleID: 12, Name: "blocked", HasStatus: true, Status: db.HeraStatusBlocked},
		}}},
		Freelance: []RoleView{{RoleID: 91, Name: "free", Kind: db.HeraKindFreelance}},
		Archived:  []OrchView{{ID: 9, Name: "old", Archived: true, Roles: []RoleView{{RoleID: 99, Name: "r"}}}},
	})
	r.SetRect(0, 0, 40, 24)
	r.Draw(sim) // must not panic

	// Expand the archive (dimmed placement path) and redraw.
	for r.rows[r.cursor].kind != rrArchiveExpando {
		r.CursorDown()
		if r.cursor == r.Rows()-1 {
			break
		}
	}
	r.ToggleCollapse()
	r.Draw(sim)

	// Narrow terminal must not panic (width-clamp guard).
	r.SetRect(0, 0, 1, 24)
	r.Draw(sim)
}

// TestRail_CursorToParent covers BUG-016: the Left-arrow parent-nav helper.
func TestRail_CursorToParent(t *testing.T) {
	t.Run("worker to orch header", func(t *testing.T) {
		r := NewRail()
		r.SetModel(twoOrchModel()) // rule(0) header(1) orch-1(2) wkr(3) orch-2(4)
		r.CursorDown()             // cursor → worker (row 3)
		testutil.Equal(t, r.rows[r.cursor].role.Name, "wkr")
		r.CursorToParent()
		testutil.Equal(t, r.CursorIndex(), 2)
		testutil.Equal(t, r.SelectedOrch().Name, "orch-1")
	})

	t.Run("root orch header no-op", func(t *testing.T) {
		r := NewRail()
		r.SetModel(twoOrchModel()) // cursor starts at row 2 (orch-1 header, first selectable)
		testutil.Equal(t, r.CursorIndex(), 2)
		r.CursorToParent()
		testutil.Equal(t, r.CursorIndex(), 2) // no-op — depth 0
	})

	t.Run("second orch header no-op", func(t *testing.T) {
		r := NewRail()
		r.SetModel(twoOrchModel()) // rule(0) header(1) orch-1(2) wkr(3) orch-2(4)
		r.CursorDown()
		r.CursorDown() // cursor → orch-2 header (row 4, depth 0)
		testutil.Equal(t, r.SelectedOrch().Name, "orch-2")
		r.CursorToParent()
		testutil.Equal(t, r.SelectedOrch().Name, "orch-2") // no-op — depth 0
	})

	t.Run("freelance role no-op", func(t *testing.T) {
		r := NewRail()
		r.SetModel(Model{
			Freelance: []RoleView{
				{RoleID: 1, Name: "free", Kind: db.HeraKindFreelance},
			},
		})
		// Rail: rrRule | rrSectionHeader(Freelance) | rrFreelanceRole
		// The section header is selectable (collFreelance:true) — cursor starts
		// there; step down to the freelance role row.
		for r.cursor < r.Rows()-1 && r.rows[r.cursor].kind != rrFreelanceRole {
			r.CursorDown()
		}
		prev := r.CursorIndex()
		r.CursorToParent()
		testutil.Equal(t, r.CursorIndex(), prev) // no-op — no rrOrch/bridging row above
	})

	t.Run("nested coord-spawn child to root header", func(t *testing.T) {
		// Build a model where orch-B is a coord-spawn child of orch-A: both share
		// the same coordinator bridge task, and A's coordinator role was created first.
		r := NewRail()
		// Direct row manipulation: force a three-depth structure
		// root header(depth 0) | child header(depth 1) | grandchild role(depth 2)
		r.rows = []railRow{
			{kind: rrOrch, orch: &OrchView{ID: 1, Name: "root"}, depth: 0, collOrchID: 1},
			{kind: rrOrch, orch: &OrchView{ID: 2, Name: "child"}, depth: 1, collOrchID: 2},
			{kind: rrRole, role: &RoleView{RoleID: 10, Name: "worker"}, depth: 2},
		}
		// cursor on grandchild role → CursorToParent → child header
		r.cursor = 2
		r.CursorToParent()
		testutil.Equal(t, r.CursorIndex(), 1)
		testutil.Equal(t, r.rows[r.cursor].orch.Name, "child")
		// cursor on child header → CursorToParent → root header
		r.CursorToParent()
		testutil.Equal(t, r.CursorIndex(), 0)
		testutil.Equal(t, r.rows[r.cursor].orch.Name, "root")
		// cursor on root header → no-op
		r.CursorToParent()
		testutil.Equal(t, r.CursorIndex(), 0)
	})

	t.Run("bridging role as parent", func(t *testing.T) {
		// A bridging worker row (rrRole with collOrchID set) acts as the parent
		// coordinator for the worker rows nested under it.
		r := NewRail()
		r.rows = []railRow{
			{kind: rrOrch, orch: &OrchView{ID: 1, Name: "root"}, depth: 0, collOrchID: 1},
			{kind: rrRole, role: &RoleView{RoleID: 5, Name: "bridge"}, depth: 1, collOrchID: 99},
			{kind: rrRole, role: &RoleView{RoleID: 6, Name: "nested-worker"}, depth: 2},
		}
		r.cursor = 2 // nested-worker
		r.CursorToParent()
		testutil.Equal(t, r.CursorIndex(), 1) // lands on the bridging row
		testutil.Equal(t, r.rows[r.cursor].role.Name, "bridge")
	})
}
