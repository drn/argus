package hera

import (
	"fmt"
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/planview"
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
	nodes, edges := heraPlanNodesWithBridge(p.SelectionContext().Orch, p.Rail().Model().BridgeIndex())
	testutil.Equal(t, len(nodes), 2)
	testutil.Equal(t, len(edges), 1)
	// The plan widget computed distinct stages for the two planned roles.
	s1, _ := p.Plan().StageOf(edges[0].From)
	s2, _ := p.Plan().StageOf(edges[0].To)
	testutil.Equal(t, s1, 0)
	testutil.Equal(t, s2, 1)
}

// TestDetailsPlan_RebuildOnCoordChange: selecting a different coordinator
// reprojects the plan for the new orchestrator (an orchestrator with no authored
// plan → the empty-plan state, NoPlan() true).
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

	// Navigate to orch-b's header; applySelection reprojects an orchestrator with
	// no authored plan → the empty-plan state.
	testutil.Equal(t, selectOrchByName(p, "orch-b"), true)
	testutil.Equal(t, p.SelectionContext().Orch.ID, b)
	testutil.Equal(t, p.Plan().NoPlan(), true)
}

// TestDetailsPlan_LiveWorkersNoPlanRendersEmptyState (BUG-013): a coordinator
// with LIVE workers but no planned nodes and no blocking edges projects to the
// empty-plan state — NoPlan() true, zero stages, the "No plan authored."
// placeholder drawn, and the live worker role chips are NOT rendered as a
// pseudo-DAG stage (the live agents are the rail's job, not the plan graph's).
func TestDetailsPlan_LiveWorkersNoPlanRendersEmptyState(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "alpha-wkr", db.HeraKindWorker, "t-alpha")
	seedBoundRole(t, d, orch, "beta-wkr", db.HeraKindWorker, "t-beta")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-coord": {id: "t-coord", alive: true},
		"t-alpha": {id: "t-alpha", alive: true},
		"t-beta":  {id: "t-beta", alive: true},
	}))
	p.Refresh()

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)
	// NoPlan + zero stages prove the plan has no flat live-role stage; the
	// placeholder renders. (The live workers still appear in the Details ROSTER
	// panel above the plan — that is the rail/roster's job, not the plan graph's.)
	testutil.Equal(t, p.Plan().NoPlan(), true)
	testutil.Equal(t, p.Plan().Stages(), 0)

	text := drawnPageText(t, p, 120, 30)
	testutil.Equal(t, strings.Contains(text, "No plan authored"), true)
}

// TestDetailsPlan_AuthoredPlanRendersDAG (BUG-013): a coordinator WITH an
// authored plan (≥1 planned node / ≥1 edge) still projects the full DAG — NoPlan()
// false and the planned nodes lay out into stages.
func TestDetailsPlan_AuthoredPlanRendersDAG(t *testing.T) {
	p := planPage(t) // coord + planned 1a/2a with 2a←1a edge
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.Plan().NoPlan(), false)
	testutil.Equal(t, p.Plan().Stages(), 2)
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

// TestFocusRouting_ArrowKeysStayWithinFocusedRegion is the end-to-end
// regression guard for the arrow-key focus-routing fix, driven through the
// real SimulationScreen + full page InputHandler dispatch (planPage/
// toAgentFocus): while the RAIL is focused, arrow-driven cursor nav and other
// rail keys (s/S status-step, etc.) behave exactly as before and never touch
// the plan widget; once focus moves to the Details/plan region (a coordinator
// selection), arrow keys drive the plan widget's stage cursor exclusively and
// never move the rail's cursor or the roster's scroll offset — closing the
// reported "arrow keys meant for the DAG instead move the rail/agent list" gap
// in both directions.
func TestFocusRouting_ArrowKeysStayWithinFocusedRegion(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	h := p.InputHandler()

	// -- Rail focus: existing nav + mutation keys are unaffected by the fix. --
	testutil.Equal(t, p.Machine().State(), FocusRail)
	railCursorBefore := p.Rail().CursorIndex()
	advanced := false
	p.OnStatusAdvance = func(Selection) { advanced = true }
	h(tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone), noFocus)
	testutil.Equal(t, advanced, true)                // rail mutation key still fires
	testutil.Equal(t, p.Plan().CursorPos().Stage, 0) // plan untouched by a rail-focused key
	// Down still drives ordinary rail cursor nav (moves onto the orchestrator's
	// planned-role rows) — unaffected by the fix, which only touches the
	// Details/plan region's own key routing.
	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex() != railCursorBefore, true)
	testutil.Equal(t, p.Plan().CursorPos().Stage, 0) // still untouched
	// Return the cursor to the coordinator header so the Details/plan region
	// (below) is exercised against the coordinator selection.
	h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), railCursorBefore)
	testutil.Equal(t, p.detailsMode, true)

	// -- Move focus onto the Details/plan region (Tab, then Ctrl+Alt+Right). --
	toAgentFocus(p)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	testutil.Equal(t, p.detailsMode, true)

	// Arrow keys move the plan's stage cursor and leave the rail's cursor
	// (still parked on the coordinator header) and the roster's scroll offset
	// untouched.
	rosterBefore := p.details.rosterScroll
	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Plan().CursorPos().Stage, 1)
	testutil.Equal(t, p.Rail().CursorIndex(), railCursorBefore)
	testutil.Equal(t, p.details.rosterScroll, rosterBefore)

	h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Plan().CursorPos().Stage, 0)
	testutil.Equal(t, p.Rail().CursorIndex(), railCursorBefore)
	testutil.Equal(t, p.details.rosterScroll, rosterBefore)

	// Focus itself never moved off the Details/plan region during any of this.
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}

