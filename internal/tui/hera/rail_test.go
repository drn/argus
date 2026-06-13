package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/theme"
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

	// 2 orch headers + 3 roles = 5 selectable rows, all selectable here.
	testutil.Equal(t, r.Rows(), 5)

	// Cursor starts at 0 (first orch header).
	testutil.Equal(t, r.CursorIndex(), 0)
	testutil.Equal(t, r.SelectedOrch().Name, "orch-1")

	r.CursorDown() // → role coord
	testutil.Equal(t, r.Selected().Name, "coord")
	r.CursorDown() // → role wkr
	testutil.Equal(t, r.Selected().Name, "wkr")
	r.CursorDown() // → orch-2 header
	testutil.Equal(t, r.SelectedOrch().Name, "orch-2")
	r.CursorDown() // → coord2
	testutil.Equal(t, r.Selected().Name, "coord2")
	r.CursorDown() // at bottom, no move
	testutil.Equal(t, r.Selected().Name, "coord2")

	r.CursorUp()
	testutil.Equal(t, r.SelectedOrch().Name, "orch-2")
}

func TestRail_ToggleCollapseHidesRoles(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.Rows(), 5)

	// Cursor on orch-1 header; collapse it → its 2 roles vanish.
	r.ToggleCollapse()
	testutil.Equal(t, r.Rows(), 3) // orch-1 (collapsed) + orch-2 + coord2

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
	r.CursorDown()
	r.CursorDown()
	testutil.Equal(t, r.Selected().Name, "wkr")

	// Rebuild with the same model — cursor should stay on role 12 (wkr).
	r.SetModel(twoOrchModel())
	testutil.Equal(t, r.Selected().Name, "wkr")
}

func TestStatusIcon_ReadyToCloseWins(t *testing.T) {
	// ready_to_close overrides the role status with the distinct review mark.
	icon, _ := statusIcon(&RoleView{ReadyToClose: true, HasStatus: true, Status: db.HeraStatusWorking}, false)
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
		icon, _ := statusIcon(&RoleView{HasStatus: true, Status: c.status}, false)
		if icon == 0 {
			t.Errorf("status %q produced zero glyph", c.status)
		}
	}
	// No status, no binding → falls back to a dimmed moon.
	icon, _ := statusIcon(&RoleView{}, false)
	if icon == 0 {
		t.Error("fallback produced zero glyph")
	}
	// Bound but statusless → distinct glyph.
	icon2, _ := statusIcon(&RoleView{Live: true}, false)
	if icon2 == 0 {
		t.Error("live-statusless produced zero glyph")
	}
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
