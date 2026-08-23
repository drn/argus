package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
)

func TestModel_SubtreeArchivedWorkers(t *testing.T) {
	// One orchestrator with: a coordinator (excluded), an archived worker
	// (included), a live worker (excluded), and an archived freelance (excluded).
	m := heramodel.Model{Active: []heramodel.OrchView{{
		ID:   1,
		Name: "orch",
		Roles: []heramodel.RoleView{
			{RoleID: 10, OrchID: 1, Kind: db.HeraKindCoordinator, Archived: true},
			{RoleID: 11, OrchID: 1, Kind: db.HeraKindWorker, Archived: true, TaskID: "t-arch", BridgeTaskID: "t-arch"},
			{RoleID: 12, OrchID: 1, Kind: db.HeraKindWorker},
			{RoleID: 13, OrchID: 1, Kind: db.HeraKindFreelance, Archived: true},
		},
	}}}

	got := SubtreeArchivedWorkers(m, 1)
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].RoleID, int64(11))

	// Unknown orchestrator → empty.
	testutil.Equal(t, len(SubtreeArchivedWorkers(m, 999)), 0)
}
