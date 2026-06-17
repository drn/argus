package hera

import (
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
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
	// header + its single worker, and orch-2 (coordinator-only) is header-only:
	// 2 headers + 1 worker = 3 selectable rows.
	testutil.Equal(t, r.Rows(), 3)

	// Cursor starts at 0 (first orch header).
	testutil.Equal(t, r.CursorIndex(), 0)
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
	testutil.Equal(t, r.Rows(), 3)

	// Cursor on orch-1 header; collapse it → its worker row vanishes.
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 2) // orch-1 (collapsed) + orch-2 header

	// Expand again.
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 3)
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
	// orch-1 expanded shows exactly its worker (no coord child row).
	testutil.Equal(t, r.Rows(), 3)

	// liveRoleCount excludes the folded coordinator: orch-1 has 1 live worker.
	testutil.Equal(t, liveRoleCount(&r.model.Active[0]), 1)
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
	wantGlyph, _ := statusIcon(&RoleView{HasStatus: true, Status: db.HeraStatusBlocked, Live: true}, false, 0)
	found := false
	for x := 0; x < 40; x++ {
		s, _, _ := sim.Get(x, 1) // row 1 = first content row inside the border
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

	// R header(0), w(1, bridges C), wc(2). C never renders as a top-level header.
	testutil.Equal(t, r.Rows(), 3)
	testutil.Equal(t, r.depthOf("R"), 0)
	testutil.Equal(t, r.depthOf("w"), 1)
	testutil.Equal(t, r.depthOf("wc"), 2)
	testutil.Equal(t, r.hasOrchHeader("C"), false) // consumed → nested, not a root

	// The bridging worker row keeps its PARENT context (conservative selection):
	// it is the worker role (not a coordinator), so mutations act on the worker,
	// not the child orchestrator. Nesting is purely visual.
	r.cursor = 1
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
	testutil.Equal(t, r.Rows(), 3)

	// Cursor on the bridging worker row → Space folds the child subtree (wc hides).
	r.cursor = 1
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 2) // R header + w (child's wc folded away)
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
	// is already placed → no further nesting. 1 header + 2 worker rows.
	testutil.Equal(t, r.Rows(), 3)
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

	// P header(0), pw worker(1), Q nested header(1), qw worker(2).
	testutil.Equal(t, r.Rows(), 4)
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

func TestRail_CoordSpawnedSubteamCollapses(t *testing.T) {
	p := coordOf(1, "P", 100, "T")
	q := coordOf(2, "Q", 200, "T",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tqw"})
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, q}})
	// P header(0), Q nested header(1), qw(2).
	testutil.Equal(t, r.Rows(), 3)

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

	// Expanded: the archived bridging worker renders in place and nests C.
	testutil.Equal(t, r.depthOf("aw"), 1)
	testutil.Equal(t, r.depthOf("cw"), 2)
	testutil.Equal(t, rootHeaderCount(r), 1)

	// Collapse the parent P — C must stay folded, not leak.
	for r.rows[r.cursor].orch == nil || r.rows[r.cursor].orch.Name != "P" {
		r.CursorDown()
	}
	r.ToggleCollapse()

	testutil.Equal(t, r.depthOf("cw"), -1)
	testutil.Equal(t, r.hasOrchHeader("C"), false)
	testutil.Equal(t, rootHeaderCount(r), 1) // only P
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

func TestRail_ArchivedBridgingWorkerNestsActiveChild(t *testing.T) {
	// Root R (active) has an ARCHIVED worker w that bridges an ACTIVE child C.
	// w must render in place (dimmed) — NOT hoisted into the collapsed Archive
	// expando — so C nests under it instead of being safety-swept flat to a
	// top-level root. C stays NORMAL (only the archived worker row dims). This is
	// the archived-worker half of the under-nesting bug (done sub-teams flat).
	root := OrchView{ID: 1, Name: "R", Roles: []RoleView{
		{RoleID: 10, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tr", BridgeTaskID: "tr"},
		{RoleID: 11, Name: "w", Kind: db.HeraKindWorker, Live: true, Archived: true, TaskID: "tc", BridgeTaskID: "tc"},
	}}
	child := orchView(2, "C", "tc", wk("wc", "twc"))
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{root, child}})

	testutil.Equal(t, r.depthOf("w"), 1)
	testutil.Equal(t, r.depthOf("wc"), 2)
	testutil.Equal(t, r.hasOrchHeader("C"), false) // nested, not a top-level root
	roots := 0
	for _, row := range r.rows {
		if row.kind == rrOrch && row.depth == 0 {
			roots++
		}
		if row.role != nil && row.role.Name == "w" {
			testutil.Equal(t, row.dim, true) // archived worker row dims (honest)
		}
		if row.role != nil && row.role.Name == "wc" {
			testutil.Equal(t, row.dim, false) // active child subtree stays normal
		}
		testutil.Equal(t, row.kind == rrArchiveExpando, false) // bridging worker not hoisted
	}
	testutil.Equal(t, roots, 1) // only R is a root
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

	// header(0) + active-wkr(1) + per-coord "Archive (1)" expando(2); the archived
	// role is hidden under the collapsed-by-default expando.
	testutil.Equal(t, r.Rows(), 3)
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
	testutil.Equal(t, r.Rows(), 4)
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
	// Ctrl+D can cascade; a non-bridging row carries 0.
	r.cursor = 1 // the "w" bridging row
	testutil.Equal(t, r.Selection().BridgeChildOrchID, child.ID)
	r.cursor = 2 // "wc" leaf (no bridge)
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

	// A "pr" url on the worker's task flags it; a task without one does not.
	r.SetPRMeta(map[string]map[string]string{"twk": {"url": "https://example/pr/1"}})
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

