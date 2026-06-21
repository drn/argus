package hera

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// dumpRows returns the rendered text of rows [0,h) over width w.
func dumpRows(s tcell.Screen, w, h int) string {
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			cell, _, _ := s.Get(x, y)
			if cell == "" {
				cell = " "
			}
			b.WriteString(cell)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// TestHeraPage_AttentionSummary_ShowsForUnmanagedAndShrinksRail proves that an
// unmanaged needs-input task draws the summary box at the top of the rail column
// and pushes the rail down by the box height.
func TestHeraPage_AttentionSummary_ShowsForUnmanagedAndShrinksRail(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(100, 30)

	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")

	p := NewHeraPage(d)
	p.Refresh()
	p.SetNeedsInput([]string{"t-unmanaged"}) // not bound to any role
	p.SetRect(0, 0, 100, 30)
	p.Draw(sim)

	testutil.Equal(t, p.summary.Count(), 1)
	// Rail is pushed below the box.
	_, ry, _, rh := p.Rail().GetRect()
	testutil.Equal(t, ry, p.summary.DesiredHeight())
	testutil.Equal(t, rh, 30-p.summary.DesiredHeight())
	// The box text is rendered in the top rows.
	top := dumpRows(sim, 30, p.summary.DesiredHeight())
	if !strings.Contains(top, "1 task needs input") {
		t.Errorf("expected summary text in top rows, got:\n%s", top)
	}
}

// TestHeraPage_AttentionSummary_ShowThenHideTransition draws the box visible then
// drives it hidden, exercising both edges of the show/hide transition logging.
func TestHeraPage_AttentionSummary_ShowThenHideTransition(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(100, 30)

	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")

	p := NewHeraPage(d)
	p.Refresh()
	p.SetRect(0, 0, 100, 30)

	p.SetNeedsInput([]string{"t-unmanaged"})
	p.Draw(sim)
	testutil.Equal(t, p.summaryShown, true)

	p.SetNeedsInput(nil)
	p.Draw(sim)
	testutil.Equal(t, p.summaryShown, false)
	_, ry, _, rh := p.Rail().GetRect()
	testutil.Equal(t, ry, 0)
	testutil.Equal(t, rh, 30)
}

// TestHeraPage_AttentionSummary_HiddenWhenAllManaged proves a managed task in the
// needs-input set produces no box and the rail keeps the full column.
func TestHeraPage_AttentionSummary_HiddenWhenAllManaged(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(100, 30)

	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")

	p := NewHeraPage(d)
	p.Refresh()
	p.SetNeedsInput([]string{"t-coord"}) // managed → excluded
	p.SetRect(0, 0, 100, 30)
	p.Draw(sim)

	testutil.Equal(t, p.summary.Count(), 0)
	testutil.Equal(t, p.summary.DesiredHeight(), 0)
	_, ry, _, rh := p.Rail().GetRect()
	testutil.Equal(t, ry, 0)
	testutil.Equal(t, rh, 30)
}

// TestHeraPage_AttentionSummary_YieldsOnShortTerminal proves the box does not
// draw when the terminal is too short to keep the rail usable.
func TestHeraPage_AttentionSummary_YieldsOnShortTerminal(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(100, 5)

	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")

	p := NewHeraPage(d)
	p.Refresh()
	p.SetNeedsInput([]string{"t-unmanaged"})
	p.SetRect(0, 0, 100, 5)
	p.Draw(sim)

	// Rail keeps the full short column; box yielded.
	_, ry, _, rh := p.Rail().GetRect()
	testutil.Equal(t, ry, 0)
	testutil.Equal(t, rh, 5)
}

// TestHeraPage_AttentionSummary_NeverDrawnRemote proves the box is inert in
// remote mode (the page short-circuits to its banner).
func TestHeraPage_AttentionSummary_NeverDrawnRemote(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(100, 30)

	p := NewHeraPage(nil) // remote
	p.SetNeedsInput([]string{"t-unmanaged"})
	p.SetRect(0, 0, 100, 30)
	p.Draw(sim)

	testutil.Equal(t, p.summary.Count(), 0)
	full := dumpRows(sim, 100, 30)
	if strings.Contains(full, "task needs input") || strings.Contains(full, "tasks need input") {
		t.Errorf("summary must not render in remote mode, got:\n%s", full)
	}
}
