package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

func TestRoleView_IsFinished(t *testing.T) {
	cases := []struct {
		name string
		r    RoleView
		want bool
	}{
		{"archived", RoleView{Archived: true}, true},
		{"status done", RoleView{HasStatus: true, Status: db.HeraStatusDone}, true},
		{"ready to close", RoleView{ReadyToClose: true}, true},
		{"live in progress", RoleView{Live: true, TaskStatus: "in_progress"}, false},
		{"working not done", RoleView{HasStatus: true, Status: db.HeraStatusWorking}, false},
		{"plain unbound", RoleView{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			testutil.Equal(t, c.r.IsFinished(), c.want)
		})
	}
}

func TestModel_SubtreeArchivedWorkers(t *testing.T) {
	// One orchestrator with: a coordinator (excluded), an archived worker
	// (included), a live worker (excluded), and an archived freelance (excluded).
	m := Model{Active: []OrchView{{
		ID:   1,
		Name: "orch",
		Roles: []RoleView{
			{RoleID: 10, OrchID: 1, Kind: db.HeraKindCoordinator, Archived: true},
			{RoleID: 11, OrchID: 1, Kind: db.HeraKindWorker, Archived: true, TaskID: "t-arch", BridgeTaskID: "t-arch"},
			{RoleID: 12, OrchID: 1, Kind: db.HeraKindWorker},
			{RoleID: 13, OrchID: 1, Kind: db.HeraKindFreelance, Archived: true},
		},
	}}}

	got := m.SubtreeArchivedWorkers(1)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].RoleID, int64(11))

	// Unknown orchestrator → empty.
	testutil.Equal(t, len(m.SubtreeArchivedWorkers(999)), 0)
}

func TestModel_FinishedRoles(t *testing.T) {
	m := Model{
		Active: []OrchView{{
			ID: 1, Name: "a", Roles: []RoleView{
				{RoleID: 1, Kind: db.HeraKindCoordinator, HasStatus: true, Status: db.HeraStatusDone},
				{RoleID: 2, Kind: db.HeraKindWorker, Live: true, TaskStatus: "in_progress"},
			},
		}},
		Archived: []OrchView{{
			ID: 2, Name: "b", Roles: []RoleView{
				{RoleID: 3, Kind: db.HeraKindWorker, Archived: true},
			},
		}},
		Freelance: []RoleView{
			{RoleID: 4, Kind: db.HeraKindFreelance, ReadyToClose: true},
			{RoleID: 5, Kind: db.HeraKindFreelance},
		},
	}
	got := m.FinishedRoles()
	// roles 1 (done), 3 (archived), 4 (ready_to_close); NOT 2 (live) or 5 (plain).
	testutil.Equal(t, len(got), 3)
	ids := map[int64]bool{}
	for _, r := range got {
		ids[r.RoleID] = true
	}
	testutil.Equal(t, ids[1] && ids[3] && ids[4], true)
	testutil.Equal(t, ids[2] || ids[5], false)
}

func TestModel_FullyFinishedOrchestratorIDs(t *testing.T) {
	m := Model{Active: []OrchView{
		{ID: 1, Name: "all-done", Roles: []RoleView{
			{RoleID: 1, Kind: db.HeraKindCoordinator, HasStatus: true, Status: db.HeraStatusDone},
			{RoleID: 2, Kind: db.HeraKindWorker, Archived: true},
		}},
		{ID: 2, Name: "has-live", Roles: []RoleView{
			{RoleID: 3, Kind: db.HeraKindCoordinator, Live: true, TaskStatus: "in_progress"},
		}},
		{ID: 3, Name: "empty"}, // no roles → not reported
	}}
	got := m.FullyFinishedOrchestratorIDs()
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0], int64(1))
}