func TestRail_FreelanceSectionCollapses(t *testing.T) {
	r := NewRail()
	r.SetModel(Model{
		Active:    []OrchView{{ID: 1, Name: "o"}},
		Freelance: []RoleView{{RoleID: 91, Name: "free-a", Kind: db.HeraKindFreelance}},
	})
	// orch header + rule + freelance header + 1 freelance role = 4.
	testutil.Equal(t, r.Rows(), 4)

	// Move cursor to the freelance header and collapse it.
	r.CursorDown() // freelance header (rule is non-selectable, skipped)
	testutil.Equal(t, r.rows[r.cursor].kind, rrSectionHeader)
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 3) // role hidden
}

func TestRail_ArchiveExpandoDefaultCollapsed(t *testing.T) {
	r := NewRail()
	r.SetModel(Model{
		Active:   []OrchView{{ID: 1, Name: "o"}},
		Archived: []OrchView{{ID: 9, Name: "old", Archived: true, Roles: []RoleView{{RoleID: 99, Name: "r"}}}},
	})
	// orch + rule + archive expando = 3 (archived orch hidden by default).
	testutil.Equal(t, r.Rows(), 3)

	// Cursor onto the archive expando and expand it.
	r.CursorDown() // archive expando
	testutil.Equal(t, r.rows[r.cursor].kind, rrArchiveExpando)
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 5) // + archived orch header + its role
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

func TestStatusIcon_ReadyToCloseWins(t *testing.T) {
	// ready_to_close overrides the role status with the distinct review mark.
	icon, _ := statusIcon(&RoleView{ReadyToClose: true, HasStatus: true, Status: db.HeraStatusWorking}, false, 0)
	testutil.Equal(t, icon, theme.IconReview)
}

func TestStatusIcon_StatusMapping(t *testing.T) {
	cases := []struct {
		status db.HeraRoleStatusValue
	}{
		{db.HeraStatusWorking},
		{db.HeraStatusBlocked},
		{db.HeraStatusDone},
		{db.HeraStatusIdle},
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
	active := &RoleView{Live: true, TaskStatus: "in_progress", HasStatus: true, Status: db.HeraStatusWorking}
	f0, _ := statusIcon(active, false, 0)
	f1, _ := statusIcon(active, false, 1)
	testutil.Equal(t, f0, widget.SpinnerFrame(0))
	testutil.Equal(t, f1, widget.SpinnerFrame(1))
	if f0 == f1 {
		t.Error("active glyph did not advance between frames")
	}

	// Real activity drives the spinner even when the stale role-status disagrees
	// (here it claims idle): the bound task is genuinely in_progress.
	activeStaleStatus := &RoleView{Live: true, TaskStatus: "in_progress", HasStatus: true, Status: db.HeraStatusIdle}
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

	// A live role whose bound task already left in_progress (e.g. auto-completed
	// coordinator) is NOT producing → static, even with stale Status==working.
	staleLiveDone := &RoleView{Live: true, TaskStatus: "in_review", HasStatus: true, Status: db.HeraStatusWorking}
	d0, _ := statusIcon(staleLiveDone, false, 0)
	d1, _ := statusIcon(staleLiveDone, false, 3)
	testutil.Equal(t, d0, d1)
	if d0 == widget.SpinnerFrame(0) {
		t.Error("live-but-not-in_progress role animated the spinner (BUG-003 regression)")
	}

	// A non-active (idle) role is static across frames.
	idle := &RoleView{HasStatus: true, Status: db.HeraStatusIdle}
	i0, _ := statusIcon(idle, false, 0)
	i1, _ := statusIcon(idle, false, 5)
	testutil.Equal(t, i0, i1)
}

// TestRoleView_IsActive isolates the activity predicate that sources the spinner.
func TestRoleView_IsActive(t *testing.T) {
	cases := []struct {
		name string
		role RoleView
		want bool
	}{
		{"live in_progress", RoleView{Live: true, TaskStatus: "in_progress"}, true},
		{"live in_review", RoleView{Live: true, TaskStatus: "in_review"}, false},
		{"live complete", RoleView{Live: true, TaskStatus: "complete"}, false},
		{"live no task snapshot", RoleView{Live: true, TaskStatus: ""}, false},
		{"not live but in_progress task", RoleView{Live: false, TaskStatus: "in_progress"}, false},
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