// TestHandleDetailsKey_ArrowsAlwaysReachPlanNeverRoster: a coordinator with far
// more agents than a short pane can show must NOT have its roster steal
// j/k/Up/Down from the embedded plan widget (BUG: reported "arrow keys used to
// navigate the DAG instead scroll the agent roster"). Those keys are the plan
// widget's own stage-nav keys, so they must reach it unconditionally and the
// roster's scroll offset must never move in response to them, regardless of
// how many agents overflow the roster panel.
func TestHandleDetailsKey_ArrowsAlwaysReachPlanNeverRoster(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "big-orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	for i := 1; i <= 20; i++ {
		seedBoundRole(t, d, orch, fmt.Sprintf("agent-%02d", i), db.HeraKindWorker, fmt.Sprintf("t-w%02d", i))
	}
	a := seedPlannedRole(t, d, orch, "1a-research")
	b := seedPlannedRole(t, d, orch, "2a-write")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // 2a←1a, so the plan has 2 stages to move between
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": {id: "t-coord", alive: true}}))
	p.Refresh()

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	// The roster panel is capped at half the details region (drawDetailsRegion),
	// and this fixture's natural ContentHeight (~36 rows for 20 agents) exceeds
	// that cap at this size — so the roster overflows without needing an
	// unrealistically short pane.
	sim.SetSize(80, 50)
	p.SetRect(0, 0, 80, 50)
	p.Draw(sim)

	testutil.Equal(t, selectOrchByName(p, "big-orch"), true)
	toAgentFocus(p)
	p.Draw(sim) // populate DetailsView.rosterVisibleRows for this selection/size

	h := p.InputHandler()
	planStage := func() int { return p.Plan().CursorPos().Stage }
	startStage := planStage()
	testutil.Equal(t, startStage, 0)
	for _, key := range []tcell.Key{tcell.KeyDown, tcell.KeyDown, tcell.KeyUp} {
		before := p.details.rosterScroll
		h(tcell.NewEventKey(key, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, p.details.rosterScroll, before) // roster never moves on an arrow key
		p.Draw(sim)
	}
	// j moved the plan cursor down a stage, k moved it back — the roster's
	// scroll offset stayed put throughout.
	rosterBefore := p.details.rosterScroll
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, planStage(), 1)
	testutil.Equal(t, p.details.rosterScroll, rosterBefore)
	h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
	testutil.Equal(t, planStage(), startStage)
	testutil.Equal(t, p.details.rosterScroll, rosterBefore)
}

