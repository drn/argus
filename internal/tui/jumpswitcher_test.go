package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/hera"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/modal"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// seedSwitcherTasks adds three plain tasks (one archived) to project "p" and
// refreshes the cache.
func seedSwitcherTasks(t *testing.T, app *App) {
	t.Helper()
	tasks := []*model.Task{
		{ID: "ts-cur", Name: "current task", Status: model.StatusInProgress, Project: "p", Worktree: t.TempDir(), CreatedAt: time.Now()},
		{ID: "ts-zeta", Name: "Zeta work", Status: model.StatusInProgress, Project: "p", Worktree: t.TempDir(), CreatedAt: time.Now()},
		{ID: "ts-alpha", Name: "Alpha work", Status: model.StatusInReview, Project: "p", Worktree: t.TempDir(), CreatedAt: time.Now()},
		{ID: "ts-arch", Name: "Archived work", Status: model.StatusComplete, Project: "p", Worktree: t.TempDir(), Archived: true, CreatedAt: time.Now()},
	}
	for _, tk := range tasks {
		testutil.NoError(t, app.db.Add(tk))
	}
	app.refreshTasks()
}

// startSleepSession starts a real (sleep) session for taskID and returns it.
func startSleepSession(t *testing.T, app *App, runner *agent.Runner, taskID string) agent.SessionHandle {
	t.Helper()
	task, err := app.db.Get(taskID)
	testutil.NoError(t, err)
	task.Backend = "test"
	cfg := config.DefaultConfig()
	cfg.Backends["test"] = config.Backend{Command: "sleep 30"}
	sess, err := runner.Start(task, cfg, 24, 80, false)
	testutil.NoError(t, err)
	t.Cleanup(func() { _ = runner.Stop(taskID) })
	return sess
}

// enterAgentViewFor parks the app in the classic agent view for taskID the way
// onTaskSelect does (header hidden, session attached, worktreeDir set).
func enterAgentViewFor(app *App, task *model.Task, sess agent.SessionHandle) {
	app.mode = modeAgent
	app.agentState.Reset(task.ID, task.Name)
	app.agentPane.SetSession(sess)
	app.worktreeDir = task.Worktree
	app.root.ResizeItem(app.header, 0, 0)
}

func ctrlJ() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyCtrlJ, 0, tcell.ModNone) }

func switcherKey(app *App, key tcell.Key, r rune) {
	app.handleJumpSwitcherKey(tcell.NewEventKey(key, r, tcell.ModNone))
}

func switcherType(app *App, s string) {
	for _, r := range s {
		switcherKey(app, tcell.KeyRune, r)
	}
}

// --- 1.3: reach ---

// TestJumpSwitcher_OpensFromClassicAgentView covers zoom on and off: ctrl+j is
// consumed by the global dispatch (never reaches the PTY — the session's user
// input stamp stays zero) and Esc restores the agent view + its focus.
func TestJumpSwitcher_OpensFromClassicAgentView(t *testing.T) {
	for _, zoom := range []bool{false, true} {
		t.Run(map[bool]string{false: "unzoomed", true: "zoomed"}[zoom], func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			d := testDB(t)
			runner := agent.NewRunner(nil)
			app := New(d, runner, false)
			seedSwitcherTasks(t, app)
			sess := startSleepSession(t, app, runner, "ts-cur")
			cur, err := d.Get("ts-cur")
			testutil.NoError(t, err)
			enterAgentViewFor(app, cur, sess)
			if zoom {
				app.setAgentZen()
			} else {
				app.clearAgentZen()
			}

			testutil.Nil(t, app.handleGlobalKey(ctrlJ()))
			testutil.Equal(t, app.mode, modeJumpSwitcher)
			testutil.True(t, app.jumpSwitcher != nil)
			testutil.True(t, app.pages.HasPage(jumpSwitcherPage))
			testutil.True(t, app.tapp.GetFocus() == tview.Primitive(app.jumpSwitcher))
			testutil.True(t, sess.LastUserInput().IsZero())
			testutil.Equal(t, app.agentZen, zoom)
			// Nothing needs input, so the cursor starts on the viewed task.
			testutil.Equal(t, app.jumpSwitcher.ActiveColumn(), SwitcherColumnTasks)
			testutil.Equal(t, app.jumpSwitcher.tasks.SelectedTask().ID, "ts-cur")

			switcherKey(app, tcell.KeyEscape, 0)
			testutil.Equal(t, app.mode, modeAgent)
			testutil.Nil(t, app.jumpSwitcher)
			testutil.False(t, app.pages.HasPage(jumpSwitcherPage))
			testutil.True(t, app.tapp.GetFocus() == tview.Primitive(app.agentPane))
			testutil.Equal(t, app.agentState.TaskID, "ts-cur")
		})
	}
}

