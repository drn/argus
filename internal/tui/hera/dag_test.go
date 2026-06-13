package hera

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/dagview"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// dagProviderByOrchName is a test DAGNodeProvider returning a single node whose
// ID is the orchestrator's name, so a test can assert which orchestrator the
// embedded DAG was (re)projected for via dag.CurrentTask().
func dagProviderByOrchName(o *OrchView) []dagview.Node {
	if o == nil {
		return nil
	}
	return []dagview.Node{{ID: o.Name, Name: o.Name, Status: "in_progress"}}
}

// drawnPage renders the whole page to a fresh sim screen and returns the full
// text of row y (so a test can find a centered border title anywhere on it).
func drawnPageRow(t *testing.T, p *HeraPage, w, h, y int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(w, h)
	p.SetRect(0, 0, w, h)
	p.Draw(sim)
	sim.Show()
	cells, _, _ := sim.GetContents()
	runes := make([]rune, 0, w)
	for i := 0; i < w; i++ {
		c := cells[(y*w)+i]
		if len(c.Runes) > 0 {
			runes = append(runes, c.Runes[0])
		}
	}
	return string(runes)
}

// dagPageWithProvider seeds an orch (coord+worker), wires a live resolver + the
// node provider, draws once so the focus machine learns both right regions are
// present, then returns the page.
func dagPageWithProvider(t *testing.T, prov DAGNodeProvider) *HeraPage {
	t.Helper()
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-coord": {id: "t-coord", alive: true},
		"t-wkr":   {id: "t-wkr", alive: true},
	}))
	if prov != nil {
		p.SetDAGNodeProvider(prov)
	}
	p.Refresh()

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)
	p.Draw(sim) // teach the focus machine the regions are present
	return p
}

// toAgentFocus walks the focus ladder rail→coord→agent via the page handler.
func toAgentFocus(p *HeraPage) {
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
}

// TestDetailsDAG_ToggleViaKey: with a coordinator selected, `g` (in the Details
// region) toggles roster ↔ DAG and projects the provider's nodes.
func TestDetailsDAG_ToggleViaKey(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	testutil.Equal(t, p.detailsMode, true)
	testutil.Equal(t, p.DetailsSubMode(), subModeRoster)

	toAgentFocus(p)
	testutil.Equal(t, p.Machine().State(), FocusAgent)

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone), noFocus)
	testutil.Equal(t, p.DetailsSubMode(), subModeDAG)
	testutil.Equal(t, p.DAG().CurrentTask(), "orch") // provider keyed nodes by orch name

	h(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone), noFocus)
	testutil.Equal(t, p.DetailsSubMode(), subModeRoster)
}

// TestDetailsDAG_RebuildOnCoordChange: in DAG sub-mode, selecting a different
// coordinator reprojects the graph for the new orchestrator (sub-mode sticky).
func TestDetailsDAG_RebuildOnCoordChange(t *testing.T) {
	d := memDB(t)
	a := seedOrch(t, d, "orch-a")
	b := seedOrch(t, d, "orch-b")
	seedBoundRole(t, d, a, "coord", db.HeraKindCoordinator, "t-a")
	seedBoundRole(t, d, b, "coord", db.HeraKindCoordinator, "t-b")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-a": {id: "t-a", alive: true}, "t-b": {id: "t-b", alive: true}}))
	p.SetDAGNodeProvider(dagProviderByOrchName)
	p.Refresh()

	// Land on orch-a's coordinator and switch to DAG.
	testutil.Equal(t, selectRoleByName(p, "coord"), true) // first "coord" is orch-a's
	p.toggleDetailsSubMode()
	testutil.Equal(t, p.DAG().CurrentTask(), "orch-a")

	// Navigate to orch-b's coordinator; applySelection reprojects.
	r := p.Rail()
	for i := 0; i < r.Rows(); i++ {
		r.CursorDown()
		if sel := r.Selected(); sel != nil && sel.OrchID == b {
			break
		}
	}
	testutil.Equal(t, p.SelectionContext().Orch.ID, b)
	testutil.Equal(t, p.DAG().CurrentTask(), "orch-b")
}

