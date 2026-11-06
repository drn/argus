package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/config"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/notify"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

type tasksRailStartRunner struct {
	*agent.Runner
	starts int
}

func (r *tasksRailStartRunner) Start(*model.Task, config.Config, uint16, uint16, bool) (agent.SessionHandle, error) {
	r.starts++
	return nil, errors.New("recorded start attempt")
}

func TestSmoke_TasksRailSelectionRefreshAndFocusClaims(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", "true"))
	for _, id := range []string{"a", "b"} {
		testutil.NoError(t, d.Add(&model.Task{ID: id, Name: id, Project: "A", Status: model.StatusComplete}))
	}
	app := New(d, agent.NewRunner(nil), false)
	tracker := notify.NewFocusTracker(nil)
	app.SetFocusTracker(tracker)
	sim, stop := wireApp(t, app)
	defer stop()
	app.tapp.QueueUpdateDraw(func() { sim.SetSize(140, 40); app.tasklist.SelectByID("a") })
	syncUI(t, app.tapp)
	testutil.Equal(t, tracker.IsFocused("a"), false)
	sim.InjectKey(tcell.KeyTab, 0, 0)
	syncUI(t, app.tapp)
	testutil.Equal(t, tracker.IsFocused("a"), true)
	app.tapp.QueueUpdateDraw(func() { testutil.NoError(t, d.Delete("a")); app.refreshTasksLocal() })
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, tracker.IsFocused("a"), false)
		testutil.Equal(t, app.mode, modeTaskList)
		testutil.Equal(t, app.agentState.TaskID, "b")
		app.tasklist.SetFilter("unmatched")
	})
	// Filter reconciliation also runs through the normal UI input path.
	app.tapp.QueueUpdateDraw(func() { app.reconcileTasksRail() })
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.agentState.TaskID, ""); testutil.Nil(t, app.agentPane.Session()) })
}

func TestTasksRailExpansionPersistsFromAction(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	for _, id := range []string{"a", "b"} {
		testutil.NoError(t, d.Add(&model.Task{ID: id, Name: id, Project: id, Status: model.StatusComplete}))
	}
	app := New(d, agent.NewRunner(nil), false)
	app.tasklist.InputHandler()(tcell.NewEventKey(tcell.KeyRune, 'F', 0), func(tview.Primitive) {})
	value, err := d.LoadTasksExpandAll()
	testutil.NoError(t, err)
	testutil.Equal(t, value, true)
	next := New(d, agent.NewRunner(nil), false)
	testutil.Equal(t, next.tasklist.ExpandAll(), true)
	testutil.Equal(t, len(next.tasklist.VisibleTaskIDs()), 2)
}

func TestTasksRailBrowseNeverStartsEligibleTask(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", "true"))
	testutil.NoError(t, d.Add(&model.Task{ID: "eligible", Name: "eligible", Project: "A", Status: model.StatusPending}))
	runner := &tasksRailStartRunner{Runner: agent.NewRunner(nil)}
	app := New(d, runner, false)
	app.tasklist.SelectByID("eligible")
	testutil.Equal(t, runner.starts, 0)
	testutil.Equal(t, app.agentState.TaskID, "eligible")
	testutil.Equal(t, app.mode, modeTaskList)
	app.onTaskSelect(app.tasklist.SelectedTask(), true)
	testutil.Equal(t, runner.starts, 1)
}

