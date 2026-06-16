package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// heraTabCursorOnWorker switches to the Hera tab and moves the cursor onto the
// worker role. After the coordinator fold the rows are orch header=0 (the
// coordinator) and worker=1, so a single Down lands on the worker.
func heraTabCursorOnWorker(t *testing.T, app *App, sim tcell.SimulationScreen) {
	t.Helper()
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, 'j', 0) // → worker (coord folded into header)
	syncUI(t, app.tapp)
}

func TestSmoke_HeraPinKeyTogglesStore(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	sim.InjectKey(tcell.KeyRune, 'P', 0) // pin
	syncUI(t, app.tapp)
	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.PinnedAt != nil, true)
}

func TestSmoke_HeraStatusKeyAdvances(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	sim.InjectKey(tcell.KeyRune, 's', 0) // advance idle→working
	syncUI(t, app.tapp)
	st, err := d.HeraRoleStatusFor(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, st.Status, db.HeraStatusWorking)
}

func TestSmoke_HeraArchiveLiveConfirmGate(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	// `a` on a LIVE role opens the confirm modal — no write yet.
	sim.InjectKey(tcell.KeyRune, 'a', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraConfirm) })
	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.ArchivedAt == nil, true) // not archived yet

	// Cancel → no-op.
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	got, _ = d.HeraRole(role.ID)
	testutil.Equal(t, got.ArchivedAt == nil, true)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })

	// `a` again, confirm with `y` → archived.
	sim.InjectKey(tcell.KeyRune, 'a', 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, 'y', 0)
	syncUI(t, app.tapp)
	got, _ = d.HeraRole(role.ID)
	testutil.Equal(t, got.ArchivedAt != nil, true)
}

func TestSmoke_HeraRenameModalInputAndPaste(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	// `r` opens the input modal.
	sim.InjectKey(tcell.KeyRune, 'r', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraInput) })

	// Clear the pre-filled name, then PASTE a new one (exercises PasteHandler).
	sim.InjectKey(tcell.KeyCtrlU, 0, 0) // delete-to-start
	syncUI(t, app.tapp)
	testutil.NoError(t, sim.PostEvent(tcell.NewEventPaste(true)))
	sim.InjectKey(tcell.KeyRune, 'n', 0)
	sim.InjectKey(tcell.KeyRune, 'u', 0)
	testutil.NoError(t, sim.PostEvent(tcell.NewEventPaste(false)))
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyEnter, 0, 0) // submit
	syncUI(t, app.tapp)

	got, _ := d.HeraRole(role.ID)
	testutil.Equal(t, got.Name, "nu")
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })
}

func TestSmoke_HeraDeleteRoleConfirmCancelKeepsIt(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	heraTabCursorOnWorker(t, app, sim)

	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraConfirm) })

	sim.InjectKey(tcell.KeyEscape, 0, 0) // cancel
	syncUI(t, app.tapp)
	_, err := d.HeraRole(role.ID)
	testutil.NoError(t, err) // still present
}

func TestSmoke_HeraDeleteOrchestratorCascadesPreservesTask(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	role := seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	// Cursor on the orchestrator header (row 0).
	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)

	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, 'y', 0) // confirm
	syncUI(t, app.tapp)

	_, err := d.HeraOrchestrator(orch)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	_, err = d.HeraRole(role.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	// Argus task preserved.
	got, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, got != nil, true)
}

// TestSmoke_HeraDeleteRoleMultiBindingIsolation is the locked must-have:
// deleting role R in orchestrator A leaves the SAME task's role in orchestrator
// B live (the task is preserved because it is bound elsewhere).
func TestSmoke_HeraDeleteRoleMultiBindingIsolation(t *testing.T) {
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
	// orch-a is first (alpha order); its worker role is row 1.
	sim.InjectKey(tcell.KeyRune, 'j', 0)
	syncUI(t, app.tapp)

	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, 'y', 0) // confirm
	syncUI(t, app.tapp)

	// Role in A gone; role in B (same task) intact; task preserved.
	_, err = d.HeraRole(rA.ID)
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
	gotB, err := d.HeraRole(rB.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotB.OrchestratorID, orchB)
	gotTask, err := d.Get(shared)
	testutil.NoError(t, err)
	testutil.Equal(t, gotTask != nil, true)
}

// TestSmoke_HeraTabKeysDoNotBreakTabSwitchOrQuit audits the key-collision
// surface: 1/2/3 still switch tabs while the Hera tab is focused.
func TestSmoke_HeraTabKeysDoNotBreakTabSwitch(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0) // → Hera
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.header.ActiveTab(), widget.TabHera) })

	sim.InjectKey(tcell.KeyRune, '1', 0) // → Tasks
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks) })

	sim.InjectKey(tcell.KeyRune, '2', 0) // → Hera
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyRune, '3', 0) // → Settings
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.header.ActiveTab(), widget.TabSettings) })
}

// TestSmoke_HeraRemoteModeMutationKeysInert proves the Hera-tab mutation keys
// are no-ops in remote mode (no local *db.DB → heraOps nil, callbacks unwired).
func TestSmoke_HeraRemoteModeMutationKeysInert(t *testing.T) {
	app := New(stubStore{}, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	// Any mutation key must not panic and must not leave a modal open.
	sim.InjectKey(tcell.KeyRune, 'a', 0)
	sim.InjectKey(tcell.KeyRune, 'P', 0)
	sim.InjectKey(tcell.KeyCtrlD, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Equal(t, app.heraOps == nil, true)
	})
}

// TestSmoke_HelpListsHeraKeys verifies the help overlay registers the Hera
// section so the bindings are discoverable.
func TestSmoke_HelpListsHeraKeys(t *testing.T) {
	found := false
	for _, sec := range modal.HelpSections {
		if sec.Title == "Hera View (rail)" {
			found = true
		}
	}
	testutil.Equal(t, found, true)
}

// TestSmoke_HeraDetailsTreeMode drives the flow end-to-end through the real
// event loop: Hera tab → coordinator selected → Tab into the Details region
// (which stacks the roster over the orchestration-tree graph) → the embedded
// tree is projected with nodes. Proves the global key routing, focus ladder, and
// tree projection compose without any toggle. The tree edges come from the role
// hierarchy (coordinator → worker), NOT depends_on.
func TestSmoke_HeraDetailsTreeMode(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")

	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	// The cursor lands on the orch header, which IS the coordinator (folded in),
	// so no extra navigation is needed to select the coordinator.
	sim.InjectKey(tcell.KeyRune, '2', 0) // → Hera tab (cursor lands on orch header)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.heraPage.SelectionContext().IsCoordinator(), true) })

	// Tab into the Details region (rail → coord → agent). The tree is stacked
	// under the roster and already projected — no toggle.
	sim.InjectKey(tcell.KeyTab, 0, 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyTab, 0, 0)
	syncUI(t, app.tapp)
	// The embedded tree has nodes (cursor non-empty — lands on the coordinator).
	readUI(t, app.tapp, func() { testutil.Equal(t, app.heraPage.DAG().CurrentTask() != "", true) })
}