// TestHandleDetailsKey_PgDnPgUpScrollRoster: PgDn/PgUp are the roster's
// dedicated scroll keys (moved off j/k/Up/Down, which now belong exclusively
// to the plan widget — see TestHandleDetailsKey_ArrowsAlwaysReachPlanNeverRoster).
// The plan widget's stage cursor must not move in response to them.
func TestHandleDetailsKey_PgDnPgUpScrollRoster(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "big-orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	for i := 1; i <= 20; i++ {
		seedBoundRole(t, d, orch, fmt.Sprintf("agent-%02d", i), db.HeraKindWorker, fmt.Sprintf("t-w%02d", i))
	}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": {id: "t-coord", alive: true}}))
	p.Refresh()

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(80, 50)
	p.SetRect(0, 0, 80, 50)
	p.Draw(sim)

	testutil.Equal(t, selectOrchByName(p, "big-orch"), true)
	toAgentFocus(p)
	p.Draw(sim)

	h := p.InputHandler()
	planStage := func() int { return p.Plan().CursorPos().Stage }
	startStage := planStage()
	scrolledRoster := false
	for i := 0; i < 30; i++ { // far more than needed; ScrollRoster clamps at the bound
		before := p.details.rosterScroll
		h(tcell.NewEventKey(tcell.KeyPgDn, 0, tcell.ModNone), noFocus)
		if p.details.rosterScroll != before {
			scrolledRoster = true
		}
		p.Draw(sim) // recompute the budget for the next keypress, as a live app would
	}
	testutil.Equal(t, scrolledRoster, true)
	testutil.Equal(t, p.details.rosterScroll, p.details.rosterMaxScroll(len(p.details.workers())))
	testutil.Equal(t, planStage(), startStage) // the plan cursor never moved

	for i := 0; i < 30; i++ {
		h(tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone), noFocus)
		p.Draw(sim)
	}
	testutil.Equal(t, p.details.rosterScroll, 0)
	testutil.Equal(t, planStage(), startStage)
}

// TestDetailsPlan_EscAtRootDoesNotJumpToRail: with a coordinator selected and the
// plan region focused at drill-depth 0 with nothing fanned, Esc is a CONSUMED
// no-op in the widget — it must NOT change focus / reach the rail. The Stage-7
// Esc→rail escape hatch was removed; the operator leaves the pane via ^Q / Tab.
func TestDetailsPlan_EscAtRootDoesNotJumpToRail(t *testing.T) {
	p := planPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	toAgentFocus(p)
	testutil.Equal(t, p.Machine().State(), FocusAgent)

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
	// Focus stays in the pane — Esc did NOT jump to the rail.
	testutil.Equal(t, p.Machine().State(), FocusAgent)
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

// leafPlanPage builds a coordinator orchestrator with one LIVE worker leaf bound
// to a real task, plus an AUTHORED plan (a planned downstream node blocked by the
// live worker) so the Plan pane renders the DAG rather than the empty-plan state
// (BUG-013). The live worker is the sole stage-0 root, so the plan cursor lands on
// its leaf node (id = its bound task "t-wkr"). Returns the page.
func leafPlanPage(t *testing.T) *HeraPage {
	t.Helper()
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	wkr := seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	plan2a := seedPlannedRole(t, d, orch, "2a")
	testutil.NoError(t, d.AddHeraBlock(plan2a.ID, wkr.ID)) // 2a blocked by the live worker
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
	return p
}

// TestPlanLeafEnter_JumpsWithinHeraNotTasks is BUG-002: Enter on a plain-leaf
// plan node selects that node's role in the rail and focuses the AGENT pane —
// staying in the Hera view — instead of switching to the Tasks tab. The page
// owns Plan().OnEnter (jumpToLeaf); the App wires no Tasks-jump.
func TestPlanLeafEnter_JumpsWithinHeraNotTasks(t *testing.T) {
	p := leafPlanPage(t)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true) // coordinator selected → details/plan

	// The plan shows the live worker as a leaf node; its node id is the bound task.
	pl := p.Plan()
	testutil.Equal(t, pl.CurrentNodeID(), "t-wkr")

	// Enter on the leaf → jump within Hera.
	pl.ActivateCursor()

	// The rail selection moved to the worker role, and the AGENT pane rebound to it
	// (applySelection fired via onSelectionChanged); focus is now the agent pane.
	testutil.Equal(t, p.SelectionContext().TaskID(), "t-wkr")
	testutil.Equal(t, p.detailsMode, false)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")
}

// TestPlanLeafEnter_OnEnterIsPageOwned: the page wires Plan().OnEnter itself (to
// jumpToLeaf), so the callback is non-nil even when the App never touches it.
func TestPlanLeafEnter_OnEnterIsPageOwned(t *testing.T) {
	p := NewHeraPage(memDB(t))
	testutil.Equal(t, p.Plan().OnEnter != nil, true)
}

