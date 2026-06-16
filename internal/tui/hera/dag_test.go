package hera

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

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

// dagPage seeds an orch (coord t-coord + worker t-wkr), wires a live resolver,
// draws once so the focus machine learns both right regions are present, then
// returns the page. The embedded tree projects from the rail model via
// heraTreeNodes (no provider seam).
func dagPage(t *testing.T) *HeraPage {
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
// projects the orchestration tree into the embedded DAG — the coordinator's
// task is the root node, so the cursor lands on it (layer 0).
func TestDetailsDAG_ProjectedOnCoordSelect(t *testing.T) {
	p := dagPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)
	testutil.Equal(t, p.DAG().CurrentTask(), "t-coord") // coordinator is the tree root
}

// TestDetailsDAG_WorkerHangsOffCoordinator: the worker node carries a synthetic
// edge to the coordinator, so the graph has both nodes and the worker depends on
// the coord.
func TestDetailsDAG_WorkerHangsOffCoordinator(t *testing.T) {
	p := dagPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	nodes := heraTreeNodes(p.Rail().Model(), p.SelectionContext().Orch)
	testutil.Equal(t, len(nodes), 2)
	for _, n := range nodes {
		if n.ID == "t-wkr" {
			testutil.DeepEqual(t, n.DependsOn, []string{"t-coord"})
		}
		if n.ID == "t-coord" {
			testutil.Equal(t, len(n.DependsOn), 0) // root has no parent
		}
	}
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
	p.Refresh()

	// Land on orch-a's header (its coordinator is folded in); DAG projects orch-a.
	testutil.Equal(t, selectOrchByName(p, "orch-a"), true)
	testutil.Equal(t, p.DAG().CurrentTask(), "t-a")

	// Navigate to orch-b's header; applySelection reprojects.
	testutil.Equal(t, selectOrchByName(p, "orch-b"), true)
	testutil.Equal(t, p.SelectionContext().Orch.ID, b)
	testutil.Equal(t, p.DAG().CurrentTask(), "t-b")
}

// TestDetailsDAG_DrawStacksBothPanels: with a coordinator selected the right
// region stacks the " Details " roster over the " Orchestration Tree " graph —
// both titles render at once; the legacy " DAG " title never appears.
func TestDetailsDAG_DrawStacksBothPanels(t *testing.T) {
	p := dagPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)

	text := drawnPageText(t, p, 120, 30)
	di := strings.Index(text, "Details")
	pi := strings.Index(text, "Orchestration Tree")
	testutil.Equal(t, di >= 0, true)
	testutil.Equal(t, pi >= 0, true)
	testutil.Equal(t, di < pi, true) // roster stacked ABOVE the tree, not below
	testutil.Equal(t, strings.Contains(text, " DAG "), false)
}

// TestDetailsDAG_TinyPaneRosterOnly: on a pane too short to fit both panels the
// roster still renders, the tree is skipped, and its rect is zeroed so a stale
// rect from a prior taller frame can't catch a mouse event over the roster.
func TestDetailsDAG_TinyPaneRosterOnly(t *testing.T) {
	p := dagPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)

	// Draw tall first so the tree gets a real rect, then redraw very short.
	drawnPageText(t, p, 120, 30)
	_, _, dw, dh := p.DAG().GetRect()
	testutil.Equal(t, dw > 0 && dh > 0, true) // armed by the tall frame

	// h=4 → rosterH clamps to 3, dagH=1 (<2) → tree skipped, rect zeroed.
	text := drawnPageText(t, p, 120, 4)
	testutil.Equal(t, strings.Contains(text, "Details"), true)
	testutil.Equal(t, strings.Contains(text, "Orchestration Tree"), false)
	_, _, zw, zh := p.DAG().GetRect()
	testutil.Equal(t, zw == 0 && zh == 0, true) // stale rect cleared

	// A click over the (now tree-less) agent region must not be consumed by the
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
// terminal (detailsMode false → no stacked Details/tree region).
func TestDetailsDAG_WorkerSelectionUnaffected(t *testing.T) {
	p := dagPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)

	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.detailsMode, false)
	// Worker terminal is bound; the tree panel is not drawn.
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")
	testutil.Equal(t, strings.Contains(drawnPageText(t, p, 120, 30), "Orchestration Tree"), false)
}

// TestDetailsDAG_KeyForwardsToWidget: with a coordinator selected, j/k forward
// to the embedded tree widget (the interactive surface of the stacked region)
// and move its cursor between nodes.
func TestDetailsDAG_KeyForwardsToWidget(t *testing.T) {
	p := dagPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	toAgentFocus(p)
	testutil.Equal(t, p.DAG().CurrentTask(), "t-coord")

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, p.DAG().CurrentTask(), "t-wkr") // moved down to the worker
	h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
	testutil.Equal(t, p.DAG().CurrentTask(), "t-coord")
}

// TestDetailsDAG_NoSyncOnDraw pins the UX-rendering rule for the stacked region.
func TestDetailsDAG_NoSyncOnDraw(t *testing.T) {
	p := dagPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)

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
// tree sub-rect) is handled by the embedded widget (no panic; consumed path).
func TestDetailsDAG_MouseRoutesToWidget(t *testing.T) {
	p := dagPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	// Click low in the right region — inside the tree (bottom of the stack).
	mh := p.MouseHandler()
	ev := tcell.NewEventMouse(110, 20, tcell.Button1, tcell.ModNone)
	mh(tview.MouseLeftClick, ev, noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}
