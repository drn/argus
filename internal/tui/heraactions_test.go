package tui

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
)

// --- fast direct-call branch coverage (no event loop) -----------------------

// heraSel builds a Selection whose orchestrator has a live coordinator bound to
// coordTaskID (project "p"), with the cursor conceptually on the orchestrator
// header. Used by the rail-key handler tests.
func heraCoordSel(orchID int64, coordTaskID string) hera.Selection {
	return hera.Selection{Orch: &hera.OrchView{
		ID: orchID, Name: "o",
		Roles: []hera.RoleView{{RoleID: 1, OrchID: orchID, Name: "coord", Kind: db.HeraKindCoordinator, TaskID: coordTaskID, Live: true}},
	}}
}

func TestHeraActions_SpawnWorkerOpensFullModal(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir()}))
	app := New(d, agent.NewRunner(nil), false)

	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")

	app.heraSpawnWorker(heraCoordSel(orch, "tc"))

	// The full new-task modal is open, defaulted to the coordinator's project,
	// returning to the Hera tab, with the worker-spawn override wired.
	testutil.Equal(t, app.mode, modeNewTask)
	testutil.Equal(t, app.newTaskReturnPage, "hera")
	testutil.Equal(t, app.newTaskOnDone != nil, true)
	testutil.Equal(t, app.newTaskForm.SelectedProject(), "p")
}

func TestHeraActions_NewCoordinatorOpensFullModal(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	// Even with an empty selection, `n` opens the modal (bootstrap affordance).
	app.heraNewCoordinator(hera.Selection{})
	testutil.Equal(t, app.mode, modeNewTask)
	testutil.Equal(t, app.newTaskReturnPage, "hera")
	testutil.Equal(t, app.newTaskOnDone != nil, true)
}

// TestHeraActions_HideBranches pins the `a` HIDE key (BUG-022): a coordinator /
// header selection is a feedback no-op (no parent archive to nest under); a
// worker selection hides immediately with NO confirmation (reversible toggle).
func TestHeraActions_HideBranches(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)

	// Coordinator/header selection → feedback, no-op, no confirm.
	app.heraHide(heraCoordSel(1, "tc"))
	testutil.Contains(t, app.statusbar.Error(), "Hide applies to workers")
	testutil.Equal(t, app.mode, modeTaskList)

	// Worker selection → hides immediately (no confirm modal), role archived.
	orch := seedHeraOrch(t, d, "o")
	role := seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	sel := hera.Selection{Role: &hera.RoleView{RoleID: role.ID, OrchID: orch, Name: "w", Kind: db.HeraKindWorker, TaskID: "tw", Live: true}}
	app.heraHide(sel)
	testutil.Equal(t, app.mode, modeTaskList) // no confirm modal
	got, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got.ArchivedAt != nil, true) // hidden (archived)
	testutil.Equal(t, got.NukedAt == nil, true)    // NOT nuked — reversible

	// Pressing `a` again unhides it (reversible toggle).
	sel2 := hera.Selection{Role: &hera.RoleView{RoleID: role.ID, OrchID: orch, Name: "w", Kind: db.HeraKindWorker, Archived: true}}
	app.heraHide(sel2)
	got2, err := d.HeraRole(role.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, got2.ArchivedAt == nil, true) // restored
}

// TestHeraActions_ClearArchiveBranches pins the `C` clear-this-coordinator's-
// archive key (BUG-022): no hidden descendants → "nothing to clear"; with a
// hidden worker → confirm opens.
func TestHeraActions_ClearArchiveBranches(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app.heraPage.Refresh()

	// No hidden descendant workers → "nothing to clear", no modal.
	app.heraClearArchive(hera.Selection{Orch: &hera.OrchView{ID: orch, Name: "o"}})
	testutil.Contains(t, app.statusbar.Info(), "Nothing to clear")
	testutil.Equal(t, app.mode, modeTaskList)

	// Hide a worker so the subtree has a Tier-1 hidden descendant.
	w := seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	testutil.NoError(t, d.ArchiveHeraRole(w.ID))
	app.heraPage.Refresh()
	app.heraClearArchive(hera.Selection{Orch: &hera.OrchView{ID: orch, Name: "o"}})
	testutil.Equal(t, app.mode, modeHeraConfirm)
}