// TestPlanLeafEnter_DeadSessionFiresReattach is BUG-009: Enter on a plan-leaf
// node whose agent session has EXITED must restart-and-join it (fire OnReattach),
// exactly as the rail's Enter does — not merely select it (which would leave the
// pane showing a dead session). Here no session resolver is wired, so the
// worker's task is treated as dead, the case the rail's Enter restarts.
func TestPlanLeafEnter_DeadSessionFiresReattach(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	wkr := seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	// An AUTHORED plan (planned 2a blocked by the live worker) so the Plan pane
	// renders the DAG with the worker as the sole stage-0 leaf (BUG-013).
	plan2a := seedPlannedRole(t, d, orch, "2a")
	testutil.NoError(t, d.AddHeraBlock(plan2a.ID, wkr.ID))
	p := NewHeraPage(d)
	// No SetSessionResolver → p.resolve == nil → the worker session is dead.
	var reattached Selection
	called := false
	p.OnReattach = func(s Selection) { reattached = s; called = true }
	p.Refresh()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)
	p.Draw(sim)

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true) // coordinator selected → details/plan
	pl := p.Plan()
	testutil.Equal(t, pl.CurrentNodeID(), "t-wkr")

	// Enter on the dead leaf → restart+join via OnReattach (like the rail).
	pl.ActivateCursor()

	testutil.Equal(t, called, true)
	testutil.Equal(t, reattached.FocusTaskID(), "t-wkr")
	// The jump-within-Hera behaviour is preserved: focus lands on the agent pane.
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}

// TestPlanLeafEnter_LiveWorkerFiresReattach: Enter on a LIVE worker leaf STILL
// fires OnReattach — identical to the rail's Enter gate. A SIGTSTP'd worker is
// "alive" but suspended, so the App-side handler is what decides whether to
// actually revive it; the view fires the same callback either way.
func TestPlanLeafEnter_LiveWorkerFiresReattach(t *testing.T) {
	p := leafPlanPage(t) // wires a LIVE resolver for t-coord and t-wkr
	var got Selection
	called := false
	p.OnReattach = func(s Selection) { got = s; called = true }

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	pl := p.Plan()
	testutil.Equal(t, pl.CurrentNodeID(), "t-wkr")

	pl.ActivateCursor()

	testutil.Equal(t, called, true)
	testutil.Equal(t, got.FocusTaskID(), "t-wkr")
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}

// TestPlanLeafEnter_LiveCoordinatorDoesNotReattach: a live coordinator stays
// navigate-only — leaf-Enter must NOT restart it. Coordinators are folded into
// the orch header (no selectable leaf row), so SelectByTaskID can't land on one
// and jumpToLeaf is a no-op; even if it resolved, the shared !IsCoordinator()
// gate (proven for the rail in TestKeyset_EnterLiveCoordinatorDoesNotReattach)
// would block the reattach. Either way: no OnReattach for a live coordinator.
func TestPlanLeafEnter_LiveCoordinatorDoesNotReattach(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": {id: "t-coord", alive: true}}))
	called := false
	p.OnReattach = func(Selection) { called = true }
	p.Refresh()

	p.jumpToLeaf("t-coord") // jumpToLeaf is the wired Plan().OnEnter handler

	testutil.Equal(t, called, false)
}

// TestPlanLeafEnter_ExpandsCollapsedAncestorBeforeJoin is BUG-007: pressing Enter
// on a plan-DAG leaf whose coordinator is COLLAPSED in the rail must expand the
// rail (uncollapse the ancestor coordinator) so the row builds, THEN join — not
// silently no-op. The plan graph projects from the full model, so the leaf node is
// still shown when the coordinator is folded; before the fix SelectByTaskID scanned
// only the built rows, so the folded coordinator swallowed the join.
func TestPlanLeafEnter_ExpandsCollapsedAncestorBeforeJoin(t *testing.T) {
	p := leafPlanPage(t) // LIVE resolver for t-coord + t-wkr
	var got Selection
	p.OnReattach = func(s Selection) { got = s }

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true) // coordinator selected → details/plan

	// Collapse the coordinator's orchestrator: its worker leaf row is no longer
	// built, so the rail cannot see it for a join.
	orchID := p.Rail().Model().Active[0].ID
	p.Rail().seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.ID == orchID })
	p.Rail().ToggleCollapse()
	testutil.Equal(t, p.Rail().OrchCollapsed(orchID), true)
	testutil.Equal(t, p.Rail().SelectByTaskID("t-wkr"), false) // invisible under the fold

	// The plan still lists the worker leaf (projected from the model, not the rail).
	pl := p.Plan()
	testutil.Equal(t, pl.CurrentNodeID(), "t-wkr")

	// Enter on the leaf expands the rail AND joins.
	pl.ActivateCursor()

	testutil.Equal(t, p.Rail().OrchCollapsed(orchID), false)  // rail re-expanded
	testutil.Equal(t, p.SelectionContext().TaskID(), "t-wkr") // selection landed
	testutil.Equal(t, p.detailsMode, false)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	testutil.Equal(t, got.FocusTaskID(), "t-wkr") // join (reattach) fired
}