func TestTasksRailDisableActiveAndGuards(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", "true"))
	testutil.NoError(t, d.Add(&model.Task{ID: "a", Project: "A", Status: model.StatusComplete}))
	app := New(d, agent.NewRunner(nil), false)
	app.tasksRailPage.SetRect(0, 0, 120, 30)
	sim := drawSim(t)
	app.tasksRailPage.Draw(sim)
	app.tasklist.SelectByID("a")
	app.enterTasksRailTerminal()
	testutil.Equal(t, app.mode, modeAgent)
	app.setTasksRail(false)
	testutil.Equal(t, app.mode, modeAgent)
	page, _ := app.pages.GetFrontPage()
	testutil.Equal(t, page, "agent")
	testutil.Equal(t, app.agentState.TaskID, "a")
	app.setTasksRail(true)
	testutil.Equal(t, app.mode, modeAgent)
	app.focusTasksRail()
	app.tasklist.InputHandler()(tcell.NewEventKey(tcell.KeyRune, '/', 0), func(tview.Primitive) {})
	testutil.Equal(t, app.handleTasksRailKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModAlt)), false)
	app.tasklist.ClearFilter()
	testutil.Equal(t, app.handleTasksRailKey(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt)), true)
	testutil.Equal(t, app.mode, modeAgent)
	testutil.Equal(t, app.handleTasksRailKey(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl|tcell.ModAlt)), true)
	testutil.Equal(t, app.mode, modeTaskList)
}

func TestSmoke_TasksRailTerminalInputAndMouse(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", "true"))
	testutil.NoError(t, d.Add(&model.Task{ID: "input", Name: "input", Project: "A", Status: model.StatusComplete}))
	sess := &recordingWriteSession{fakeKickSession: &fakeKickSession{alive: true}}
	runner := &fakeInputRunner{Runner: agent.NewRunner(nil), sessions: map[string]agent.SessionHandle{"input": sess}}
	app := New(d, runner, false)
	sim, stop := wireApp(t, app)
	defer stop()
	processed := make(chan struct{}, 1)
	app.tapp.QueueUpdateDraw(func() {
		app.tapp.SetInputCapture(func(e *tcell.EventKey) *tcell.EventKey {
			if e.Key() == tcell.KeyF12 {
				processed <- struct{}{}
				return nil
			}
			return app.handleGlobalKey(e)
		})
	})
	app.tapp.QueueUpdateDraw(func() { sim.SetSize(160, 40); app.tasklist.SelectByID("input") })
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyTab, 0, 0)
	syncUI(t, app.tapp)
	for _, key := range []tcell.Key{tcell.KeyTab, tcell.KeyBacktab} {
		sim.InjectKey(key, 0, 0)
		syncUI(t, app.tapp)
	}
	for _, r := range "12q?F" {
		app.tapp.QueueEvent(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	app.tapp.QueueEvent(tcell.NewEventPaste(true))
	for _, r := range "paste safely" {
		app.tapp.QueueEvent(tcell.NewEventKey(tcell.KeyRune, r, 0))
	}
	app.tapp.QueueEvent(tcell.NewEventPaste(false))
	app.tapp.QueueEvent(tcell.NewEventKey(tcell.KeyF12, 0, 0))
	select {
	case <-processed:
	case <-time.After(uiTimeout):
		t.Fatal("paste events did not drain")
	}
	readUI(t, app.tapp, func() {
		var written strings.Builder
		for _, b := range sess.writes {
			written.Write(b)
		}
		testutil.Contains(t, written.String(), "\t")
		testutil.Contains(t, written.String(), "\x1b[Z")
		testutil.Contains(t, written.String(), "12q?")
		testutil.Contains(t, written.String(), "\x1b[200~paste safely\x1b[201~")
		for _, origin := range sess.origins {
			testutil.Equal(t, origin, agentview.OriginUser)
		}
		testutil.Equal(t, app.mode, modeAgent)
		testutil.Equal(t, app.tasklist.ExpandAll(), false)
	})
	// The first click on the rail focuses it; subsequent navigation must work.
	app.tapp.QueueEvent(tcell.NewEventMouse(5, 5, tcell.Button1, 0))
	app.tapp.QueueEvent(tcell.NewEventMouse(5, 5, tcell.ButtonNone, 0))
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList) })
	// Clicking non-interactive git chrome must not steal the wrapper's focus.
	app.tapp.QueueUpdateDraw(func() { app.clearAgentZen() })
	syncUI(t, app.tapp)
	var gx, gy int
	readUI(t, app.tapp, func() { gx, gy, _, _ = app.gitPanel.GetRect() })
	app.tapp.QueueEvent(tcell.NewEventMouse(gx+2, gy+2, tcell.Button1, 0))
	app.tapp.QueueEvent(tcell.NewEventMouse(gx+2, gy+2, tcell.ButtonNone, 0))
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.tapp.GetFocus(), tview.Primitive(app.tasksRailPage)) })
}

