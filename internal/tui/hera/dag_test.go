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

// planPage seeds an orch (coord t-coord + planned 1a/2a roles, 2a←1a), wires a
// live resolver, draws once so the focus machine learns both right regions are
// present, then returns the page. The embedded plan graph projects from the rail
// model via heraPlanNodesWithBridge (no provider seam).
func planPage(t *testing.T) *HeraPage {
	t.Helper()
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	a := seedPlannedRole(t, d, orch, "1a-research")
	b := seedPlannedRole(t, d, orch, "2a-write")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // 2a←1a
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-coord": {id: "t-coord", alive: true},
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
// Tab enters a pane from the rail; once in the coord pane (a terminal) Tab now
// passes through to the PTY (BUG-019), so the pane→pane hop uses the
// Ctrl+Alt+→ ladder instead.
func toAgentFocus(p *HeraPage) {
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt), noFocus)
}

// TestDetailsPlan_ProjectedOnCoordSelect: selecting a coordinator immediately
// projects the orchestrator's plan DAG into the embedded widget — the two
// planned worker roles + their blocking edge produce a 2-stage layout.
func TestDetailsPlan_ProjectedOnCoordSelect(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)
	testutil.Equal(t, p.Plan().Stages(), 2) // 1a → 2a is two longest-path layers
	testutil.Equal(t, p.Plan().NoPlan(), false)
}

// TestDetailsPlan_EdgeDrivesStages: the projected plan carries the blocking edge,
// so 2a sits one stage below 1a (computed longest-path, not the short-id number).
func TestDetailsPlan_EdgeDrivesStages(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	nodes, edges := heraPlanNodesWithBridge(p.SelectionContext().Orch, p.Rail().Model().bridgeIndex())
	testutil.Equal(t, len(nodes), 2)
	testutil.Equal(t, len(edges), 1)
	// The plan widget computed distinct stages for the two planned roles.
	s1, _ := p.Plan().StageOf(edges[0].From)
	s2, _ := p.Plan().StageOf(edges[0].To)
	testutil.Equal(t, s1, 0)
	testutil.Equal(t, s2, 1)
}

// TestDetailsPlan_RebuildOnCoordChange: selecting a different coordinator
// reprojects the plan for the new orchestrator (an empty plan → degenerate
// single flat stage).
func TestDetailsPlan_RebuildOnCoordChange(t *testing.T) {
	d := memDB(t)
	a := seedOrch(t, d, "orch-a")
	b := seedOrch(t, d, "orch-b")
	seedBoundRole(t, d, a, "coord", db.HeraKindCoordinator, "t-a")
	pa := seedPlannedRole(t, d, a, "1a")
	pb := seedPlannedRole(t, d, a, "2a")
	testutil.NoError(t, d.AddHeraBlock(pb.ID, pa.ID))
	seedBoundRole(t, d, b, "coord", db.HeraKindCoordinator, "t-b")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-a": {id: "t-a", alive: true}, "t-b": {id: "t-b", alive: true}}))
	p.Refresh()

	// Land on orch-a's header; the plan has the 1a→2a chain (2 stages).
	testutil.Equal(t, selectOrchByName(p, "orch-a"), true)
	testutil.Equal(t, p.Plan().Stages(), 2)

	// Navigate to orch-b's header; applySelection reprojects an empty (no-plan)
	// orchestrator → a single degenerate stage.
	testutil.Equal(t, selectOrchByName(p, "orch-b"), true)
	testutil.Equal(t, p.SelectionContext().Orch.ID, b)
	testutil.Equal(t, p.Plan().NoPlan(), true)
}

// TestDetailsPlan_DrawStacksBothPanels: with a coordinator selected the right
// region stacks the " Details " roster over the " Plan " graph — both titles
// render at once; the legacy " Orchestration Tree "/" DAG " titles never appear.
func TestDetailsPlan_DrawStacksBothPanels(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)

	text := drawnPageText(t, p, 120, 30)
	di := strings.Index(text, "Details")
	pi := strings.Index(text, " Plan ")
	testutil.Equal(t, di >= 0, true)
	testutil.Equal(t, pi >= 0, true)
	testutil.Equal(t, di < pi, true) // roster stacked ABOVE the plan, not below
	testutil.Equal(t, strings.Contains(text, " DAG "), false)
	testutil.Equal(t, strings.Contains(text, "Orchestration Tree"), false)
}

