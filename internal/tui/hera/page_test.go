package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

func TestHeraPage_LocalRefreshPopulatesRail(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")

	p := NewHeraPage(d)
	testutil.Equal(t, p.IsRemote(), false)
	p.Refresh() // first refresh fires immediately

	testutil.Equal(t, len(p.Rail().Model().Active), 1)
	testutil.Equal(t, p.Rail().Model().Active[0].Name, "orch")
}

// TestHeraPage_SetNeedsInputThreadsToModel proves the authoritative needs-input
// set the App pushes each tick reaches BuildModel via doRefresh: a worker in the
// set carries its own flag and the rollup reaches its coordinator; clearing the
// set clears the flags on the next refresh (BUG-018).
func TestHeraPage_SetNeedsInputThreadsToModel(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	p := NewHeraPage(d)
	p.SetNeedsInput([]string{"t-wkr"})
	p.Refresh()

	m := p.Rail().Model()
	o := m.OrchByID(orch)
	testutil.Equal(t, o.CoordRole().SubtreeNeedsInput, true)
	testutil.Equal(t, roleByName(t, &m, orch, "wkr").NeedsInput, true)

	// Clearing the set clears the rollup on the next refresh (Refresh always
	// rebuilds: Schedule fires when due, else the Flush forces the pending build).
	p.SetNeedsInput(nil)
	p.Refresh()
	m2 := p.Rail().Model()
	testutil.Equal(t, m2.OrchByID(orch).CoordRole().SubtreeNeedsInput, false)
}

func TestHeraPage_RemoteModeIsBannerOnly(t *testing.T) {
	p := NewHeraPage(nil) // remote: no hera reader
	testutil.Equal(t, p.IsRemote(), true)
	p.Refresh() // safe no-op
	testutil.Equal(t, p.Rail().Model().IsEmpty(), true)
}

func TestHeraPage_DrawLocalAndRemote(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(100, 30)

	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")

	local := NewHeraPage(d)
	local.Refresh()
	local.SetRect(0, 0, 100, 30)
	local.Draw(sim) // three-region layout, must not panic

	remote := NewHeraPage(nil)
	remote.SetRect(0, 0, 100, 30)
	remote.Draw(sim) // banner path, must not panic

	// Narrow terminal: rail-only, no right area, no panic.
	local.SetRect(0, 0, 20, 30)
	local.Draw(sim)
}

func TestHeraPage_ScheduleRefreshCoalesces(t *testing.T) {
	d := memDB(t)
	seedOrch(t, d, "orch")
	p := NewHeraPage(d)
	// First schedule fires; the model is populated.
	p.ScheduleRefresh()
	testutil.Equal(t, len(p.Rail().Model().Active), 1)
}

func TestHeraPage_RefreshSurvivesReaderError(t *testing.T) {
	p := NewHeraPage(errReader{})
	p.Refresh() // BuildModel errors → logged, rail left empty, no panic
	testutil.Equal(t, p.Rail().Model().IsEmpty(), true)
}

func TestHeraPage_FocusBorderReflectsState(t *testing.T) {
	p := NewHeraPage(nil)
	testutil.Equal(t, p.Machine().State(), FocusRail)
	p.Machine().Advance()
	testutil.Equal(t, p.Machine().State(), FocusCoord)
}

func TestHeraPage_CtrlZTogglesFullscreen(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "t-w")
	p := NewHeraPage(d)
	p.Refresh()
	h := p.InputHandler()

	// On the rail, Ctrl+Z is a consumed no-op — fullscreen stays off.
	h(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().Fullscreen(), false)

	// Move focus into the coordinator pane, then Ctrl+Z fullscreens it.
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	h(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().Fullscreen(), true)
	// And off again.
	h(tcell.NewEventKey(tcell.KeyCtrlZ, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().Fullscreen(), false)
}

// TestHeraPage_CmdArrowMovesRailSelectionWithoutChangingFocus verifies that
// Cmd+Down / Cmd+Up (tcell: KeyDown/KeyUp + ModCtrl|ModAlt) move the rail
// cursor regardless of which pane is focused, and do NOT change focus.
// This is BUG-002: the keys must be intercepted BEFORE forwardKey sends the
// mod-7 escape sequence to the pane PTY.
func TestHeraPage_CmdArrowMovesRailSelectionWithoutChangingFocus(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "t-w")
	p := NewHeraPage(d)
	p.Refresh()

	// Cursor starts on the orchestrator header (row 0 = the coordinator).
	testutil.Equal(t, p.Rail().CursorIndex(), 0)

	// Move focus into the coordinator pane — simulating the user typing while
	// watching the coordinator's output.
	p.Machine().Advance()
	testutil.Equal(t, p.Machine().State(), FocusCoord)

	h := p.InputHandler()

	// Cmd+Down must move the rail cursor to the worker row without changing focus.
	cmdDown := tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModCtrl|tcell.ModAlt)
	h(cmdDown, noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 1)
	testutil.Equal(t, p.Machine().State(), FocusCoord) // focus unchanged

	// Cmd+Up must move the rail cursor back to the header.
	cmdUp := tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModCtrl|tcell.ModAlt)
	h(cmdUp, noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 0)
	testutil.Equal(t, p.Machine().State(), FocusCoord) // focus still unchanged

	// Same behaviour when focused on the agent pane.
	p.Machine().Advance() // coord → agent
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	h(cmdDown, noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 1)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}