func TestSmoke_TasksRailFocusAndLayout(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", "true"))
	for _, task := range []*model.Task{{ID: "rail-a", Name: "First", Project: "A", Status: model.StatusComplete, CreatedAt: time.Now()}, {ID: "rail-b", Name: "Second", Project: "B", Status: model.StatusComplete, CreatedAt: time.Now()}} {
		testutil.NoError(t, d.Add(task))
	}
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()
	app.tapp.QueueUpdateDraw(func() { sim.SetSize(140, 40); app.switchTab(widget.TabTasks); app.tasklist.SelectByID("rail-a") })
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.agentState.TaskID, "rail-a")
		testutil.Equal(t, app.mode, modeTaskList)
		_, _, rw, _ := app.tasklist.GetRect()
		testutil.Equal(t, rw, 34)
		rows, cols := app.computePTYSize()
		testutil.Equal(t, rows, uint16(35))
		testutil.Equal(t, cols, uint16(104))
	})
	sim.InjectKey(tcell.KeyEnter, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeAgent); testutil.Nil(t, app.runner.Get("rail-a")) })
	sim.InjectKey(tcell.KeyCtrlQ, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.mode, modeTaskList); testutil.Equal(t, app.agentState.TaskID, "rail-a") })
	app.tapp.QueueUpdateDraw(func() { sim.SetSize(30, 20) })
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		testutil.Equal(t, app.tasksRailPage.ContentVisible(), false)
		testutil.Equal(t, app.mode, modeTaskList)
	})
	app.tapp.QueueUpdateDraw(func() { sim.SetSize(140, 40) })
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { testutil.Equal(t, app.tasksRailPage.ContentVisible(), true) })
	sim.InjectKey(tcell.KeyTab, 0, 0)
	syncUI(t, app.tapp)
	sim.InjectKey(tcell.KeyCtrlZ, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { _, _, rw, _ := app.tasklist.GetRect(); testutil.Equal(t, rw, 34) })
}

func TestTasksRailLiveToggle(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	testutil.NoError(t, d.Add(&model.Task{ID: "toggle", Name: "toggle", Status: model.StatusComplete}))
	app := New(d, agent.NewRunner(nil), false)
	app.tasklist.SelectByID("toggle")
	testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", "true"))
	app.setTasksRail(true)
	testutil.Equal(t, app.tasksRailEnabled, true)
	testutil.Equal(t, app.agentState.TaskID, "toggle")
	app.switchTab(widget.TabSettings)
	testutil.NoError(t, d.SetConfigValue("ui.tasks_rail", "false"))
	app.setTasksRail(false)
	app.switchTab(widget.TabTasks)
	testutil.Equal(t, app.mode, modeTaskList)
	name, _ := app.pages.GetFrontPage()
	testutil.Equal(t, name, "tasks")
}

func TestTasksRailEnablePreservesNavigatedAgent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d := testDB(t)
	for _, id := range []string{"a", "b"} {
		testutil.NoError(t, d.Add(&model.Task{ID: id, Name: id, Project: "A", Status: model.StatusComplete}))
	}
	app := New(d, agent.NewRunner(nil), false)
	app.tasklist.SelectByID("a")
	task, err := d.Get("b")
	testutil.NoError(t, err)
	app.onTaskSelect(task, false)
	testutil.Equal(t, app.tasklist.SelectedTask().ID, "a")
	app.setTasksRail(true)
	testutil.Equal(t, app.agentState.TaskID, "b")
	testutil.Equal(t, app.tasklist.SelectedTask().ID, "b")
}