// TestHeraActions_ClearArchiveScopesToSubCoordinator pins BUG-003: pressing `C`
// on a SUB-coordinator (a bridging worker row whose Selection carries the child
// orch id) must scope the nuke set to that sub-coordinator's OWN subtree — not
// the parent orchestrator's whole subtree. A top-level coordinator selection
// (no BridgeChildOrchID) still scopes to its own orch, unchanged.
func TestHeraActions_ClearArchiveScopesToSubCoordinator(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)

	// Parent P (id 1): a coordinator, a LIVE worker "w" bridging to child C, and a
	// hidden parent worker "pw". Child C (id 2): a coordinator bound to "tc-c"
	// (so the bridge index links P's worker → C), plus a hidden child worker "cw".
	parent := hera.OrchView{ID: 1, Name: "P", Roles: []hera.RoleView{
		{RoleID: 10, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc-p"},
		{RoleID: 11, OrchID: 1, Name: "w", Kind: db.HeraKindWorker, Live: true, TaskID: "tc-c", BridgeTaskID: "tc-c"},
		{RoleID: 12, OrchID: 1, Name: "pw", Kind: db.HeraKindWorker, Archived: true},
	}}
	child := hera.OrchView{ID: 2, Name: "C", Roles: []hera.RoleView{
		{RoleID: 20, OrchID: 2, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc-c", BridgeTaskID: "tc-c"},
		{RoleID: 21, OrchID: 2, Name: "cw", Kind: db.HeraKindWorker, Archived: true},
	}}
	app.heraPage.Rail().SetModel(hera.Model{Active: []hera.OrchView{parent, child}})

	// Sub-coordinator selection: cursor on the bridging worker row, carrying the
	// child orch id. Scope = child subtree only → 1 hidden worker ("cw"), NOT 2.
	subSel := hera.Selection{
		Role:              &hera.RoleView{RoleID: 11, OrchID: 1, Name: "w", Kind: db.HeraKindWorker, Live: true, TaskID: "tc-c", BridgeTaskID: "tc-c"},
		Orch:              &parent,
		BridgeChildOrchID: 2,
	}
	app.heraClearArchive(subSel)
	testutil.Equal(t, app.mode, modeHeraConfirm)
	testutil.Contains(t, app.heraConfirmModal.Message(), "Nukes 1 hidden agent(s)")
	// Title names the SELECTED sub-coordinator (child "C"), not the parent "P".
	testutil.Contains(t, app.heraConfirmModal.Title(), "C's archive")
	app.closeHeraConfirm()

	// Top-level coordinator selection (no BridgeChildOrchID): scope = parent's
	// whole subtree → both hidden workers ("pw" + "cw") → 2.
	topSel := hera.Selection{Orch: &parent}
	app.heraClearArchive(topSel)
	testutil.Equal(t, app.mode, modeHeraConfirm)
	testutil.Contains(t, app.heraConfirmModal.Message(), "Nukes 2 hidden agent(s)")
	testutil.Contains(t, app.heraConfirmModal.Title(), "P's archive")
	app.closeHeraConfirm()
}

// TestHeraActions_NewTaskOverrideInvokesOnDone drives the shared new-task modal
// through submit and asserts the Hera override (newTaskOnDone) runs instead of
// the default create path, the form returns to the Hera tab, and state is reset.
func TestHeraActions_NewTaskOverrideInvokesOnDone(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetProject("p", config.Project{Path: t.TempDir()}))
	app := New(d, agent.NewRunner(nil), false)

	var gotTask *model.Task
	var gotName string
	app.openHeraNewTaskForm(" Test ", "p", func(task *model.Task, name string) {
		gotTask = task
		gotName = name
	})
	testutil.Equal(t, app.mode, modeNewTask)

	app.newTaskForm.focused = ntFieldPrompt
	for _, r := range "do it" {
		app.handleNewTaskKey(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone))
	}
	app.handleNewTaskKey(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))

	testutil.Equal(t, gotTask != nil, true)
	// No name typed → the callback's name param is blank (user-chosen signal off).
	testutil.Equal(t, gotName, "")
	testutil.Equal(t, gotTask.Prompt, "do it")
	// Form closed, override cleared, return page reset.
	testutil.Equal(t, app.mode, modeTaskList)
	testutil.Equal(t, app.newTaskOnDone == nil, true)
	testutil.Equal(t, app.newTaskReturnPage, "")
}

// TestHeraActions_NukeArchivedRoleSoleBound exercises `C`'s per-role nuke of a
// sole-bound hidden role: task ARCHIVED (not completed/deleted), role NUKED
// (nuked_at set, NOT hard-deleted — the row is retained for DB recovery).
func TestHeraActions_NukeArchivedRoleSoleBound(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)
	orch := seedHeraOrch(t, d, "o")
	role := seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	testutil.NoError(t, d.ArchiveHeraRole(role.ID)) // hidden first

	rv := &hera.RoleView{RoleID: role.ID, OrchID: orch, Kind: db.HeraKindWorker, TaskID: "tw", BridgeTaskID: "tw", Archived: true}
	app.heraNukeArchivedRole(rv)

	task, err := d.Get("tw")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, true) // archived, NOT completed
	got, err := d.HeraRole(role.ID)        // row RETAINED (no hard delete)
	testutil.NoError(t, err)
	testutil.Equal(t, got.NukedAt != nil, true)
}