func TestJumpSwitcher_OpensFromTasksTab(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.tasklist.SelectByID("ts-zeta")

	testutil.Nil(t, app.handleGlobalKey(ctrlJ()))
	testutil.Equal(t, app.mode, modeJumpSwitcher)
	testutil.Equal(t, app.jumpSwitcher.tasks.SelectedTask().ID, "ts-zeta") // current = tasklist selection

	switcherKey(app, tcell.KeyEscape, 0)
	testutil.Equal(t, app.mode, modeTaskList)
	testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks)
	testutil.True(t, app.tapp.GetFocus() == tview.Primitive(app.tasklist))
}

func TestJumpSwitcher_OpensFromSettingsTab(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.switchTab(widget.TabSettings)
	testutil.Nil(t, app.handleGlobalKey(ctrlJ()))
	testutil.Equal(t, app.mode, modeJumpSwitcher)
	switcherKey(app, tcell.KeyCtrlQ, 0)
	testutil.Equal(t, app.mode, modeTaskList)
	testutil.True(t, app.tapp.GetFocus() == tview.Primitive(app.settingsPage))
}

// TestJumpSwitcher_OpensFromEveryProjectsFocus: rail, coordinator pane, agent
// pane and fullscreen — the key is consumed by handleGlobalKey (returns nil),
// so tview never hands it to the focused HeraPage/pane PTY.
func TestJumpSwitcher_OpensFromEveryProjectsFocus(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	app := New(d, agent.NewRunner(nil), false)
	app.switchTab(widget.TabHera)
	app.heraPage.Rail().EnsureAncestorsExpanded(orch)
	testutil.True(t, app.heraPage.Rail().SelectByTaskID("tw"))

	cases := []struct {
		name  string
		setup func()
		want  hera.Focus
	}{
		{"rail", func() {}, hera.FocusRail},
		{"coordinator pane", func() { app.heraPage.Machine().SetRegion(hera.FocusCoord) }, hera.FocusCoord},
		{"agent pane", func() { app.heraPage.Machine().SetRegion(hera.FocusAgent) }, hera.FocusAgent},
		{"fullscreen agent pane", func() {
			app.heraPage.Machine().SetRegion(hera.FocusAgent)
			app.heraPage.Machine().ToggleFullscreen()
		}, hera.FocusAgent},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			app.heraPage.Machine().ToRail()
			tc.setup()
			testutil.Nil(t, app.handleGlobalKey(ctrlJ()))
			testutil.Equal(t, app.mode, modeJumpSwitcher)
			// The popup's start cursor is the rail's selected task (Projects).
			testutil.Equal(t, app.jumpSwitcher.ActiveColumn(), SwitcherColumnProjects)
			testutil.Equal(t, app.jumpSwitcher.rail.Selection().FocusTaskID(), "tw")
			switcherKey(app, tcell.KeyEscape, 0)
			testutil.Equal(t, app.mode, modeTaskList)
			testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
			testutil.Equal(t, app.heraPage.Machine().State(), tc.want)
			testutil.True(t, app.tapp.GetFocus() == tview.Primitive(app.heraPage))
		})
	}
}

func TestJumpSwitcher_NotOpenedOverNewTaskForm(t *testing.T) {
	d := testDB(t)
	testutil.NoError(t, d.SetProject("test", config.Project{Path: t.TempDir()}))
	app := New(d, agent.NewRunner(nil), false)
	app.onNewTask()
	testutil.Equal(t, app.mode, modeNewTask)
	app.handleGlobalKey(ctrlJ())
	testutil.Equal(t, app.mode, modeNewTask)
	testutil.Nil(t, app.jumpSwitcher)
}

func TestJumpSwitcher_NotOpenedInPluginView(t *testing.T) {
	app, _ := pluginSurrenderApp(t)
	ev := ctrlJ()
	testutil.True(t, app.handleGlobalKey(ev) == ev) // forwarded to the plugin
	testutil.Equal(t, app.mode, modePluginView)
	testutil.Nil(t, app.jumpSwitcher)
}