func TestHeraPage_FullscreenDrawRendersSinglePane(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(100, 30)

	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "t-w")
	p := NewHeraPage(d)
	p.Refresh()
	p.SetRect(0, 0, 100, 30)

	// Focus the coordinator pane and fullscreen it.
	p.Machine().Advance() // → coord
	p.Machine().ToggleFullscreen()
	p.Draw(sim) // fullscreen path, must not panic

	// The coordinator pane fills the area right of the rail; the agent pane's
	// hit-test rect collapsed to zero width.
	testutil.Equal(t, p.agentW, 0)
	if p.coordW <= 0 {
		t.Fatalf("expected fullscreen coord pane to have positive width, got %d", p.coordW)
	}
}

// TestHeraPage_OnFocusChange_FiresOnTabAdvance asserts that pressing Tab (which
// calls focus.Advance) triggers OnFocusChange with the new focus state. This is
// the wiring that lets the status bar update its hint set on every focus change.
func TestHeraPage_OnFocusChange_FiresOnTabAdvance(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "t-w")
	p := NewHeraPage(d)
	p.Refresh()

	var got []Focus
	p.OnFocusChange = func(f Focus) { got = append(got, f) }

	h := p.InputHandler()

	// Tab from rail → coord pane.
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	if len(got) == 0 {
		t.Fatal("OnFocusChange not called after Tab")
	}
	testutil.Equal(t, got[len(got)-1], FocusCoord)

	// Ctrl+Q → back to rail.
	h(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusRail)
	testutil.Equal(t, got[len(got)-1], FocusRail)
}

// TestHeraPage_LeftArrowMovesSelectionToParentOnRailFocus verifies BUG-016:
// when the rail is focused, Left arrow moves the cursor to the parent coordinator
// row (no-op at the root). Crucially it does NOT change the focused region.
func TestHeraPage_LeftArrowMovesSelectionToParentOnRailFocus(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.Refresh()

	h := p.InputHandler()
	// Start: cursor row 0 = orch header; row 1 = worker.
	testutil.Equal(t, p.Rail().CursorIndex(), 0)
	testutil.Equal(t, p.Machine().State(), FocusRail)

	// Move to the worker row.
	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 1)

	// Left on the worker → should jump to orch header (row 0).
	h(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 0)
	testutil.Equal(t, p.Machine().State(), FocusRail) // focus unchanged

	// Left again on the orch header (depth 0) → no-op.
	h(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 0)
}

// TestHeraPage_LeftArrowFromPaneDoesNotMoveRail verifies that Left from a
// focused pane is NOT intercepted by the rail's parent-nav logic — it passes
// through to the PTY unchanged, keeping the rail cursor where it was.
func TestHeraPage_LeftArrowFromPaneDoesNotMoveRail(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.Refresh()

	h := p.InputHandler()
	// Move rail cursor to the worker.
	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 1)

	// Move focus to the coordinator pane.
	p.Machine().Advance()
	testutil.Equal(t, p.Machine().State(), FocusCoord)

	// Left while pane is focused — must NOT move rail cursor.
	h(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 1)       // cursor unchanged
	testutil.Equal(t, p.Machine().State(), FocusCoord) // focus unchanged

	// Same for FocusAgent.
	p.Machine().Advance()
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	h(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 1) // still unchanged
}

// TestHeraPage_OnFocusChange_FiresOnEveryKey asserts that OnFocusChange fires
// on every key (including non-focus-changing keys) so the hint set stays current
// without a separate polling mechanism.
func TestHeraPage_OnFocusChange_FiresOnEveryKey(t *testing.T) {
	p := NewHeraPage(nil) // remote — OnFocusChange is still called (callback is nil-safe)

	called := 0
	p.OnFocusChange = func(f Focus) { called++ }

	h := p.InputHandler()
	// Key event in remote mode returns immediately; defer still fires.
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, called, 1)
}