// TestHeraActions_NukeArchivedRoleMultiBoundPreservesTask verifies a task bound
// live under another orchestrator is preserved — only this role row is nuked.
func TestHeraActions_NukeArchivedRoleMultiBoundPreservesTask(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)
	a := seedHeraOrch(t, d, "A")
	b := seedHeraOrch(t, d, "B")
	roleA := seedHeraBoundRole(t, d, a, "w", db.HeraKindWorker, "shared")
	roleB, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: b, Name: "c", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: roleB.ID, ArgusTaskID: "shared", WorktreePath: "/wt/shared2"})
	testutil.NoError(t, err)

	rv := &hera.RoleView{RoleID: roleA.ID, OrchID: a, Kind: db.HeraKindWorker, TaskID: "shared", BridgeTaskID: "shared", Live: true}
	reclaimed := app.heraNukeArchivedRole(rv)

	testutil.Equal(t, reclaimed, false) // bound elsewhere → worktree not reclaimed
	task, err := d.Get("shared")
	testutil.NoError(t, err)
	testutil.Equal(t, task.Archived, false) // preserved (not archived)
	gotA, err := d.HeraRole(roleA.ID)       // this role nuked but RETAINED
	testutil.NoError(t, err)
	testutil.Equal(t, gotA.NukedAt != nil, true)
	gotB, err := d.HeraRole(roleB.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotB.OrchestratorID, b) // other orchestrator's role intact
}

func TestHeraActions_EOLKeysRemoteInert(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = nil // simulate remote mode

	app.heraNewCoordinator(hera.Selection{})
	app.heraHide(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindWorker}})
	app.heraClearArchive(hera.Selection{Orch: &hera.OrchView{ID: 1}})
	app.heraOpenDelete(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindWorker}})
	// No modal opened, no panic.
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestHeraActions_SpawnWorkerValidationBranches(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)

	// No orchestrator / no coordinator → guarded, no modal.
	app.heraSpawnWorker(hera.Selection{})
	testutil.Equal(t, app.mode, modeTaskList)

	// Orchestrator with a coordinator role but the coord task has no project.
	orch := seedHeraOrch(t, d, "o")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc-noproj")
	// Blank the project on the coord task so resolution fails.
	tc, _ := d.Get("tc-noproj")
	tc.Project = ""
	testutil.NoError(t, d.Update(tc))
	sel := hera.Selection{Orch: &hera.OrchView{
		ID: orch, Name: "o",
		Roles: []hera.RoleView{{RoleID: 1, OrchID: orch, Name: "coord", Kind: db.HeraKindCoordinator, TaskID: "tc-noproj", Live: true}},
	}}
	app.heraSpawnWorker(sel)
	testutil.Equal(t, app.mode, modeTaskList) // still no modal (project missing)
}

func TestHeraActions_ReattachNoopBranches(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)

	app.heraReattach(hera.Selection{})                                      // empty taskID → no-op
	app.heraReattach(hera.Selection{Role: &hera.RoleView{TaskID: "ghost"}}) // missing task → no-op
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestReviveHeraWorker_GuardsAreNoops(t *testing.T) {
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)

	live := &fakeKickSession{alive: true, idle: true}

	// nil task, nil session, dead session, and empty-SessionID all return before
	// any kick is attempted — no panic, no error surfaced.
	app.reviveHeraWorker(nil, live)
	app.reviveHeraWorker(&model.Task{ID: "x", SessionID: "s"}, nil)
	app.reviveHeraWorker(&model.Task{ID: "x", SessionID: "s"}, &fakeKickSession{alive: false})
	app.reviveHeraWorker(&model.Task{ID: "x", SessionID: ""}, live) // no session id → cannot resume
	testutil.Equal(t, app.statusbar.Error(), "")

	// A kick already pending for the task short-circuits.
	runner.SetPendingRestartForTest("busy-task", true)
	app.reviveHeraWorker(&model.Task{ID: "busy-task", SessionID: "s"}, live)
	testutil.Equal(t, app.statusbar.Error(), "")
}