func TestJumpSwitcher_CtrlJWhileOpenIsNoop(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.handleGlobalKey(ctrlJ())
	p := app.jumpSwitcher
	testutil.Nil(t, app.handleGlobalKey(ctrlJ()))
	testutil.True(t, app.jumpSwitcher == p)
	testutil.Equal(t, app.mode, modeJumpSwitcher)
	testutil.False(t, p.Canceled() || p.Selected())
	// A direct second open (e.g. from a stale caller) is also a no-op.
	app.openJumpSwitcher()
	testutil.True(t, app.jumpSwitcher == p)
}

// TestJumpSwitcher_CtrlGInsidePopupDoesNotJump: with a Projects role needing
// input, ctrl+g inside the popup cycles the popup's cursor and never fires
// the global jump (which would switch to the Projects tab).
func TestJumpSwitcher_CtrlGInsidePopupDoesNotJump(t *testing.T) {
	d := testDB(t)
	seedNeedsInputWorker(t, d, "orch", "tw")
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.needsInputIDs = []string{"ts-zeta"}
	app.tasklist.SetNeedsInput(app.needsInputIDs)
	app.heraPage.SetNeedsInput([]string{"tw"})

	app.handleGlobalKey(ctrlJ())
	p := app.jumpSwitcher
	testutil.Equal(t, p.ActiveColumn(), SwitcherColumnTasks)
	testutil.Equal(t, p.tasks.SelectedTask().ID, "ts-zeta") // first (?)

	testutil.Nil(t, app.handleGlobalKey(tcell.NewEventKey(tcell.KeyCtrlG, 0, tcell.ModNone)))
	testutil.Equal(t, app.mode, modeJumpSwitcher)
	testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks)
	testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
	testutil.Equal(t, p.rail.Selection().FocusTaskID(), "tw")
	testutil.Equal(t, app.header.Notice(), "")
}

// TestJumpSwitcher_ConfigOverrideAppliesEverywhere: global.switcher rebound to
// ctrl+e opens from the Tasks tab, the agent view, and the Projects rail;
// ctrl+j no longer does; the help overlay shows the new key.
func TestJumpSwitcher_ConfigOverrideAppliesEverywhere(t *testing.T) {
	d := testDBWithConfig(t, "[keybindings.global]\nswitcher = \"ctrl+e\"\n")
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	ctrlE := func() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyCtrlE, 0, tcell.ModNone) }

	app.handleGlobalKey(ctrlJ())
	testutil.Nil(t, app.jumpSwitcher)

	contexts := []struct {
		name  string
		enter func()
	}{
		{"tasks tab", func() { app.switchTab(widget.TabTasks) }},
		{"projects rail", func() { app.switchTab(widget.TabHera) }},
		{"agent view", func() {
			cur, err := d.Get("ts-cur")
			testutil.NoError(t, err)
			app.switchTab(widget.TabTasks)
			enterAgentViewFor(app, cur, nil)
		}},
	}
	for _, c := range contexts {
		t.Run(c.name, func(t *testing.T) {
			c.enter()
			testutil.Nil(t, app.handleGlobalKey(ctrlE()))
			testutil.Equal(t, app.mode, modeJumpSwitcher)
			switcherKey(app, tcell.KeyEscape, 0)
			testutil.Nil(t, app.jumpSwitcher)
		})
	}

	found := false
	for _, sec := range modal.SectionsFromKeymap(app.activeKeymap()) {
		for _, b := range sec.Bindings {
			if b.Action == "jump switcher (tasks + projects)" {
				found = true
				testutil.Equal(t, b.Key, "ctrl+e")
			}
		}
	}
	testutil.True(t, found)
	_, ok := app.activeKeymap().Resolve(keymap.CtxGlobal, ctrlJ())
	testutil.False(t, ok)
}

// --- 1.4: Enter routing ---

