package hera

import (
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

	// The coordinator's blocked glyph must appear somewhere on the header row.
	wantGlyph, _ := statusIcon(&RoleView{HasStatus: true, Status: db.HeraStatusBlocked, Live: true}, false, 0)
	found := false
	for x := 0; x < 40; x++ {
		primary, _, _, _ := sim.GetContent(x, 1) // row 1 = first content row inside the border
		if primary == wantGlyph {
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
	found := false
	for y := 0; y < 10 && !found; y++ {
		for x := 0; x+1 < 40; x++ {
			a, _, _, _ := sim.GetContent(x, y)
			b, _, _, _ := sim.GetContent(x+1, y)
			if a == 'P' && b == 'R' {
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

func TestStatusIcon_WorkingAnimatesSpinner(t *testing.T) {
	widget.SetActiveSpinner("progress")
	defer widget.SetActiveSpinner("progress")
	working := &RoleView{HasStatus: true, Status: db.HeraStatusWorking}

	// A working role's glyph is the active spinner's frame and advances with the
	// frame counter (distinct frames produce distinct glyphs).
	f0, _ := statusIcon(working, false, 0)
	f1, _ := statusIcon(working, false, 1)
	testutil.Equal(t, f0, widget.SpinnerFrame(0))
	testutil.Equal(t, f1, widget.SpinnerFrame(1))
	if f0 == f1 {
		t.Error("working glyph did not advance between frames")
	}

	// A non-working (idle) role is static across frames.
	idle := &RoleView{HasStatus: true, Status: db.HeraStatusIdle}
	i0, _ := statusIcon(idle, false, 0)
	i1, _ := statusIcon(idle, false, 5)
	testutil.Equal(t, i0, i1)
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
