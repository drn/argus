package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

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

func TestModel_SubtreeArchivedBridges(t *testing.T) {
	// Parent orchestrator with an ARCHIVED worker that bridges child orch 2 (its
	// BridgeTaskID matches child 2's coordinator bridge task), plus an ordinary
	// (non-bridging) archived worker and a LIVE bridging worker to child 3 (not
	// archived, so it must not surface here — Ctrl+D already handles a live one).
	m := Model{Active: []OrchView{
		{
			ID:   1,
			Name: "parent",
			Roles: []RoleView{
				{RoleID: 10, OrchID: 1, Kind: db.HeraKindCoordinator},
				{RoleID: 11, OrchID: 1, Kind: db.HeraKindWorker, Archived: true, TaskID: "t-child", BridgeTaskID: "t-child"},
				{RoleID: 12, OrchID: 1, Kind: db.HeraKindWorker, Archived: true, TaskID: "t-leaf", BridgeTaskID: "t-leaf"},
				{RoleID: 13, OrchID: 1, Kind: db.HeraKindWorker, Live: true, TaskID: "t-live-child", BridgeTaskID: "t-live-child"},
			},
		},
		{
			ID:   2,
			Name: "child",
			Roles: []RoleView{
				{RoleID: 20, OrchID: 2, Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-child", BridgeTaskID: "t-child"},
			},
		},
		{
			ID:   3,
			Name: "live-child",
			Roles: []RoleView{
				{RoleID: 30, OrchID: 3, Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-live-child", BridgeTaskID: "t-live-child"},
			},
		},
	}}

	got := m.SubtreeArchivedBridges(1)
	testutil.DeepEqual(t, got, []int64{2})

	// Unknown orchestrator → empty.
	testutil.Equal(t, len(m.SubtreeArchivedBridges(999)), 0)
}