func TestJumpSwitcher_EnterOnTaskRow(t *testing.T) {
	t.Run("from the Tasks tab opens the agent view without restarting", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)
		seedSwitcherTasks(t, app)
		app.tasklist.SelectByID("ts-cur")
		app.openJumpSwitcher()
		switcherType(app, "zeta")
		switcherKey(app, tcell.KeyEnter, 0)

		testutil.Nil(t, app.jumpSwitcher)
		testutil.Equal(t, app.mode, modeAgent)
		testutil.Equal(t, app.agentState.TaskID, "ts-zeta")
		testutil.Equal(t, app.tasklist.SelectedTask().ID, "ts-zeta")
		testutil.Nil(t, runner.Get("ts-zeta")) // autoStart=false: nothing started
	})

	t.Run("from the agent view switches tasks", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)
		seedSwitcherTasks(t, app)
		cur, err := d.Get("ts-cur")
		testutil.NoError(t, err)
		enterAgentViewFor(app, cur, nil)
		app.openJumpSwitcher()
		switcherType(app, "alpha")
		switcherKey(app, tcell.KeyEnter, 0)
		testutil.Equal(t, app.mode, modeAgent)
		testutil.Equal(t, app.agentState.TaskID, "ts-alpha")
		testutil.Equal(t, app.tasklist.SelectedTask().ID, "ts-alpha")
	})

	t.Run("the task already being viewed just closes", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		d := testDB(t)
		runner := agent.NewRunner(nil)
		app := New(d, runner, false)
		seedSwitcherTasks(t, app)
		sess := startSleepSession(t, app, runner, "ts-cur")
		cur, err := d.Get("ts-cur")
		testutil.NoError(t, err)
		enterAgentViewFor(app, cur, sess)
		app.openJumpSwitcher()
		switcherKey(app, tcell.KeyEnter, 0) // start cursor = the viewed task
		testutil.Equal(t, app.mode, modeAgent)
		testutil.Equal(t, app.agentState.TaskID, "ts-cur")
		testutil.True(t, app.agentPane.Session() == sess) // not re-bound
	})

	t.Run("from the Projects tab lands on the Tasks tab's agent view", func(t *testing.T) {
		t.Setenv("HOME", t.TempDir())
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)
		seedSwitcherTasks(t, app)
		app.switchTab(widget.TabHera)
		app.openJumpSwitcher()
		switcherType(app, "zeta")
		switcherKey(app, tcell.KeyEnter, 0)
		testutil.Equal(t, app.mode, modeAgent)
		testutil.Equal(t, app.agentState.TaskID, "ts-zeta")
		testutil.Equal(t, app.header.ActiveTab(), widget.TabTasks)
	})

	t.Run("a vanished task is a no-op close", func(t *testing.T) {
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)
		seedSwitcherTasks(t, app)
		app.routeJumpSwitcherSelection(SwitcherSelection{Column: SwitcherColumnTasks, TaskID: "gone"})
		testutil.Equal(t, app.mode, modeTaskList)
	})
}

// TestJumpSwitcher_EnterOnProjectsRoleFromAgentView ports the intent of the
// retired TestSwitcher_SelectingHeraManagedEntryJumpsIntoHeraTab: a Projects
// role nested under a COLLAPSED coordinator, chosen from the classic agent
// view, tears the agent view down (exitAgentView before switchTab(TabHera):
// header restored, session detached, worktreeDir cleared), expands the real
// rail's ancestor, and lands selection + focus on the role's pane.
func TestJumpSwitcher_EnterOnProjectsRoleFromAgentView(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	seedSwitcherTasks(t, app)

	// Collapse the coordinator in the REAL rail.
	rail := app.heraPage.Rail()
	app.heraPage.Refresh()
	testutil.True(t, rail.SelectOrch(orch))
	if !rail.OrchCollapsed(orch) {
		rail.ToggleCollapse()
	}
	testutil.True(t, rail.OrchCollapsed(orch))

	sess := startSleepSession(t, app, runner, "ts-cur")
	cur, err := d.Get("ts-cur")
	testutil.NoError(t, err)
	enterAgentViewFor(app, cur, sess)

	app.openJumpSwitcher()
	p := app.jumpSwitcher
	testutil.True(t, p.rail.OrchCollapsed(orch)) // folds seeded from the real rail
	// Navigate the popup's PRIVATE copy to the buried role.
	p.rail.EnsureAncestorsExpanded(orch)
	testutil.True(t, p.rail.SelectByTaskID("tw"))
	switcherKey(app, tcell.KeyRight, 0)
	testutil.Equal(t, p.ActiveColumn(), SwitcherColumnProjects)
	switcherKey(app, tcell.KeyEnter, 0)

	testutil.Nil(t, app.jumpSwitcher)
	testutil.Equal(t, app.mode, modeTaskList)
	testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
	testutil.False(t, rail.OrchCollapsed(orch))
	testutil.Equal(t, app.heraPage.SelectionContext().TaskID(), "tw")
	testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusAgent)
	testutil.Nil(t, app.agentPane.Session())
	testutil.Equal(t, app.worktreeDir, "")

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 40)
	app.root.SetRect(0, 0, 120, 40)
	app.root.Draw(sim)
	_, _, headerW, headerH := app.header.GetRect()
	if headerW == 0 || headerH == 0 {
		t.Errorf("expected the tab header restored after jumping into Projects from the agent view, got %dx%d", headerW, headerH)
	}
}

