package hera

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
)

// memDB opens an in-memory db.DB for hera-store seeding. NEVER touches
// ~/.argus or the live daemon.
func memDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// seedOrch creates an active orchestrator and returns its id.
func seedOrch(t *testing.T, d *db.DB, name string) int64 {
	t.Helper()
	o, err := d.CreateHeraOrchestrator(name)
	testutil.NoError(t, err)
	return o.ID
}

// seedRole creates a role + binds it to taskID (Add'ing the task first so
// task_meta FK constraints are satisfied). Returns the role.
func seedBoundRole(t *testing.T, d *db.DB, orchID int64, name string, kind db.HeraRoleKind, taskID string) *db.HeraRole {
	t.Helper()
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, Kind: kind, ArgusProject: "p",
	})
	testutil.NoError(t, err)
	if taskID != "" {
		testutil.NoError(t, d.Add(&model.Task{ID: taskID, Name: taskID, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{
			RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID,
		})
		testutil.NoError(t, err)
	}
	return role
}

func TestBuildModel_NilReaderEmpty(t *testing.T) {
	m, err := BuildModel(nil)
	testutil.NoError(t, err)
	testutil.Equal(t, m.IsEmpty(), true)
}

func TestBuildModel_PartitionsSections(t *testing.T) {
	d := memDB(t)
	active := seedOrch(t, d, "active-orch")
	seedBoundRole(t, d, active, "coord", db.HeraKindCoordinator, "t-active")

	pinnedID := seedOrch(t, d, "pinned-orch")
	testutil.NoError(t, d.PinHeraOrchestrator(pinnedID))

	archID := seedOrch(t, d, "arch-orch")
	testutil.NoError(t, d.ArchiveHeraOrchestrator(archID))

	m, err := BuildModel(d)
	testutil.NoError(t, err)
	testutil.Equal(t, len(m.Active), 1)
	testutil.Equal(t, len(m.Pinned), 1)
	testutil.Equal(t, len(m.Archived), 1)
	testutil.Equal(t, m.Active[0].Name, "active-orch")
	testutil.Equal(t, m.Pinned[0].Name, "pinned-orch")
	testutil.Equal(t, m.Archived[0].Name, "arch-orch")
}

// The locked must-have: a single argus task bound under TWO orchestrators
// surfaces under EACH of them in the model (via two distinct roles).
func TestBuildModel_MultiBindingFanOut(t *testing.T) {
	d := memDB(t)
	orchA := seedOrch(t, d, "orch-a")
	orchB := seedOrch(t, d, "orch-b")

	// One task, bound as a worker in A and a coordinator in B.
	const sharedTask = "shared-task"
	roleA, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchA, Name: "wkr", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	roleB, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchB, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	testutil.NoError(t, d.Add(&model.Task{ID: sharedTask, Name: sharedTask, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: roleA.ID, ArgusTaskID: sharedTask, WorktreePath: "/wt/a"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: roleB.ID, ArgusTaskID: sharedTask, WorktreePath: "/wt/b"})
	testutil.NoError(t, err)

	m, err := BuildModel(d)
	testutil.NoError(t, err)
	testutil.Equal(t, len(m.Active), 2)

	// The shared task appears once under each orchestrator's roles.
	count := 0
	for _, o := range m.Active {
		for _, r := range o.Roles {
			if r.TaskID == sharedTask {
				count++
				testutil.Equal(t, r.Live, true)
			}
		}
	}
	testutil.Equal(t, count, 2)
}

func TestBuildModel_FreelanceHoisted(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "free", db.HeraKindFreelance, "t-free")

	m, err := BuildModel(d)
	testutil.NoError(t, err)
	// Coordinator stays under the orchestrator; freelance hoists out.
	testutil.Equal(t, len(m.Active[0].Roles), 1)
	testutil.Equal(t, m.Active[0].Roles[0].Kind, db.HeraKindCoordinator)
	testutil.Equal(t, len(m.Freelance), 1)
	testutil.Equal(t, m.Freelance[0].Name, "free")
}

func TestBuildModel_ReadyToCloseAndStatus(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	role := seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-rc")
	// Stamp ready_to_close on the bound task + a working status on the role.
	testutil.NoError(t, d.SetMeta("t-rc", db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusWorking))

	m, err := BuildModel(d)
	testutil.NoError(t, err)
	rv := m.Active[0].Roles[0]
	testutil.Equal(t, rv.ReadyToClose, true)
	testutil.Equal(t, rv.HasStatus, true)
	testutil.Equal(t, rv.Status, db.HeraStatusWorking)
}

func TestBuildModel_BridgeTaskID(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")

	t.Run("live role: bridge equals live task", func(t *testing.T) {
		role := seedBoundRole(t, d, orch, "live", db.HeraKindWorker, "t-live")
		_ = role
		m, err := BuildModel(d)
		testutil.NoError(t, err)
		var rv *RoleView
		for i := range m.Active[0].Roles {
			if m.Active[0].Roles[i].Name == "live" {
				rv = &m.Active[0].Roles[i]
			}
		}
		testutil.Equal(t, rv != nil, true)
		testutil.Equal(t, rv.BridgeTaskID, "t-live")
		testutil.Equal(t, rv.LinkEndReason, "")
	})

	t.Run("ended role: bridge is latest task + end_reason, not live", func(t *testing.T) {
		role := seedBoundRole(t, d, orch, "ended", db.HeraKindWorker, "t-ended")
		bnd, err := d.HeraLiveBindingByRole(role.ID)
		testutil.NoError(t, err)
		testutil.NoError(t, d.EndHeraBinding(bnd.ID, db.HeraEndReasonUserDeleted))

		m, err := BuildModel(d)
		testutil.NoError(t, err)
		var rv *RoleView
		for i := range m.Active[0].Roles {
			if m.Active[0].Roles[i].Name == "ended" {
				rv = &m.Active[0].Roles[i]
			}
		}
		testutil.Equal(t, rv != nil, true)
		testutil.Equal(t, rv.Live, false)
		testutil.Equal(t, rv.TaskID, "")              // no live binding
		testutil.Equal(t, rv.BridgeTaskID, "t-ended") // latest binding still bridges
		testutil.Equal(t, rv.LinkEndReason, db.HeraEndReasonUserDeleted)
	})
}

// errReader returns an error from ListHeraOrchestrators to prove BuildModel
// surfaces read errors rather than swallowing them.
type errReader struct{ HeraReader }

func (errReader) ListHeraOrchestrators(bool) ([]*db.HeraOrchestrator, error) {
	return nil, errors.New("boom")
}

func TestBuildModel_PropagatesReadError(t *testing.T) {
	_, err := BuildModel(errReader{})
	testutil.Contains(t, errString(err), "boom")
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