func TestReviveHeraWorker_GatingViaEventLoop(t *testing.T) {
	t.Run("busy worker is not kicked", func(t *testing.T) {
		_, app := newReviveApp(t)
		_, stop := wireApp(t, app)
		defer stop()
		sess := &fakeKickSession{alive: true, idle: false} // busy
		readUI(t, app.tapp, func() {
			app.reviveHeraWorker(&model.Task{ID: "busy", SessionID: "sid"}, sess)
		})
		settleReviveNoKick(t, app)
	})

	t.Run("worker blocked on prompt is not kicked", func(t *testing.T) {
		_, app := newReviveApp(t)
		// A prompt in the session log → sessionBlockedOnPrompt true → preserve.
		writeReviveSessionLog(t, "blocked", "Do you want to proceed?\n❯ 1. Yes\n  2. No\n")
		_, stop := wireApp(t, app)
		defer stop()
		sess := &fakeKickSession{alive: true, idle: true}
		readUI(t, app.tapp, func() {
			app.reviveHeraWorker(&model.Task{ID: "blocked", SessionID: "sid"}, sess)
		})
		settleReviveNoKick(t, app)
	})

	t.Run("idle non-blocked worker is kicked", func(t *testing.T) {
		_, app := newReviveApp(t)
		// No prompt → idle + not-blocked → revive attempted. With no real session
		// in the runner, KickRerender returns ErrSessionNotFound, surfaced as a
		// statusbar error — a durable signal that the gate passed and the kick ran.
		writeReviveSessionLog(t, "stuck", "working on it...\n")
		_, stop := wireApp(t, app)
		defer stop()
		sess := &fakeKickSession{alive: true, idle: true}
		readUI(t, app.tapp, func() {
			app.reviveHeraWorker(&model.Task{ID: "stuck", SessionID: "sid"}, sess)
		})
		deadline := time.Now().Add(uiTimeout)
		kicked := false
		for time.Now().Before(deadline) {
			readUI(t, app.tapp, func() { kicked = app.statusbar.Error() != "" })
			if kicked {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		testutil.Equal(t, kicked, true)
	})
}

// newReviveApp builds an App on a temp HOME (so session logs land under the temp
// dir) with an in-process runner that satisfies agent.SessionRunner.
func newReviveApp(t *testing.T) (*db.DB, *App) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	return d, New(d, agent.NewRunner(nil), false)
}

func writeReviveSessionLog(t *testing.T, taskID, body string) {
	t.Helper()
	p := agent.SessionLogPath(taskID)
	testutil.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	testutil.NoError(t, os.WriteFile(p, []byte(body), 0o644))
}

// settleReviveNoKick gives the revive goroutine a moment to run and asserts it
// did NOT attempt a kick (no info/error surfaced).
func settleReviveNoKick(t *testing.T, app *App) {
	t.Helper()
	for i := 0; i < 10; i++ {
		syncUI(t, app.tapp)
		time.Sleep(10 * time.Millisecond)
	}
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.statusbar.Error(), "")
		testutil.Equal(t, app.statusbar.Info(), "")
	})
}

func TestHeraPaneFocused_GlobalKeySurrender(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	// Tasks tab → never "hera pane focused".
	testutil.Equal(t, app.heraPaneFocused(), false)

	// Hera tab but the RAIL holds focus → globals still apply (rail is not a
	// content pane), so heraPaneFocused stays false.
	app.header.SetTab(widget.TabHera)
	testutil.Equal(t, app.heraPaneFocused(), false)

	// Hera tab with a content pane focused → true.
	app.heraPage.Machine().Advance() // rail → coordinator pane
	testutil.Equal(t, app.heraPaneFocused(), true)

	// With a Hera pane focused, the global handler must NOT consume the keys it
	// otherwise would (BUG-001): each returns the event (fall-through to the page,
	// which forwards it to the pane PTY) rather than nil (consumed).
	keys := []*tcell.EventKey{
		tcell.NewEventKey(tcell.KeyRune, 'q', tcell.ModNone), // would quit argus
		tcell.NewEventKey(tcell.KeyRune, '1', tcell.ModNone), // would switch tab
		tcell.NewEventKey(tcell.KeyRune, '2', tcell.ModNone), // would switch tab
		tcell.NewEventKey(tcell.KeyRune, '3', tcell.ModNone), // would switch tab
		tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone), // would open help
		tcell.NewEventKey(tcell.KeyCtrlC, 0, tcell.ModNone),  // would quit argus
		tcell.NewEventKey(tcell.KeyCtrlL, 0, tcell.ModNone),  // would Sync
	}
	for _, ev := range keys {
		if got := app.handleGlobalKey(ev); got == nil {
			t.Fatalf("key %q was consumed while a Hera pane was focused; expected fall-through to the pane", ev.Name())
		}
	}

	// Back on the rail, `?` is once again a global (opens help, consumed).
	app.heraPage.Machine().ToRail()
	testutil.Equal(t, app.heraPaneFocused(), false)
	if got := app.handleGlobalKey(tcell.NewEventKey(tcell.KeyRune, '?', tcell.ModNone)); got != nil {
		t.Fatalf("? on the rail should be consumed (open help), got fall-through")
	}
}