// TestJumpSwitcher_EnterOnTasklessProjectsRow: a coordinator-less
// orchestrator header has no task of its own, so Enter lands on the Projects
// tab with that orchestrator row selected (HeraPage.SelectOrch).
func TestJumpSwitcher_EnterOnTasklessProjectsRow(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "lonely")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	app := New(d, agent.NewRunner(nil), false)

	app.openJumpSwitcher()
	p := app.jumpSwitcher
	testutil.True(t, p.rail.SelectOrch(orch))
	testutil.Equal(t, p.rail.Selection().FocusTaskID(), "")
	switcherKey(app, tcell.KeyRight, 0)
	switcherKey(app, tcell.KeyEnter, 0)

	testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
	testutil.Equal(t, app.heraPage.Rail().SelectedOrch().ID, orch)
	testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusRail)
}

func TestJumpSwitcher_ProjectsSelectionWithNoRowStaysOnProjectsTab(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.routeJumpSwitcherSelection(SwitcherSelection{Column: SwitcherColumnProjects, TaskID: "nope", OrchID: 4242})
	testutil.Equal(t, app.mode, modeTaskList)
	testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
}

// TestJumpSwitcher_RealWidgetsUntouched: moving, folding and filtering inside
// the popup, then Esc, leaves the real Tasks list and Projects rail cursor and
// fold state exactly as they were.
func TestJumpSwitcher_RealWidgetsUntouched(t *testing.T) {
	d := testDB(t)
	orchA := seedHeraOrch(t, d, "orch-a")
	seedHeraBoundRole(t, d, orchA, "coord-a", db.HeraKindCoordinator, "tca")
	seedHeraBoundRole(t, d, orchA, "wkr-a", db.HeraKindWorker, "twa")
	orchB := seedHeraOrch(t, d, "orch-b")
	seedHeraBoundRole(t, d, orchB, "coord-b", db.HeraKindCoordinator, "tcb")
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.tasklist.SelectByID("ts-alpha")
	rail := app.heraPage.Rail()
	app.heraPage.Refresh()
	testutil.True(t, rail.SelectOrch(orchB))

	before := struct {
		task, expanded string
		cursor         int
		collA, collB   bool
		kanban         db.HeraKanbanStatus
	}{app.tasklist.SelectedTask().ID, app.tasklist.Expanded(), rail.CursorIndex(),
		rail.OrchCollapsed(orchA), rail.OrchCollapsed(orchB), rail.FocusedKanban()}

	app.openJumpSwitcher()
	switcherKey(app, tcell.KeyDown, 0)
	switcherKey(app, tcell.KeyUp, 0)
	switcherKey(app, tcell.KeyRight, 0)
	switcherKey(app, tcell.KeyUp, 0)
	switcherKey(app, tcell.KeyRune, ' ')
	switcherKey(app, tcell.KeyDown, 0)
	switcherKey(app, tcell.KeyRune, ' ')
	switcherType(app, "wkr")
	switcherKey(app, tcell.KeyEscape, 0) // clears the query
	testutil.True(t, app.jumpSwitcher != nil)
	switcherKey(app, tcell.KeyEscape, 0) // closes
	testutil.Nil(t, app.jumpSwitcher)

	testutil.Equal(t, app.tasklist.SelectedTask().ID, before.task)
	testutil.Equal(t, app.tasklist.Expanded(), before.expanded)
	testutil.Equal(t, rail.CursorIndex(), before.cursor)
	testutil.Equal(t, rail.OrchCollapsed(orchA), before.collA)
	testutil.Equal(t, rail.OrchCollapsed(orchB), before.collB)
	testutil.Equal(t, rail.FocusedKanban(), before.kanban)
	testutil.Equal(t, rail.Filter(), "")
}

// --- open-time feed, refeed, spinner, palette ---

func TestJumpSwitcher_FeedExcludesManagedAndArchived(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.tasklist.SetHideHeraManaged(false) // the real list's H toggle is off
	app.openJumpSwitcher()
	ids := app.jumpSwitcher.taskIDs
	testutil.True(t, ids["ts-cur"] && ids["ts-zeta"] && ids["ts-alpha"])
	testutil.False(t, ids["tc"] || ids["tw"] || ids["ts-arch"])
	// The Projects column got the fresh Hera model.
	testutil.Equal(t, len(app.jumpSwitcher.rail.Model().Active), 1)
}

