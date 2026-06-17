package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// idsContain reports whether ids contains want.
func idsContain(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// seedHeraOrch creates an active orchestrator on the test DB.
func seedHeraOrch(t *testing.T, d *db.DB, name string) int64 {
	t.Helper()
	o, err := d.CreateHeraOrchestrator(name)
	testutil.NoError(t, err)
	return o.ID
}

// seedHeraBoundRole creates a role and binds it to taskID (Add'ing the task).
func seedHeraBoundRole(t *testing.T, d *db.DB, orchID int64, name string, kind db.HeraRoleKind, taskID string) *db.HeraRole {
	t.Helper()
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchID, Name: name, Kind: kind, ArgusProject: "p"})
	testutil.NoError(t, err)
	testutil.NoError(t, d.Add(&model.Task{ID: taskID, Name: taskID, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: "/wt/" + taskID})
	testutil.NoError(t, err)
	return role
}

func TestSmoke_HeraTabRendersRailFromStore(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "my-orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0) // → Hera tab
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
		m := app.heraPage.Rail().Model()
		testutil.Equal(t, len(m.Active), 1)
		testutil.Equal(t, m.Active[0].Name, "my-orch")
		testutil.Equal(t, len(m.Active[0].Roles), 1)
	})
}

// The locked must-have, end-to-end: a task bound under two orchestrators
// renders under EACH in the rail.
func TestSmoke_HeraRailMultiBindingUnderTwoOrchestrators(t *testing.T) {
	d := testDB(t)
	orchA := seedHeraOrch(t, d, "orch-a")
	orchB := seedHeraOrch(t, d, "orch-b")
	const shared = "shared"
	rA, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchA, Name: "wkr", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	rB, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchB, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	testutil.NoError(t, d.Add(&model.Task{ID: shared, Name: shared, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: rA.ID, ArgusTaskID: shared, WorktreePath: "/a"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: rB.ID, ArgusTaskID: shared, WorktreePath: "/b"})
	testutil.NoError(t, err)

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		m := app.heraPage.Rail().Model()
		testutil.Equal(t, len(m.Active), 2)
		count := 0
		for _, o := range m.Active {
			for _, r := range o.Roles {
				if r.TaskID == shared {
					count++
				}
			}
		}
		testutil.Equal(t, count, 2) // appears under both orchestrators
	})
}

func TestSmoke_HeraRailCursorNavAndCollapse(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t1")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t2")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	// First-run (no saved state): the orchestrator starts fully collapsed — only
	// the header row is visible. Space expands it to show the worker.
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.Rail().Rows(), 1)        // collapsed: only header
		testutil.Equal(t, app.heraPage.Rail().CursorIndex(), 0) // cursor on header
		testutil.Equal(t, app.heraPage.Rail().OrchCollapsed(orch), true)
	})

	sim.InjectKey(tcell.KeyRune, ' ', 0) // expand
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.Rail().Rows(), 2) // header + worker
		testutil.Equal(t, app.heraPage.Rail().OrchCollapsed(orch), false)
	})

	sim.InjectKey(tcell.KeyRune, 'j', 0) // cursor down to a role
	syncUI(t, app.tapp)
	var cursorAfterJ int
	readUI(t, app.tapp, func() { cursorAfterJ = app.heraPage.Rail().CursorIndex() })
	testutil.Equal(t, cursorAfterJ, 1)

	// Cursor back to the orch header, then Space to collapse → roles vanish.
	sim.InjectKey(tcell.KeyRune, 'k', 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, ' ', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.heraPage.Rail().Rows(), 1) // only the collapsed orch header
	})
}

func TestSmoke_HeraReadyToCloseRendersInRail(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	testutil.NoError(t, d.SetMeta("tw", db.HeraMetaNamespace, db.HeraMetaKeyReadyToClose, "true"))
	testutil.NoError(t, d.UpsertHeraRoleStatus(role.ID, db.HeraStatusDone))

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		m := app.heraPage.Rail().Model()
		testutil.Equal(t, m.Active[0].Roles[0].ReadyToClose, true)
	})
}

func TestSmoke_HideHeraWorkersInTasksTab(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.Add(&model.Task{ID: "normal", Name: "normal", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	testutil.NoError(t, d.Add(&model.Task{ID: "worker", Name: "worker", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	// Mark the worker task as a hera-spawned worker.
	testutil.NoError(t, d.SetMeta("worker", db.HeraMetaNamespace, db.HeraMetaKeyRole, string(db.HeraKindWorker)))

	app := New(d, agent.NewRunner(nil), false)
	app.refreshTasks() // feeds SetHeraWorkers + SetHeraCoordinators from the "hera" meta namespace
	sim, stop := wireApp(t, app)
	defer stop()

	// Default: the worker is hidden from the Tasks tab.
	readUI(t, app.tapp, func() {
		vis := app.tasklist.VisibleTaskIDs()
		testutil.Equal(t, idsContain(vis, "normal"), true)
		testutil.Equal(t, idsContain(vis, "worker"), false)
		testutil.Equal(t, app.tasklist.HideHeraWorkers(), true)
	})

	// `H` reveals them inline.
	sim.InjectKey(tcell.KeyRune, 'H', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.tasklist.HideHeraWorkers(), false)
		testutil.Equal(t, idsContain(app.tasklist.VisibleTaskIDs(), "worker"), true)
	})
}

// TestSmoke_HeraRemoteModeBanner proves the Hera tab degrades gracefully when
// the store is not a local *db.DB (remote mode): the page is remote, renders
// the unavailable banner, and never panics.
func TestSmoke_HeraRemoteModeBanner(t *testing.T) {
	app := New(stubStore{}, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
		testutil.Equal(t, app.heraPage.IsRemote(), true)
		page, _ := app.pages.GetFrontPage()
		testutil.Equal(t, page, "hera")
	})
}

// TestSmoke_HeraTabSelectionThreadsThroughRunner is the App-level integration
// for M6b: switching to the Hera tab builds the rail from the local db, and
// navigating to a worker role drives applySelection, which calls the wired
// runner.Get resolver (nil here since no session is started — the pane binds in
// replay mode) and threads the (role, orchestrator, task) selection context for
// 6c. The page draws the real coord/agent panes without panicking. Live-session
// feed semantics are covered by the internal/tui/hera package tests.
func TestSmoke_HeraTabSelectionThreadsThroughRunner(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedHeraBoundRole(t, d, orch, "worker", db.HeraKindWorker, "t-worker")

	app := New(d, agent.NewRunner(nil), true)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0) // → Hera tab
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		if app.heraPage.Rail().Model().IsEmpty() {
			t.Error("hera rail empty after tab entry")
		}
	})

	// Navigate: orch header (row 0, the folded coordinator) → worker role (1).
	// First run starts collapsed — expand the orch before navigating down.
	var collapsed bool
	readUI(t, app.tapp, func() {
		if o := app.heraPage.Rail().SelectedOrch(); o != nil {
			collapsed = app.heraPage.Rail().OrchCollapsed(o.ID)
		}
	})
	if collapsed {
		sim.InjectKey(tcell.KeyRune, ' ', 0)
		syncUI(t, app.tapp)
	}
	sim.InjectKey(tcell.KeyRune, 'j', 0)
	syncUI(t, app.tapp)
	var sel hera.Selection
	readUI(t, app.tapp, func() { sel = app.heraPage.SelectionContext() })
	testutil.Equal(t, sel.TaskID(), "t-worker")
	testutil.Equal(t, sel.IsCoordinator(), false)
	testutil.Equal(t, sel.CoordTaskID(), "t-coord")
}