// TestDetailsPlan_TinyPaneRosterOnly: on a pane too short to fit both panels the
// roster still renders, the plan graph is skipped, and its rect is zeroed so a
// stale rect from a prior taller frame can't catch a mouse event over the roster.
func TestDetailsPlan_TinyPaneRosterOnly(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)

	// Draw tall first so the plan gets a real rect, then redraw very short.
	drawnPageText(t, p, 120, 30)
	_, _, dw, dh := p.Plan().GetRect()
	testutil.Equal(t, dw > 0 && dh > 0, true) // armed by the tall frame

	// h=4 → rosterH clamps to 3, planH=1 (<2) → plan skipped, rect zeroed.
	text := drawnPageText(t, p, 120, 4)
	testutil.Equal(t, strings.Contains(text, "Details"), true)
	testutil.Equal(t, strings.Contains(text, " Plan "), false)
	_, _, zw, zh := p.Plan().GetRect()
	testutil.Equal(t, zw == 0 && zh == 0, true) // stale rect cleared

	// A click over the (now plan-less) agent region must not be consumed by the
	// zeroed plan rect.
	mh := p.MouseHandler()
	ev := tcell.NewEventMouse(110, 1, tcell.Button1, tcell.ModNone)
	consumed, _ := mh(tview.MouseLeftClick, ev, noFocus)
	testutil.Equal(t, consumed, false)

	// Heights below the roster's min-floor (h<3) exercise the clamp interaction
	// (max(_,3)→min(_,h)) with planH==0 — must not panic.
	for h := 1; h <= 3; h++ {
		drawnPageText(t, p, 120, h)
	}
}

// TestDetailsPlan_WorkerSelectionUnaffected: a worker selection shows its AGENT
// terminal (detailsMode false → no stacked Details/plan region).
func TestDetailsPlan_WorkerSelectionUnaffected(t *testing.T) {
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
	p.Draw(sim)

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)

	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.detailsMode, false)
	// Worker terminal is bound; the plan panel is not drawn.
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")
	testutil.Equal(t, strings.Contains(drawnPageText(t, p, 120, 30), " Plan "), false)
}

// TestDetailsPlan_KeyForwardsToWidget: with a coordinator selected, j/k forward
// to the embedded plan widget (the interactive surface of the stacked region)
// and move its stage cursor.
func TestDetailsPlan_KeyForwardsToWidget(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	toAgentFocus(p)
	testutil.Equal(t, p.Plan().CursorPos().Stage, 0)

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Plan().CursorPos().Stage, 1) // moved down a stage
	h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Plan().CursorPos().Stage, 0)
}

// TestDetailsPlan_EscAtRootEscapesPane: with a coordinator selected and the plan
// region focused at drill-depth 0, Esc escapes the pane back to the rail (the
// widget no-ops Esc at root, so the page must handle it — no operator trap).
func TestDetailsPlan_EscAtRootEscapesPane(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	toAgentFocus(p)
	testutil.Equal(t, p.Machine().State(), FocusAgent)

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusRail)
}

// TestDetailsPlan_NoSyncOnDraw pins the UX-rendering rule for the stacked region.
func TestDetailsPlan_NoSyncOnDraw(t *testing.T) {
	p := planPage(t)
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

// TestDetailsPlan_MouseRoutesToWidget: a click in the agent region (within the
// plan sub-rect) is handled by the embedded widget (no panic; consumed path).
func TestDetailsPlan_MouseRoutesToWidget(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	// Click low in the right region — inside the plan graph (bottom of the stack).
	mh := p.MouseHandler()
	ev := tcell.NewEventMouse(110, 20, tcell.Button1, tcell.ModNone)
	mh(tview.MouseLeftClick, ev, noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}