func TestJumpSwitcher_RefeedsOnTick(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.openJumpSwitcher()
	testutil.False(t, app.jumpSwitcher.taskIDs["ts-new"])
	testutil.NoError(t, d.Add(&model.Task{ID: "ts-new", Name: "new one", Status: model.StatusPending, Project: "p", CreatedAt: time.Now()}))
	app.refreshTasks()
	testutil.True(t, app.jumpSwitcher.taskIDs["ts-new"])
	// Refeed is a no-op once closed.
	switcherKey(app, tcell.KeyEscape, 0)
	app.refeedJumpSwitcher()
	testutil.Nil(t, app.jumpSwitcher)
}

// TestJumpSwitcher_SpinnerKeepsAnimating: opened from the Tasks tab, the
// spinner redraw gate stops being Tasks-tab-scoped, so a running task that is
// not on the visible Tasks rows (e.g. a hera worker) still drives redraws.
func TestJumpSwitcher_SpinnerKeepsAnimating(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.runningIDs = []string{"not-visible"}
	app.idleIDs = nil
	testutil.False(t, app.computeVisibleActiveSpinner(app.runningIDs, app.idleIDs))
	app.openJumpSwitcher()
	testutil.True(t, app.visibleActiveSpinner)
}

func TestJumpSwitcher_CommandPaletteOpensIt(t *testing.T) {
	t.Run("tasks tab global row", func(t *testing.T) {
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)
		app.globalActionRegistry()[keymap.ActGlobalSwitcher]()
		testutil.Equal(t, app.mode, modeJumpSwitcher)
	})
	t.Run("agent view row", func(t *testing.T) {
		d := testDB(t)
		app := New(d, agent.NewRunner(nil), false)
		app.mode = modeAgent
		var invoke func()
		for _, r := range app.paletteApplicableActions() {
			if r.Label == "jump switcher (tasks + projects)" {
				testutil.Equal(t, r.Key, "ctrl+j")
				invoke = r.invoke
			}
		}
		testutil.True(t, invoke != nil)
		invoke()
		testutil.Equal(t, app.mode, modeJumpSwitcher)
		testutil.Equal(t, app.jumpSwitcherReturnMode, modeAgent)
	})
}

// --- smoke: real event loop reach ---

func TestSmoke_JumpSwitcherOpensFromHeraPaneViaEventLoop(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		app.heraPage.Machine().Advance() // rail → coordinator pane
		testutil.Equal(t, app.heraPaneFocused(), true)
	})
	sim.InjectKey(tcell.KeyCtrlJ, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeJumpSwitcher) })

	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Equal(t, app.heraPage.Machine().State(), hera.FocusCoord)
	})
}

// TestJumpSwitcher_TransparentToAgentViewSession: while the popup covers the
// classic agent view, isViewingTaskSession still reports the viewed task (so a
// size-drift kick restart is not skipped as "navigated away"), and closing the
// popup re-attaches the runner's current session and restarts the 200ms
// redraw loop that exited while a.mode was modeJumpSwitcher.
func TestJumpSwitcher_TransparentToAgentViewSession(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	runner := agent.NewRunner(nil)
	app := New(d, runner, false)
	seedSwitcherTasks(t, app)
	sess := startSleepSession(t, app, runner, "ts-cur")
	cur, err := d.Get("ts-cur")
	testutil.NoError(t, err)
	enterAgentViewFor(app, cur, nil) // pane not attached, as after a skipped startSession attach

	app.openJumpSwitcher()
	testutil.True(t, app.isViewingTaskSession("ts-cur"))
	testutil.False(t, app.isViewingTaskSession("ts-zeta"))
	gen := app.redrawLoopGen["ts-cur"]

	switcherKey(app, tcell.KeyEscape, 0)
	testutil.Equal(t, app.mode, modeAgent)
	testutil.True(t, app.agentPane.Session() == sess)
	testutil.Equal(t, app.redrawLoopGen["ts-cur"], gen+1)

	t.Run("no session for the viewed task is a no-op", func(t *testing.T) {
		zeta, err := d.Get("ts-zeta")
		testutil.NoError(t, err)
		enterAgentViewFor(app, zeta, nil)
		app.openJumpSwitcher()
		switcherKey(app, tcell.KeyEscape, 0)
		testutil.Nil(t, app.agentPane.Session())
		testutil.Equal(t, app.redrawLoopGen["ts-zeta"], uint64(0))
	})
}