func TestHeraActions_StatusStepNilRoleNoop(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)
	// Coordinator-less header → StatusRole() nil → no-op (BUG-014: a header WITH a
	// coordinator does step it; that path is covered in ops_test.go).
	app.heraStatusStep(hera.Selection{Orch: &hera.OrchView{ID: 1}}, +1)
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestHeraActions_OpenRenameEmptySelectionNoModal(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraOps = hera.NewOps(d)
	app.heraOpenRename(hera.Selection{})
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestHeraActions_SelName(t *testing.T) {
	testutil.Equal(t, heraSelName(hera.Selection{Role: &hera.RoleView{Name: "w"}}), "role w")
	testutil.Equal(t, heraSelName(hera.Selection{Orch: &hera.OrchView{Name: "o"}}), "orchestrator o")
	testutil.Equal(t, heraSelName(hera.Selection{}), "selection")
}

func TestHeraActions_CoordRoleName(t *testing.T) {
	o := &hera.OrchView{Roles: []hera.RoleView{
		{Name: "w", Kind: db.HeraKindWorker},
		{Name: "boss", Kind: db.HeraKindCoordinator},
	}}
	testutil.Equal(t, heraCoordRoleName(o), "boss")
	// Fallback when no coordinator role.
	testutil.Equal(t, heraCoordRoleName(&hera.OrchView{Roles: []hera.RoleView{{Name: "w", Kind: db.HeraKindWorker}}}), "coordinator")
}

func TestHeraCoordReparentTarget(t *testing.T) {
	t.Run("coordinator role row qualifies", func(t *testing.T) {
		sel := hera.Selection{
			Role: &hera.RoleView{Kind: db.HeraKindCoordinator, Name: "c", TaskID: "ct"},
			Orch: &hera.OrchView{ID: 7, Name: "orch-c"},
		}
		id, name, task, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, id, int64(7))
		testutil.Equal(t, name, "c") // the coordinator role's name
		testutil.Equal(t, task, "ct")
	})
	t.Run("orchestrator header with a coordinator role qualifies", func(t *testing.T) {
		sel := hera.Selection{Orch: &hera.OrchView{
			ID: 8, Name: "o",
			Roles: []hera.RoleView{{Kind: db.HeraKindCoordinator, TaskID: "ct", Live: true}},
		}}
		id, name, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, true)
		testutil.Equal(t, id, int64(8))
		testutil.Equal(t, name, "o")
	})
	t.Run("worker role does not qualify", func(t *testing.T) {
		sel := hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindWorker}, Orch: &hera.OrchView{ID: 1}}
		_, _, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, false)
	})
	t.Run("archived coordinator does not qualify", func(t *testing.T) {
		sel := hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindCoordinator, Archived: true}, Orch: &hera.OrchView{ID: 1}}
		_, _, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, false)
	})
	t.Run("orchestrator header without a coordinator role does not qualify", func(t *testing.T) {
		sel := hera.Selection{Orch: &hera.OrchView{ID: 1, Roles: []hera.RoleView{{Kind: db.HeraKindWorker}}}}
		_, _, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, false)
	})
	t.Run("orchestrator header whose only coordinator role is archived does not qualify", func(t *testing.T) {
		// Symmetry with the role-row branch (which guards !Archived).
		sel := hera.Selection{Orch: &hera.OrchView{ID: 1, Roles: []hera.RoleView{
			{Kind: db.HeraKindCoordinator, Archived: true},
		}}}
		_, _, _, ok := heraCoordReparentTarget(sel)
		testutil.Equal(t, ok, false)
	})
	t.Run("empty selection does not qualify", func(t *testing.T) {
		_, _, _, ok := heraCoordReparentTarget(hera.Selection{})
		testutil.Equal(t, ok, false)
	})
}

func TestHeraActions_OpenAdoptNoOpAndFeedbackBranches(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)

	// Remote mode (heraAdoptOps nil) → fully inert.
	app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: "x"}})
	testutil.Equal(t, app.mode, modeTaskList)

	app.heraAdoptOps = hera.NewAdoptOps(d)

	// Empty selection → feedback, no picker.
	app.heraOpenAdopt(hera.Selection{})
	testutil.Equal(t, app.mode, modeTaskList)

	// Managed worker role → feedback, no picker.
	app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindWorker}, Orch: &hera.OrchView{ID: 1}})
	testutil.Equal(t, app.mode, modeTaskList)

	// Freelancer with no task id → feedback, no picker.
	app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: ""}})
	testutil.Equal(t, app.mode, modeTaskList)

	// Freelancer with a task but NO active orchestrators → feedback, no picker.
	testutil.NoError(t, d.Add(&model.Task{ID: "tf", Name: "tf", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: "tf", Name: "tf"}})
	testutil.Equal(t, app.mode, modeTaskList)

	// Coordinator with NO other orchestrator to nest under → feedback, no picker.
	orch := seedHeraOrch(t, d, "only")
	seedHeraBoundRole(t, d, orch, "only", db.HeraKindCoordinator, "tc")
	app.heraOpenAdopt(hera.Selection{
		Role: &hera.RoleView{Kind: db.HeraKindCoordinator, TaskID: "tc", Name: "only"},
		Orch: &hera.OrchView{ID: orch, Name: "only"},
	})
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestSmoke_HeraAdoptFreelancerThroughPicker(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraAdoptOps = hera.NewAdoptOps(d)
	target := seedHeraOrch(t, d, "target")
	testutil.NoError(t, d.Add(&model.Task{ID: "tf", Name: "freelancer", Status: model.StatusInProgress, Project: "p", Worktree: "/wt/tf", CreatedAt: time.Now()}))

	sim, stop := wireApp(t, app)
	defer stop()

	// Open the adopt picker for a freelancer on the tview thread.
	sel := hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: "tf", Name: "freelancer"}}
	readUI(t, app.tapp, func() { app.heraOpenAdopt(sel) })
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraOrchPicker) })

	// Enter selects the (single) orchestrator → adopt runs.
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })

	live, err := d.HeraLiveBindingByTaskAndOrchestrator("tf", target)
	testutil.NoError(t, err)
	testutil.Equal(t, live.WorktreePath, "/wt/tf")
}

