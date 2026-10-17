package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/keymap"
	"github.com/drn/argus/internal/tui/widget"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func containsLabel(rows []paletteRow, label string) bool {
	for _, r := range rows {
		if r.Label == label {
			return true
		}
	}
	return false
}

// --- CommandPaletteModal (widget-level) ---

func TestCommandPaletteModal_FilterNarrowsPreservingOrder(t *testing.T) {
	rows := []paletteRow{
		{Label: "new task", Key: "n", invoke: func() {}},
		{Label: "switch to Projects tab", Key: "2", invoke: func() {}},
		{Label: "toggle pin", Key: "P", invoke: func() {}},
	}
	m := NewCommandPaletteModal(rows)
	h := m.InputHandler()
	for _, r := range "proj" {
		h(tcell.NewEventKey(tcell.KeyRune, r, tcell.ModNone), func(tview.Primitive) {})
	}
	testutil.Equal(t, len(m.filtered), 1)
	testutil.Equal(t, m.filtered[0].Label, "switch to Projects tab")
}

func TestCommandPaletteModal_EnterInvokesSelectedAndClosesViaApp(t *testing.T) {
	rows := []paletteRow{
		{Label: "a", Key: "a", invoke: func() {}},
	}
	m := NewCommandPaletteModal(rows)
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), true)
	invoked := false
	m.filtered[0].invoke = func() { invoked = true }
	m.Invoke()
	testutil.Equal(t, invoked, true)
}

func TestCommandPaletteModal_EscCancels(t *testing.T) {
	m := NewCommandPaletteModal([]paletteRow{{Label: "a", Key: "a", invoke: func() {}}})
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, m.Canceled(), true)
	testutil.Equal(t, m.Selected(), false)
}

func TestCommandPaletteModal_EnterOnEmptyFilteredIsNoop(t *testing.T) {
	m := NewCommandPaletteModal([]paletteRow{{Label: "a", Key: "a", invoke: func() {}}})
	h := m.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, len(m.filtered), 0)
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, m.Selected(), false) // nothing to select
}

// --- paletteApplicableActions (App-level, no sim needed) ---

func TestPaletteApplicableActions_ClassicAgentViewIsCtxAgentOnly(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeAgent

	rows := app.paletteApplicableActions()
	testutil.Equal(t, containsLabel(rows, "toggle single-pane (zoom)"), true)
	// The jump switcher moved to CtxGlobal (add-two-column-switcher) but still
	// fires in the agent view, so it stays offered here — the one global row.
	testutil.Equal(t, containsLabel(rows, "jump switcher (tasks + projects)"), true)
	// The pre-existing modeAgent CtxGlobal boundary is untouched: no global
	// actions (quit/tab-switch/help) leak into the agent-view palette.
	testutil.Equal(t, containsLabel(rows, "quit"), false)
	testutil.Equal(t, containsLabel(rows, "switch to Tasks tab"), false)
	testutil.Equal(t, containsLabel(rows, "jump to next needs-input (?); jumps back once all clear"), false)
}

func TestPaletteApplicableActions_TaskListIsGlobalPlusTaskList(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeTaskList
	app.header.SetTab(widget.TabTasks)

	rows := app.paletteApplicableActions()
	testutil.Equal(t, containsLabel(rows, "quit"), true)
	testutil.Equal(t, containsLabel(rows, "new task"), true)
	// A new CtxGlobal action (add-hera-jump-question) is reachable here too,
	// like every other global action.
	testutil.Equal(t, containsLabel(rows, "jump to next needs-input (?); jumps back once all clear"), true)
	// No cross-tab bleed: Hera rail actions absent from the Tasks-tab palette.
	testutil.Equal(t, containsLabel(rows, "spawn worker under coordinator (new-task modal)"), false)
}

func TestPaletteApplicableActions_HeraRailIsGlobalPlusHeraRail(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeTaskList
	app.header.SetTab(widget.TabHera)
	app.heraPage.Refresh()

	rows := app.paletteApplicableActions()
	testutil.Equal(t, containsLabel(rows, "quit"), true)
	testutil.Equal(t, containsLabel(rows, "spawn worker under coordinator (new-task modal)"), true)
	// No cross-tab bleed: Tasks-tab-only actions absent.
	testutil.Equal(t, containsLabel(rows, "new task"), false)
	// Rail focus (not a pane) — no Hera literal actions (fullscreen/copy).
	testutil.Equal(t, containsLabel(rows, "toggle fullscreen"), false)
}

func TestPaletteApplicableActions_HeraPaneIncludesLiteralActionsAndHeraRail(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeTaskList
	app.header.SetTab(widget.TabHera)
	app.heraPage.Refresh()
	app.heraPage.Machine().Advance() // rail → coordinator pane

	rows := app.paletteApplicableActions()
	testutil.Equal(t, containsLabel(rows, "toggle fullscreen"), true)
	testutil.Equal(t, containsLabel(rows, "quit"), true) // global still unioned in (deliberate pick, not accidental typing)
	testutil.Equal(t, containsLabel(rows, "spawn worker under coordinator (new-task modal)"), true)
	testutil.Equal(t, containsLabel(rows, "new task"), false) // still no cross-tab bleed
}

