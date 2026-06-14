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

// drawnPageText renders the whole page to a fresh sim screen and returns its
// full text (all rows joined by newlines), so a test can find a centered border
// title on any row.
func drawnPageText(t *testing.T, p *HeraPage, w, h int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(w, h)
	p.SetRect(0, 0, w, h)
	p.Draw(sim)
	sim.Show()
	cells, _, _ := sim.GetContents()
	var b strings.Builder
	for y := range h {
		for i := range w {
			c := cells[(y*w)+i]
			if len(c.Runes) > 0 {
				b.WriteRune(c.Runes[0])
			}
		}
		b.WriteByte('\n')
	}
	return b.String()
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

// TestDetailsDAG_ProjectedOnCoordSelect: selecting a coordinator immediately
// projects the provider's nodes into the embedded DAG — no toggle needed.
func TestDetailsDAG_ProjectedOnCoordSelect(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	testutil.Equal(t, p.detailsMode, true)
	testutil.Equal(t, p.DAG().CurrentTask(), "orch") // provider keyed nodes by orch name
}

// TestDetailsDAG_RebuildOnCoordChange: selecting a different coordinator
// reprojects the graph for the new orchestrator.
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

	// Land on orch-a's coordinator; the DAG projects for orch-a.
	testutil.Equal(t, selectRoleByName(p, "coord"), true) // first "coord" is orch-a's
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

// TestDetailsDAG_NilProviderEmpty: with no provider (remote-style), a coordinator
// selection yields an empty graph (no panic, no cursor).
func TestDetailsDAG_NilProviderEmpty(t *testing.T) {
	p := dagPageWithProvider(t, nil) // provider intentionally unset
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	testutil.Equal(t, p.DAG().CurrentTask(), "")
}

// TestDetailsDAG_DrawStacksBothPanels: with a coordinator selected the right
// region stacks the " Details " roster over the " Dependencies " DAG — both
// titles render at once; the legacy " DAG " title never appears.
func TestDetailsDAG_DrawStacksBothPanels(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)

	text := drawnPageText(t, p, 120, 30)
	di := strings.Index(text, "Details")
	pi := strings.Index(text, "Dependencies")
	testutil.Equal(t, di >= 0, true)
	testutil.Equal(t, pi >= 0, true)
	testutil.Equal(t, di < pi, true) // roster stacked ABOVE the DAG, not below
	testutil.Equal(t, strings.Contains(text, " DAG "), false)
}

// TestDetailsDAG_TinyPaneRosterOnly: on a pane too short to fit both panels the
// roster still renders, the DAG is skipped, and its rect is zeroed so a stale
// rect from a prior taller frame can't catch a mouse event over the roster.
func TestDetailsDAG_TinyPaneRosterOnly(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)

	// Draw tall first so the DAG gets a real rect, then redraw very short.
	drawnPageText(t, p, 120, 30)
	_, _, dw, dh := p.DAG().GetRect()
	testutil.Equal(t, dw > 0 && dh > 0, true) // armed by the tall frame

	// h=4 → rosterH clamps to 3, dagH=1 (<2) → DAG skipped, rect zeroed.
	text := drawnPageText(t, p, 120, 4)
	testutil.Equal(t, strings.Contains(text, "Details"), true)
	testutil.Equal(t, strings.Contains(text, "Dependencies"), false)
	_, _, zw, zh := p.DAG().GetRect()
	testutil.Equal(t, zw == 0 && zh == 0, true) // stale rect cleared

	// A click over the (now DAG-less) agent region must not be consumed by the
	// zeroed DAG rect.
	mh := p.MouseHandler()
	ev := tcell.NewEventMouse(110, 1, tcell.Button1, tcell.ModNone)
	consumed, _ := mh(tview.MouseLeftClick, ev, noFocus)
	testutil.Equal(t, consumed, false)

	// Heights below the roster's min-floor (h<3) exercise the clamp interaction
	// (max(_,3)→min(_,h)) with dagH==0 — must not panic.
	for h := 1; h <= 3; h++ {
		drawnPageText(t, p, 120, h)
	}
}

// TestDetailsDAG_WorkerSelectionUnaffected: a worker selection shows its AGENT
// terminal (detailsMode false → no stacked Details/DAG region).
func TestDetailsDAG_WorkerSelectionUnaffected(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	testutil.Equal(t, p.detailsMode, true)

	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.detailsMode, false)
	// Worker terminal is bound; the Dependencies panel is not drawn.
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")
	testutil.Equal(t, strings.Contains(drawnPageText(t, p, 120, 30), "Dependencies"), false)
}

// TestDetailsDAG_KeyForwardsToWidget: with a coordinator selected, l/L/h forward
// to the embedded DAG (the interactive surface of the stacked region) and fire
// its callbacks with the cursor task.
func TestDetailsDAG_KeyForwardsToWidget(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	var linked, unlinked, halted string
	p.DAG().OnLink = func(c string) { linked = c }
	p.DAG().OnUnlink = func(c string) { unlinked = c }
	p.DAG().OnHalt = func(c string) { halted = c }

	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	toAgentFocus(p)
	testutil.Equal(t, p.DAG().CurrentTask(), "orch")

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyRune, 'L', tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone), noFocus)
	testutil.Equal(t, linked, "orch")
	testutil.Equal(t, unlinked, "orch")
	testutil.Equal(t, halted, "orch")
}

// TestDetailsDAG_NoSyncOnDraw pins the UX-rendering rule for the stacked region.
func TestDetailsDAG_NoSyncOnDraw(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)

	base := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, base.Init())
	defer base.Fini()
	base.SetSize(120, 30)
	sc := &syncCountingScreen{SimulationScreen: base}
	p.SetRect(0, 0, 120, 30)
	p.Draw(sc)
	testutil.Equal(t, sc.syncCount, 0)
}

// TestDetailsDAG_MouseRoutesToWidget: a click in the agent region (within the
// DAG sub-rect) is handled by the embedded widget (no panic; consumed path).
func TestDetailsDAG_MouseRoutesToWidget(t *testing.T) {
	p := dagPageWithProvider(t, dagProviderByOrchName)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	// Click low in the right region — inside the DAG (bottom of the stack).
	mh := p.MouseHandler()
	ev := tcell.NewEventMouse(110, 20, tcell.Button1, tcell.ModNone)
	mh(tview.MouseLeftClick, ev, noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}