func TestSmoke_HeraReparentCoordinatorThroughPicker(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraAdoptOps = hera.NewAdoptOps(d)
	child := seedHeraOrch(t, d, "child")
	seedHeraBoundRole(t, d, child, "child", db.HeraKindCoordinator, "child-coord")
	parent := seedHeraOrch(t, d, "parent")
	seedHeraBoundRole(t, d, parent, "parent", db.HeraKindCoordinator, "parent-coord")

	sim, stop := wireApp(t, app)
	defer stop()

	sel := hera.Selection{
		Role: &hera.RoleView{Kind: db.HeraKindCoordinator, TaskID: "child-coord", Name: "child"},
		Orch: &hera.OrchView{ID: child, Name: "child"},
	}
	readUI(t, app.tapp, func() { app.heraOpenAdopt(sel) })
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraOrchPicker) })

	// The picker excludes the child itself, so only "parent" remains; Enter picks it.
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)

	link, err := d.HeraLiveBindingByTaskAndOrchestrator("child-coord", parent)
	testutil.NoError(t, err)
	testutil.Equal(t, link.OrchestratorID, parent)
}

func TestSmoke_HeraAdoptPickerEscCancels(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.heraAdoptOps = hera.NewAdoptOps(d)
	seedHeraOrch(t, d, "target")
	testutil.NoError(t, d.Add(&model.Task{ID: "tf", Name: "tf", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))

	sim, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.heraOpenAdopt(hera.Selection{Role: &hera.RoleView{Kind: db.HeraKindFreelance, TaskID: "tf", Name: "tf"}})
	})
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeHeraOrchPicker) })

	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })

	// Esc made no change — no binding created.
	_, err := d.HeraLiveBindingByTask("tf")
	testutil.ErrorIs(t, err, db.ErrHeraNotFound)
}

// --- real-repo integration (spawn + reattach through the event loop) --------

// heraRepoApp sets HOME to a temp dir, builds a one-commit git repo, and seeds
// project "p" + an echo backend so CreateAndStart / startSession succeed.
func heraRepoApp(t *testing.T) (*App, *db.DB) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "t@t.com")
	run("config", "user.name", "T")
	testutil.NoError(t, os.WriteFile(filepath.Join(repo, "README.md"), []byte("hi"), 0o644))
	run("add", ".")
	run("commit", "-q", "-m", "init")

	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("defaults.backend", "test"))
	testutil.NoError(t, d.SetBackend("test", config.Backend{Command: "echo hello", PromptFlag: ""}))
	testutil.NoError(t, d.SetProject("p", config.Project{Path: repo, Branch: "HEAD"}))
	app := New(d, agent.NewRunner(nil), false)
	return app, d
}

func TestSmoke_HeraSpawnWorkerCreatesRoleAndTask(t *testing.T) {
	app, d := heraRepoApp(t)
	orch := seedHeraOrch(t, d, "orch")
	// Coordinator bound to a real task in project "p".
	testutil.NoError(t, d.Add(&model.Task{ID: "tc", Name: "tc", Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	coord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: coord.ID, ArgusTaskID: "tc", WorktreePath: "/wt/tc"})
	testutil.NoError(t, err)

	sim, stop := wireApp(t, app)
	defer stop()
	t.Cleanup(func() { app.runner.StopAll() })

	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp) // → Hera tab, cursor on orch header

	// `w` opens the FULL new-task modal (project defaulted to the coordinator's).
	sim.InjectKey(tcell.KeyRune, 'w', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeNewTask) })

	// The prompt field is focused by default — type a prompt and submit.
	for _, r := range "do work" {
		sim.InjectKey(tcell.KeyRune, r, 0)
	}
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyEnter, 0, 0)

	// The spawn runs in a goroutine; poll for the new worker role.
	deadline := time.Now().Add(3 * time.Second)
	var workerCount int
	for time.Now().Before(deadline) {
		syncUI(t, app.tapp)
		roles, _ := d.ListHeraRolesByKind(orch, db.HeraKindWorker)
		workerCount = len(roles)
		if workerCount >= 1 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	testutil.Equal(t, workerCount, 1)
}