func TestPaletteApplicableActions_HeraDetailsModeExcludesCopy(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	// Coordinator-only orchestrator: selecting the header is detailsMode (no
	// worker, so the agent region shows Details/plan, not a terminal).
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeTaskList
	app.header.SetTab(widget.TabHera)
	app.heraPage.Refresh()
	app.heraPage.Machine().Advance() // rail → coord
	app.heraPage.Machine().Advance() // coord → agent/details

	rows := app.paletteApplicableActions()
	testutil.Equal(t, containsLabel(rows, "toggle fullscreen"), true)
	testutil.Equal(t, containsLabel(rows, "copy staged clipboard"), false)
}

// --- Smoke tests (real key dispatch through handleGlobalKey/HeraPage) ---

func TestSmoke_CommandPaletteOpensFromFullscreenAgentView(t *testing.T) {
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
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeCommandPalette)

	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeAgent)
}

func TestSmoke_CommandPaletteOpensFromHeraRailAndPane(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0) // Hera tab, rail focus
	syncUI(t, app.tapp)

	sim.InjectKey(tcell.KeyCtrlK, 0, 0)
	syncUI(t, app.tapp)
	var mode viewMode
	var rowCount int
	readUI(t, app.tapp, func() {
		mode = app.mode
		if app.commandPaletteModal != nil {
			rowCount = len(app.commandPaletteModal.all)
		}
	})
	testutil.Equal(t, mode, modeCommandPalette)
	if rowCount == 0 {
		t.Fatal("expected at least one applicable action from Hera rail focus")
	}

	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() {
		mode = app.mode
		if app.header.ActiveTab() != widget.TabHera {
			t.Fatalf("expected to stay on the Hera tab, got %v", app.header.ActiveTab())
		}
	})
	testutil.Equal(t, mode, modeTaskList)
}

// --- Synthetic-event registries (CtxTaskList/CtxSettings) actually fire the
// real widget dispatch, and go inert during text-input mode ---

// TestTaskListActionRegistry_InvokeFiresRealAction confirms the synthesized
// tcell.EventKey actually reaches TaskListView's own InputHandler (not a
// stub) — invoking ActTaskNew through the registry opens the real new-task
// form, exactly as pressing `n` would.
func TestTaskListActionRegistry_InvokeFiresRealAction(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeTaskList
	app.header.SetTab(widget.TabTasks)

	reg := app.taskListActionRegistry()
	fn, ok := reg[keymap.ActTaskNew]
	if !ok {
		t.Fatal("expected ActTaskNew to be registered")
	}
	fn()
	testutil.Equal(t, app.mode, modeNewTask)
}

// TestTaskListActionRegistry_NilWhileFiltering guards against a synthesized
// action key silently corrupting the `/` filter query instead of firing the
// action (the registry must go fully inert while filtering).
func TestTaskListActionRegistry_NilWhileFiltering(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	// Enter filter mode via the real InputHandler (the same `/` keypress a
	// user would type) rather than poking private state.
	app.tasklist.InputHandler()(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), func(tview.Primitive) {})
	testutil.Equal(t, app.tasklist.Filtering(), true)

	if reg := app.taskListActionRegistry(); reg != nil {
		t.Fatal("expected a nil registry while the task list is filtering")
	}
}

// TestSettingsActionRegistry_InvokeFiresRealAction mirrors the task-list
// case for SettingsView.HandleKey: invoking ActSettingsQuickAdd through the
// registry fires the real OnQuickAdd callback (category gated, exactly as a
// physical `i` keypress would be).
func TestSettingsActionRegistry_InvokeFiresRealAction(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.settings.category = catProjects
	fired := false
	app.settings.OnQuickAdd = func() { fired = true }

	reg := app.settingsActionRegistry()
	fn, ok := reg[keymap.ActSettingsQuickAdd]
	if !ok {
		t.Fatal("expected ActSettingsQuickAdd to be registered")
	}
	fn()
	testutil.Equal(t, fired, true)
}

// TestSettingsActionRegistry_NilWhileEditing mirrors the task-list filtering
// guard: a synthesized action key must not reach an in-progress inline edit.
func TestSettingsActionRegistry_NilWhileEditing(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.settings.editingBackendModel = "some-backend"
	testutil.Equal(t, app.settings.IsEditing(), true)

	if reg := app.settingsActionRegistry(); reg != nil {
		t.Fatal("expected a nil registry while a settings field is being edited")
	}
}

// TestSmoke_CommandPaletteOpensFromPlainTaskList is the real end-to-end
// dispatch regression the unit-level TestPaletteApplicableActions_* tests
// didn't cover: driving ctrl+k through the actual handleGlobalKey SetInputCapture
// path from the plain Tasks tab (no Hera, no agent view) — the exact gap a
// live QA pass found dead.
func TestSmoke_CommandPaletteOpensFromPlainTaskList(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app)
	sim, stop := wireApp(t, app)
	defer stop()

	readUI(t, app.tapp, func() {
		if app.mode != modeTaskList || app.header.ActiveTab() != widget.TabTasks {
			t.Fatalf("setup: expected modeTaskList/TabTasks, got mode=%v tab=%v", app.mode, app.header.ActiveTab())
		}
	})

	sim.InjectKey(tcell.KeyCtrlK, 0, 0)
	syncUI(t, app.tapp)

	var mode viewMode
	var rowCount int
	readUI(t, app.tapp, func() {
		mode = app.mode
		if app.commandPaletteModal != nil {
			rowCount = len(app.commandPaletteModal.all)
		}
	})
	testutil.Equal(t, mode, modeCommandPalette)
	if rowCount == 0 {
		t.Fatal("expected at least one applicable action from the plain Tasks tab")
	}

	sim.InjectKey(tcell.KeyEscape, 0, 0)
	syncUI(t, app.tapp)
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeTaskList)
}
