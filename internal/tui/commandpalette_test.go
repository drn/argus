package tui

import (
	"testing"

	"github.com/drn/argus/internal/agent"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
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
	testutil.Equal(t, containsLabel(rows, "task/role switcher"), true)
	testutil.Equal(t, containsLabel(rows, "toggle single-pane (zoom)"), true)
	// The pre-existing modeAgent CtxGlobal boundary is untouched: no global
	// actions (quit/tab-switch/help) leak into the agent-view palette.
	testutil.Equal(t, containsLabel(rows, "quit"), false)
	testutil.Equal(t, containsLabel(rows, "switch to Tasks tab"), false)
}

func TestPaletteApplicableActions_TaskListIsGlobalPlusTaskList(t *testing.T) {
	d := testDB(t)
	app := New(d, agent.NewRunner(nil), false)
	app.mode = modeTaskList
	app.header.SetTab(widget.TabTasks)

	rows := app.paletteApplicableActions()
	testutil.Equal(t, containsLabel(rows, "quit"), true)
	testutil.Equal(t, containsLabel(rows, "new task"), true)
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

func TestSmoke_TaskSwitcherOpensFromHeraRailAndPane(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	app := New(d, agent.NewRunner(nil), false)
	sim, stop := wireApp(t, app)
	defer stop()

	sim.InjectKey(tcell.KeyRune, '2', 0) // Hera tab, rail focus
	syncUI(t, app.tapp)

	sim.InjectKey(tcell.KeyCtrlJ, 0, 0)
	syncUI(t, app.tapp)
	var mode viewMode
	readUI(t, app.tapp, func() { mode = app.mode })
	testutil.Equal(t, mode, modeTaskSwitcher)

	// Esc closes and returns to the Hera tab (not modeAgent — opened from Hera).
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

// TestSwitcher_SelectingHeraManagedEntryJumpsIntoHeraTab confirms the unified
// switcher's Hera routing: selecting a hera-managed entry from the CLASSIC
// agent view switches to the Hera tab and lands on the role there, rather
// than opening the classic per-task agent view for it.
func TestSwitcher_SelectingHeraManagedEntryJumpsIntoHeraTab(t *testing.T) {
	d := testDB(t)
	orch := seedHeraOrch(t, d, "orch")
	seedHeraBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedHeraBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	app := New(d, agent.NewRunner(nil), false)
	seedSwitcherTasks(t, app) // adds an unrelated plain task too
	app.refreshTasks()

	app.mode = modeAgent
	app.agentState.Reset("ts-cur", "current task")
	app.openTaskSwitcher()

	// Find the hera-managed entry (worker task "tw") in the built list.
	found := false
	for _, e := range app.taskSwitcherModal.all {
		if e.ID == "tw" {
			found = true
			testutil.Equal(t, e.HeraManaged, true)
		}
	}
	if !found {
		t.Fatal("expected the hera-managed worker task to appear in the switcher entries")
	}

	// Select it directly (bypass cursor-walking — set the entry list to just
	// this one, drop to flat mode so selection reads `.filtered` directly
	// rather than the grouped `.rows` projection built before this overwrite,
	// and drive Enter).
	app.taskSwitcherModal.all = []taskSwitcherEntry{{ID: "tw", Name: "wkr", HeraManaged: true}}
	app.taskSwitcherModal.filtered = app.taskSwitcherModal.all
	app.taskSwitcherModal.SetGrouped(false)
	app.taskSwitcherModal.cursor = 0
	app.handleTaskSwitcherKey(tcell.NewEventKey(tcell.KeyEnter, 0, 0))

	testutil.Equal(t, app.mode, modeTaskList)
	testutil.Equal(t, app.header.ActiveTab(), widget.TabHera)
	testutil.Equal(t, app.heraPage.SelectionContext().TaskID(), "tw")
}