func TestSmoke_HeraReattachRestartsSession(t *testing.T) {
	app, d := heraRepoApp(t)
	orch := seedHeraOrch(t, d, "orch")
	// A coordinator task with a real worktree (the repo) but no live session.
	repo := d.Config().Projects["p"].Path
	testutil.NoError(t, d.Add(&model.Task{ID: "tc", Name: "tc", Status: model.StatusInReview, Project: "p", Worktree: repo, CreatedAt: time.Now()}))
	coord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orch, Name: "coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: coord.ID, ArgusTaskID: "tc", WorktreePath: repo})
	testutil.NoError(t, err)

	sim, stop := wireApp(t, app)
	defer stop()
	t.Cleanup(func() { app.runner.StopAll() })

	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	// The orch header IS the coordinator (folded), so it is already selected; the
	// j is a no-op here (no worker rows), and Enter reattaches the coordinator.
	sim.InjectKey(tcell.KeyRune, 'j', 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyEnter, 0, 0) // Enter → reattach (no live session)

	// startSession mints a session ID for the (Claude-style) echo backend and
	// persists it before runner.Start — a durable signal that reattach ran,
	// even though the echo session exits instantly.
	deadline := time.Now().Add(3 * time.Second)
	started := false
	for time.Now().Before(deadline) {
		syncUI(t, app.tapp)
		if got, _ := d.Get("tc"); got != nil && got.SessionID != "" {
			started = true
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	testutil.Equal(t, started, true)
}

// --- BUG-017: coord/orchestrator HEADER delete cascades the full subtree ------

// seedHeraRoleOnTask binds a NEW role under orchID to an ALREADY-EXISTING argus
// task (no task Add). Used to build worker→coordinator bridges and the
// multi-binding case (the same task bound under several orchestrators).
func seedHeraRoleOnTask(t *testing.T, d *db.DB, orchID int64, name string, kind db.HeraRoleKind, taskID, wt string) *db.HeraRole {
	t.Helper()
	role, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchID, Name: name, Kind: kind, ArgusProject: "p"})
	testutil.NoError(t, err)
	_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: role.ID, ArgusTaskID: taskID, WorktreePath: wt})
	testutil.NoError(t, err)
	return role
}

// TestHeraActions_OrchHeaderDeleteCascadesSingleOrch verifies BUG-017 (Option A):
// Ctrl+D on a coordinator/orchestrator HEADER opens the COUNT-bearing cascade
// confirm and, on accept, ARCHIVES the orchestrator + roles + tasks (NO hard
// deletes) and reclaims their worktrees — even with no nested sub-coordinators.
func TestHeraActions_OrchHeaderDeleteCascadesSingleOrch(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false)
	orch := seedHeraOrch(t, d, "solo")
	coord := seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	wkr := seedHeraBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	app.heraPage.Refresh()

	app.heraOpenDelete(hera.Selection{Orch: &hera.OrchView{ID: orch, Name: "solo"}})

	// The count-bearing cascade confirm opens, worded "removes" + "reclaims".
	testutil.Equal(t, app.mode, modeHeraConfirm)
	msg := app.heraConfirmModal.Message()
	testutil.Contains(t, msg, "removes")
	testutil.Contains(t, msg, "reclaims")
	testutil.Contains(t, msg, "1 orchestrator(s)")
	testutil.Contains(t, msg, "1 agent(s)")      // the worker (coordinator not counted)
	testutil.Contains(t, msg, "2 worktree(s)")   // coord task + worker task
	testutil.Contains(t, msg, "0 task(s) bound") // none preserved

	// Accept → cascade NUKES the orchestrator + roles (no hard deletes) and
	// ARCHIVES the tasks.
	app.heraConfirmDo()

	gotOrch, err := d.HeraOrchestrator(orch) // still exists, NUKED
	testutil.NoError(t, err)
	testutil.Equal(t, gotOrch.NukedAt != nil, true)
	for _, role := range []*db.HeraRole{coord, wkr} {
		gotRole, rErr := d.HeraRole(role.ID) // role row nuked, not deleted
		testutil.NoError(t, rErr)
		testutil.Equal(t, gotRole.NukedAt != nil, true)
		_, bErr := d.HeraLiveBindingByRole(role.ID) // binding ended
		testutil.ErrorIs(t, bErr, db.ErrHeraNotFound)
	}
	for _, id := range []string{"tc", "tw"} {
		task, tErr := d.Get(id) // task row archived, not deleted
		testutil.NoError(t, tErr)
		testutil.Equal(t, task.Archived, true)
	}
}

