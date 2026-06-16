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