// TestJumpToTask_ExpandsCollapsedAncestorAndReturnsTrue pins the exported
// entry point (extracted from jumpToLeaf for the unified task/role switcher,
// hera-nav-palette): calling JumpToTask directly does the same ancestor
// expansion + reattach + focus jumpToLeaf does, and reports success via its
// return value (jumpToLeaf, still the plan widget's OnEnter, ignores it).
func TestJumpToTask_ExpandsCollapsedAncestorAndReturnsTrue(t *testing.T) {
	p := leafPlanPage(t)
	var got Selection
	p.OnReattach = func(s Selection) { got = s }

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	orchID := p.Rail().Model().Active[0].ID
	p.Rail().seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.ID == orchID })
	p.Rail().ToggleCollapse()
	testutil.Equal(t, p.Rail().OrchCollapsed(orchID), true)

	ok := p.JumpToTask("t-wkr")

	testutil.Equal(t, ok, true)
	testutil.Equal(t, p.Rail().OrchCollapsed(orchID), false)
	testutil.Equal(t, p.SelectionContext().TaskID(), "t-wkr")
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	testutil.Equal(t, got.FocusTaskID(), "t-wkr")
}

// TestJumpToTask_UnknownTaskReturnsFalse: no rail row for the id → no-op,
// reported via the return value (the switcher falls back to the classic
// per-task agent view on false).
func TestJumpToTask_UnknownTaskReturnsFalse(t *testing.T) {
	p := leafPlanPage(t)
	testutil.Equal(t, p.JumpToTask("no-such-task"), false)
}

// TestJumpToTask_RemoteAndEmptyIDAreNoops mirrors jumpToLeaf's original guard.
func TestJumpToTask_RemoteAndEmptyIDAreNoops(t *testing.T) {
	remote := NewHeraPage(nil) // remote: no hera reader
	testutil.Equal(t, remote.JumpToTask("t-wkr"), false)

	p := leafPlanPage(t)
	testutil.Equal(t, p.JumpToTask(""), false)
}

// TestPlanEnter_DrillInDoesNotReattach: a Drillable sub-coordinator node fires
// OnDrillIn (the page-owned drill-in path), NOT OnEnter/jumpToLeaf, so the
// BUG-009 reattach must never fire for it. The planview widget routes the Enter
// key by Drillable, so the new jumpToLeaf reattach is structurally unreachable
// from a drill-in node.
func TestPlanEnter_DrillInDoesNotReattach(t *testing.T) {
	p := NewHeraPage(memDB(t))
	reattached := false
	p.OnReattach = func(Selection) { reattached = true }
	drilled := ""
	p.Plan().OnDrillIn = func(id string) { drilled = id }

	// A single Drillable node under the cursor.
	p.Plan().SetData([]planview.Node{{ID: "t-sub", Name: "sub", Drillable: true}}, nil)
	testutil.Equal(t, p.Plan().CurrentNodeID(), "t-sub")

	p.Plan().ActivateCursor()

	testutil.Equal(t, drilled, "t-sub")  // drill-in path fired
	testutil.Equal(t, reattached, false) // BUG-009 reattach did NOT fire
}

// TestRailSelectByTaskID finds and selects a role row by its bound task id,
// firing onSelectionChanged; an unknown id is a no-op returning false.
func TestRailSelectByTaskID(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.Refresh()
	r := p.Rail()

	var fired int
	r.SetOnSelectionChanged(func() { fired++ })
	testutil.Equal(t, r.SelectByTaskID("t-wkr"), true)
	testutil.Equal(t, r.Selected() != nil && r.Selected().TaskID == "t-wkr", true)
	testutil.Equal(t, fired >= 1, true)

	// Unknown task id: no row, no-op, false.
	before := r.CursorIndex()
	testutil.Equal(t, r.SelectByTaskID("nope"), false)
	testutil.Equal(t, r.CursorIndex(), before)
}
