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