// TestHeraActions_OrchHeaderDeleteCascadesNestedSubtree verifies the deep case:
// nuking a TOP-LEVEL coordinator NUKES the coordinator, its agents, AND every
// nested sub-coordinator + their agents (no hard deletes), reclaiming worktrees.
// The internal-bridge worktree (the task bridging two subtree orchestrators) is
// counted + archived; a task also bound live in an orchestrator OUTSIDE the
// subtree is PRESERVED (left fully alone — not archived).
//
//	P (top-level) ── worker→tC ──▶ C (sub-coord) ── worker→tShared ─┐
//	  coord→tP                       coord→tC                        │
//	                                                  O (outside): worker→tShared, coord→tO
func TestHeraActions_OrchHeaderDeleteCascadesNestedSubtree(t *testing.T) {
	d := testDB(t)
	t.Setenv("HOME", t.TempDir())
	app := New(d, agent.NewRunner(nil), false)

	// Tasks (one row each; bridges/multi-binding reuse them across orchestrators).
	for _, id := range []string{"tP", "tC", "tShared", "tO"} {
		testutil.NoError(t, d.Add(&model.Task{ID: id, Name: id, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
	}
	p := seedHeraOrch(t, d, "P")
	c := seedHeraOrch(t, d, "C")
	o := seedHeraOrch(t, d, "O")
	// P: coordinator tP, plus a worker bound to tC (= C's coordinator → C nests under P).
	seedHeraRoleOnTask(t, d, p, "coord", db.HeraKindCoordinator, "tP", "/wt/tP")
	wP := seedHeraRoleOnTask(t, d, p, "wP", db.HeraKindWorker, "tC", "/wt/tC-p")
	// C: coordinator tC, plus a worker bound to tShared.
	seedHeraRoleOnTask(t, d, c, "coord", db.HeraKindCoordinator, "tC", "/wt/tC")
	wC := seedHeraRoleOnTask(t, d, c, "wC", db.HeraKindWorker, "tShared", "/wt/tShared-c")
	// O (outside the subtree): its OWN coordinator tO, plus a worker on the shared
	// task — so tShared is bound live OUTSIDE {P,C} and must be preserved. O's coord
	// task (tO) is bridged by nobody in {P,C}, so O stays a separate top-level orch.
	seedHeraRoleOnTask(t, d, o, "coord", db.HeraKindCoordinator, "tO", "/wt/tO")
	wO := seedHeraRoleOnTask(t, d, o, "wO", db.HeraKindWorker, "tShared", "/wt/tShared-o")
	app.heraPage.Refresh()

	// BridgeSubtree rooted at the TOP-LEVEL coordinator P sees the full nested chain.
	subtree := app.heraPage.Rail().Model().BridgeSubtree(p)
	names := make([]string, len(subtree))
	for i, ov := range subtree {
		names[i] = ov.Name
	}
	testutil.DeepEqual(t, names, []string{"P", "C"})

	app.heraOpenDelete(hera.Selection{Orch: &hera.OrchView{ID: p, Name: "P"}})
	testutil.Equal(t, app.mode, modeHeraConfirm)
	msg := app.heraConfirmModal.Message()
	testutil.Contains(t, msg, "removes")
	testutil.Contains(t, msg, "2 orchestrator(s)")
	testutil.Contains(t, msg, "2 agent(s)")      // wP + wC
	testutil.Contains(t, msg, "2 worktree(s)")   // tP + tC (internal bridge); tShared preserved
	testutil.Contains(t, msg, "1 task(s) bound") // tShared preserved

	app.heraConfirmDo()

	// Both subtree orchestrators NUKED (still present); the outside one untouched.
	for _, id := range []int64{p, c} {
		gotOrch, oErr := d.HeraOrchestrator(id)
		testutil.NoError(t, oErr)
		testutil.Equal(t, gotOrch.NukedAt != nil, true)
	}
	oOrch, err := d.HeraOrchestrator(o)
	testutil.NoError(t, err)
	testutil.Equal(t, oOrch.NukedAt == nil, true) // outside orchestrator untouched

	// Subtree worker roles nuked + bindings ended; the OUTSIDE worker untouched.
	for _, role := range []*db.HeraRole{wP, wC} {
		gotRole, rErr := d.HeraRole(role.ID)
		testutil.NoError(t, rErr)
		testutil.Equal(t, gotRole.NukedAt != nil, true)
		_, bErr := d.HeraLiveBindingByRole(role.ID)
		testutil.ErrorIs(t, bErr, db.ErrHeraNotFound)
	}
	gotWO, err := d.HeraRole(wO.ID)
	testutil.NoError(t, err)
	testutil.Equal(t, gotWO.NukedAt == nil, true) // outside role untouched
	wob, err := d.HeraLiveBindingByRole(wO.ID)
	testutil.NoError(t, err) // outside binding still live
	testutil.Equal(t, wob != nil, true)

	// Subtree-only tasks ARCHIVED (incl. the internal bridge tC); preserved task kept.
	for _, id := range []string{"tP", "tC"} {
		task, tErr := d.Get(id)
		testutil.NoError(t, tErr)
		testutil.Equal(t, task.Archived, true)
	}
	shared, err := d.Get("tShared")
	testutil.NoError(t, err)
	testutil.Equal(t, shared.Archived, false) // preserved — left fully alone
	testutil.Equal(t, shared.Status, model.StatusInProgress)
	to, err := d.Get("tO")
	testutil.NoError(t, err)
	testutil.Equal(t, to.Archived, false)
}
