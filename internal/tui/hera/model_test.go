package hera

import (
	"errors"
	"testing"
	"time"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/widget"
)

// Shared hera-package test builders. These mirror (deliberately duplicated,
// not shared via import) the identically-named builders in
// internal/hera/model/model_test.go — that package's own white-box tests of
// BuildModel's internals need them too, but a _test.go file can't be shared
// across packages, and promoting them to non-test exported API just for test
// convenience isn't worth polluting the model package's real surface. Used
// throughout this package's test suite (rail_test.go, plan_test.go, and many
// more), not just this file.

// memDB opens an in-memory db.DB for hera-store seeding. NEVER touches
// ~/.argus or the live daemon.
func memDB(t *testing.T) *db.DB {
	t.Helper()
	d, err := db.OpenInMemory()
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = d.Close() })
	return d
}

// orchView is a tiny builder for a single orchestrator: the first (coord, task)
// pair is the coordinator, the rest are live workers. (Relocated from the
// retired tree_test.go; still used by the filter/model in-memory tests.)
func orchView(id int64, name, coordTask string, workers ...struct{ name, task string }) heramodel.OrchView {
	o := heramodel.OrchView{ID: id, Name: name}
	if coordTask != "" {
		o.Roles = append(o.Roles, heramodel.RoleView{
			Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: coordTask,
			TaskStatus: "in_progress", SessionRunning: true,
		})
	}
	for _, w := range workers {
		o.Roles = append(o.Roles, heramodel.RoleView{
			Name: w.name, Kind: db.HeraKindWorker, Live: true, TaskID: w.task,
			TaskStatus: "in_progress", SessionRunning: true,
		})
	}
	return o
}

func wk(name, task string) struct{ name, task string } {
	return struct{ name, task string }{name, task}
}

// seedOrch creates an active orchestrator and returns its id.
func seedOrch(t *testing.T, d *db.DB, name string) int64 {
	t.Helper()
	o, err := d.CreateHeraOrchestrator(name, "")
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

// coordOf builds an orchestrator whose coordinator role has an explicit RoleID
// and bridge task (the coord-of-both fixtures need distinct coordinator role ids
// to exercise the earliest-id=parent rule).
func coordOf(id int64, name string, coordRoleID int64, coordTask string, workers ...heramodel.RoleView) heramodel.OrchView {
	o := heramodel.OrchView{ID: id, Name: name, Roles: []heramodel.RoleView{
		{RoleID: coordRoleID, OrchID: id, Name: "coord", Kind: db.HeraKindCoordinator,
			Live: true, TaskID: coordTask, BridgeTaskID: coordTask},
	}}
	for i := range workers {
		workers[i].OrchID = id
		o.Roles = append(o.Roles, workers[i])
	}
	return o
}

// coordSubtreeNI returns the SubtreeNeedsInput flag of the orchestrator's
// folded coordinator role (the glyph the rail header projects). Fails the test
// if the orchestrator or its coordinator is missing.
func coordSubtreeNI(t *testing.T, m *heramodel.Model, orchID int64) bool {
	t.Helper()
	o := m.OrchByID(orchID)
	if o == nil {
		t.Fatalf("orchestrator %d not found", orchID)
	}
	c := o.CoordRole()
	if c == nil {
		t.Fatalf("orchestrator %d has no coordinator role", orchID)
	}
	return c.SubtreeNeedsInput
}

// roleByName returns a pointer to the named role within an orchestrator.
func roleByName(t *testing.T, m *heramodel.Model, orchID int64, name string) *heramodel.RoleView {
	t.Helper()
	o := m.OrchByID(orchID)
	if o == nil {
		t.Fatalf("orchestrator %d not found", orchID)
	}
	for i := range o.Roles {
		if o.Roles[i].Name == name {
			return &o.Roles[i]
		}
	}
	t.Fatalf("role %q not found under orchestrator %d", name, orchID)
	return nil
}

// errReader returns an error from ListHeraOrchestrators to prove BuildModel
// surfaces read errors rather than swallowing them. Also used by page_test.go
// as a reader that satisfies heramodel.HeraReader without a real store.
type errReader struct{ heramodel.HeraReader }

func (errReader) ListHeraOrchestrators(bool) ([]*db.HeraOrchestrator, error) {
	return nil, errors.New("boom")
}

// TestBuildModel_PopulatesDetailsFields proves the additive coordinator-Details
// projection inputs (orch + role creation, the live binding's worktree + start,
// the role-status update time, and the bound task name) flow into the model.
func TestBuildModel_PopulatesDetailsFields(t *testing.T) {
	d := memDB(t)
	orchID := seedOrch(t, d, "orch")
	role := seedBoundRole(t, d, orchID, "coord", db.HeraKindCoordinator, "t-c")
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusWorking))

	m, err := heramodel.BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	ov := m.Active[0]
	testutil.Equal(t, ov.CreatedAt.IsZero(), false)
	rv := ov.Roles[0]
	testutil.Equal(t, rv.CreatedAt.IsZero(), false)
	testutil.Equal(t, rv.ArgusProject, "p")
	testutil.Equal(t, rv.WorktreePath, "/wt/t-c")
	testutil.Equal(t, rv.BindingStartedAt.IsZero(), false)
	testutil.Equal(t, rv.StatusUpdatedAt.IsZero(), false)
	testutil.Equal(t, rv.TaskName, "t-c")

	// Derived metadata over the real projection: repos-in-scope is the role's
	// project and last activity is at/after the orchestrator creation.
	meta := deriveCoordMeta(&ov)
	testutil.DeepEqual(t, meta.Repos, []string{"p"})
	testutil.Equal(t, meta.AgentName, "t-c")
	testutil.Equal(t, meta.Worktree, "/wt/t-c")
	testutil.Equal(t, meta.LastActivity.Before(ov.CreatedAt), false)
}

// TestBuildModel_SessionIdleSuppressesSpinner is the BUG-036 headline at the
// BuildModel seam: a live in_progress role normally counts as active (spins), but
// when the App's content-aware idle set marks its bound session idle (a parked
// fullscreen agent), the role is NOT active and renders a static glyph instead.
func TestBuildModel_SessionIdleSuppressesSpinner(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	running := map[string]bool{"t-coord": true, "t-wkr": true}

	// No content-idle: a live in_progress worker with a running session spins.
	m, err := heramodel.BuildModel(d, nil, nil, running, nil)
	testutil.NoError(t, err)
	wkr := roleByName(t, &m, orch, "wkr")
	testutil.Equal(t, wkr.SessionIdle, false)
	testutil.Equal(t, wkr.IsActive(), true)

	// Content-idle for the worker's task → SessionIdle stamped, IsActive false.
	m2, err := heramodel.BuildModel(d, nil, map[string]bool{"t-wkr": true}, running, nil)
	testutil.NoError(t, err)
	wkr2 := roleByName(t, &m2, orch, "wkr")
	testutil.Equal(t, wkr2.SessionIdle, true)
	testutil.Equal(t, wkr2.IsActive(), false)
	// The status glyph is no longer the animated spinner frame.
	glyph, _ := statusIcon(wkr2, false, 0)
	if glyph == widget.SpinnerFrame(0) {
		t.Error("content-idle role should not render the active spinner glyph")
	}
}