// TestDetailsDAG_NilProviderEmpty: with no provider (remote-style), toggling to
// DAG yields an empty graph (no panic, no cursor).
func TestDetailsDAG_NilProviderEmpty(t *testing.T) {
	p := dagPageWithProvider(t, nil) // provider intentionally unset
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	p.toggleDetailsSubMode()
	testutil.Equal(t, p.DetailsSubMode(), subModeDAG)
	testutil.Equal(t, p.DAG().CurrentTask(), "")
}

// TestDetailsDAG_DrawShowsDependenciesTitle: in DAG sub-mode the right region
// draws the retitled " Dependencies " panel, NOT the " Details " roster; the
// legacy " DAG " title never appears (embedding caveat handled).
func TestDetailsDAG_DrawShowsDependenciesTitle(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)

	// Roster mode first: the right region is " Details ".
	testutil.Equal(t, strings.Contains(drawnPageRow(t, p, 120, 30, 0), "Details"), true)

	p.toggleDetailsSubMode()
	row := drawnPageRow(t, p, 120, 30, 0)
	testutil.Equal(t, strings.Contains(row, "Dependencies"), true)
	testutil.Equal(t, strings.Contains(row, "Details"), false)
	testutil.Equal(t, strings.Contains(row, " DAG "), false)
}

// TestDetailsDAG_WorkerSelectionUnaffected: even with DAG sub-mode active, a
// worker selection shows its AGENT terminal (detailsMode false → no DAG).
func TestDetailsDAG_WorkerSelectionUnaffected(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	p.toggleDetailsSubMode() // sub-mode now DAG, but sticky in the background
	testutil.Equal(t, p.DetailsSubMode(), subModeDAG)

	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.detailsMode, false)
	// Worker terminal is bound; the Dependencies panel is not drawn.
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")
	testutil.Equal(t, strings.Contains(drawnPageRow(t, p, 120, 30, 0), "Dependencies"), false)
}

// TestDetailsDAG_KeyForwardsToWidget: in DAG sub-mode, l/L/h forward to the
// embedded widget and fire its callbacks with the cursor task.
func TestDetailsDAG_KeyForwardsToWidget(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	var linked, unlinked, halted string
	p.DAG().OnLink = func(c string) { linked = c }
	p.DAG().OnUnlink = func(c string) { unlinked = c }
	p.DAG().OnHalt = func(c string) { halted = c }

	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	toAgentFocus(p)
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'g', tcell.ModNone), noFocus) // → DAG
	testutil.Equal(t, p.DAG().CurrentTask(), "orch")

	h(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyRune, 'L', tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone), noFocus)
	testutil.Equal(t, linked, "orch")
	testutil.Equal(t, unlinked, "orch")
	testutil.Equal(t, halted, "orch")
}

// TestDetailsDAG_NoSyncOnDraw pins the UX-rendering rule for the DAG sub-mode.
func TestDetailsDAG_NoSyncOnDraw(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	p.toggleDetailsSubMode()

	base := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, base.Init())
	defer base.Fini()
	base.SetSize(120, 30)
	sc := &syncCountingScreen{SimulationScreen: base}
	p.SetRect(0, 0, 120, 30)
	p.Draw(sc)
	testutil.Equal(t, sc.syncCount, 0)
}

// TestDetailsDAG_MouseRoutesToWidget: a click in the agent region in DAG mode is
// handled by the embedded widget (no panic; consumed path).
func TestDetailsDAG_MouseRoutesToWidget(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	p.toggleDetailsSubMode()
	// Click near the right region; regionAt maps it to FocusAgent.
	mh := p.MouseHandler()
	ev := tcell.NewEventMouse(110, 5, tcell.Button1, tcell.ModNone)
	mh(tview.MouseLeftClick, ev, noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}
