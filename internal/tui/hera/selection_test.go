package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func sampleModel() Model {
	return Model{
		Active: []OrchView{
			{ID: 1, Name: "a", Roles: []RoleView{
				{RoleID: 10, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-c1"},
				{RoleID: 11, OrchID: 1, Name: "wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "t-w1"},
			}},
		},
		Pinned: []OrchView{
			{ID: 2, Name: "b", Roles: []RoleView{
				{RoleID: 20, OrchID: 2, Name: "coord", Kind: db.HeraKindCoordinator, Live: false, TaskID: ""},
			}},
		},
	}
}

func TestModel_OrchByID(t *testing.T) {
	m := sampleModel()
	testutil.Equal(t, m.OrchByID(1).Name, "a")
	testutil.Equal(t, m.OrchByID(2).Name, "b")
	testutil.Nil(t, m.OrchByID(999))
}

func TestOrchView_CoordTaskID(t *testing.T) {
	m := sampleModel()
	testutil.Equal(t, m.OrchByID(1).CoordTaskID(), "t-c1") // live coordinator
	testutil.Equal(t, m.OrchByID(2).CoordTaskID(), "")     // coordinator not live
}

func TestSelection_Accessors(t *testing.T) {
	m := sampleModel()
	coord := &m.Active[0].Roles[0]
	wkr := &m.Active[0].Roles[1]
	orch := &m.Active[0]

	cs := Selection{Role: coord, Orch: orch}
	testutil.Equal(t, cs.IsCoordinator(), true)
	testutil.Equal(t, cs.TaskID(), "t-c1")
	testutil.Equal(t, cs.CoordTaskID(), "t-c1")

	ws := Selection{Role: wkr, Orch: orch}
	testutil.Equal(t, ws.IsCoordinator(), false)
	testutil.Equal(t, ws.TaskID(), "t-w1")
	testutil.Equal(t, ws.CoordTaskID(), "t-c1") // worker selection still resolves the coordinator

	empty := Selection{}
	testutil.Equal(t, empty.TaskID(), "")
	testutil.Equal(t, empty.IsCoordinator(), false)
	testutil.Equal(t, empty.CoordTaskID(), "")
}

// BUG-014: StatusRole resolves the role the s/S keys step — the selected role,
// else the orchestrator's folded coordinator, else nil.
func TestSelection_StatusRole(t *testing.T) {
	m := sampleModel()
	coord := &m.Active[0].Roles[0]
	wkr := &m.Active[0].Roles[1]
	orch := &m.Active[0]

	// Explicit role selection → that role.
	testutil.Equal(t, Selection{Role: wkr, Orch: orch}.StatusRole().RoleID, int64(11))

	// Header selection (Role nil) → the orchestrator's folded coordinator.
	testutil.Equal(t, Selection{Orch: orch}.StatusRole().RoleID, int64(10))
	_ = coord

	// Header over a coordinator-less orchestrator → nil.
	noCoord := &OrchView{ID: 9, Name: "x", Roles: []RoleView{{RoleID: 90, Kind: db.HeraKindWorker}}}
	testutil.Nil(t, Selection{Orch: noCoord}.StatusRole())

	// Empty selection → nil.
	testutil.Nil(t, Selection{}.StatusRole())
}

func TestRail_SelectionResolvesOrch(t *testing.T) {
	r := NewRail()
	r.SetModel(sampleModel())
	// Cursor starts on the first selectable row — the pinned orchestrator "b".
	sel := r.Selection()
	testutil.Equal(t, sel.Orch != nil, true)

	// Step to the worker role (under active orchestrator "a"); Selection
	// resolves its containing orchestrator from OrchID even though the cursor is
	// on a role row, not a header.
	testutil.Equal(t, selectRailRole(r, "wkr"), true)
	sel = r.Selection()
	testutil.Equal(t, sel.Role.Name, "wkr")
	testutil.Equal(t, sel.Orch.ID, int64(1))
	testutil.Equal(t, sel.CoordTaskID(), "t-c1")
}

// selectRailRole steps the rail cursor to a role by name (test-local).
func selectRailRole(r *Rail, name string) bool {
	for i := 0; i < r.Rows()+1; i++ {
		if sel := r.Selected(); sel != nil && sel.Name == name {
			return true
		}
		before := r.CursorIndex()
		r.CursorDown()
		if r.CursorIndex() == before {
			return false
		}
	}
	return false
}
