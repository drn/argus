package tui

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// seedSwitcherTasks adds three tasks (one archived) and refreshes the cache.
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

func TestOpenTaskSwitcher_SortsNeedsInputFirstExcludesArchivedAndCurrent(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)

	app.mode = modeAgent
	app.agentState.Reset("ts-cur", "current task")
	// Zeta needs input — it must jump to the top despite the alpha sort.
	app.needsInputIDs = []string{"ts-zeta"}

	app.openTaskSwitcher()

	testutil.Equal(t, app.mode, modeTaskSwitcher)
	if app.taskSwitcherModal == nil {
		t.Fatal("expected task switcher modal to open")
	}
	all := app.taskSwitcherModal.all
	// Current + archived excluded → 2 entries.
	testutil.Equal(t, len(all), 2)
	// Needs-input first.
	testutil.Equal(t, all[0].ID, "ts-zeta")
	testutil.Equal(t, all[0].NeedsInput, true)
	// Then alphabetical.
	testutil.Equal(t, all[1].ID, "ts-alpha")
	testutil.Equal(t, all[1].NeedsInput, false)
}

func TestOpenTaskSwitcher_AlphabeticalWhenNoNeedsInput(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)

	app.mode = modeAgent
	app.agentState.Reset("ts-cur", "current task")

	app.openTaskSwitcher()

	all := app.taskSwitcherModal.all
	testutil.Equal(t, len(all), 2)
	testutil.Equal(t, all[0].Name, "Alpha work")
	testutil.Equal(t, all[1].Name, "Zeta work")
}

func TestTaskSwitcher_SelectInvokesOnTaskSelect(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)

	app.mode = modeAgent
	app.agentState.Reset("ts-cur", "current task")
	app.openTaskSwitcher()

	// First entry is "Alpha work" (no needs-input set). Enter selects it.
	app.handleTaskSwitcherKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	// Modal closed and the agent view now points at the chosen task.
	testutil.Equal(t, app.mode, modeAgent)
	testutil.Nil(t, app.taskSwitcherModal)
	testutil.Equal(t, app.agentState.TaskID, "ts-alpha")
}

func TestTaskSwitcher_SelectSyncsTaskListCursor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)

	app.mode = modeAgent
	app.agentState.Reset("ts-cur", "current task")
	// Point the task-list cursor at the current task to start.
	app.tasklist.SelectByID("ts-cur")
	app.openTaskSwitcher()

	// First entry is "Alpha work" (ts-alpha). Enter selects it.
	app.handleTaskSwitcherKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, app.agentState.TaskID, "ts-alpha")
	// The task-list cursor must follow so Ctrl+Q lands on the switched-to task.
	sel := app.tasklist.SelectedTask()
	if sel == nil {
		t.Fatal("expected a selected task after switch")
	}
	testutil.Equal(t, sel.ID, "ts-alpha")
}

func TestTaskSwitcher_EscClosesModal(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.mode = modeAgent
	app.agentState.Reset("ts-cur", "current task")
	app.openTaskSwitcher()

	app.handleTaskSwitcherKey(tcell.NewEventKey(tcell.KeyEscape, 0, 0))

	testutil.Equal(t, app.mode, modeAgent)
	testutil.Nil(t, app.taskSwitcherModal)
	// Current task unchanged.
	testutil.Equal(t, app.agentState.TaskID, "ts-cur")
}

func TestTaskSwitcher_SelectMissingTaskIsNoop(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	app.mode = modeAgent
	app.agentState.Reset("ts-cur", "current task")
	app.openTaskSwitcher()

	// Delete the candidate from the DB after the modal cached it, so the
	// post-selection db.Get fails. The switcher must close without switching.
	testutil.NoError(t, d.Delete("ts-alpha"))

	app.handleTaskSwitcherKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, app.mode, modeAgent)
	testutil.Nil(t, app.taskSwitcherModal)
	// Still on the original task — no switch happened.
	testutil.Equal(t, app.agentState.TaskID, "ts-cur")
}

func TestOpenTaskSwitcher_NoOtherTasksOpensEmpty(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	task := &model.Task{ID: "only", Name: "only", Status: model.StatusInProgress, Worktree: t.TempDir(), CreatedAt: time.Now()}
	testutil.NoError(t, d.Add(task))
	app.refreshTasks()

	app.mode = modeAgent
	app.agentState.Reset("only", "only")
	app.openTaskSwitcher()

	// Modal still opens (with an empty list / empty-state message).
	testutil.Equal(t, app.mode, modeTaskSwitcher)
	testutil.Equal(t, len(app.taskSwitcherModal.all), 0)
}

func TestSmoke_TaskSwitcherOpenViaCtrlK(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)

	sim, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		app.agentState.Reset("ts-cur", "current task")
		app.mode = modeAgent
	})

	sim.InjectKey(tcell.KeyCtrlK, 0, 0)
	syncUI(t, app.tapp)

	var mode viewMode
	var count int
	readUI(t, app.tapp, func() {
		mode = app.mode
		if app.taskSwitcherModal != nil {
			count = len(app.taskSwitcherModal.all)
		}
	})
	testutil.Equal(t, mode, modeTaskSwitcher)
	testutil.Equal(t, count, 2)

	// Esc returns to agent view.
	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeAgent)
}
