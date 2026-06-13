package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// drawnPage builds a worker+coordinator page, draws it once (so the focus
// machine learns the right regions are present and region rects are recorded),
// and returns it.
func drawnPage(t *testing.T) *HeraPage {
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
	p.Draw(sim)
	return p
}

func TestPanes_PasteRouting(t *testing.T) {
	// Remote → no-op, no panic.
	NewHeraPage(nil).PasteHandler()("x", noFocus)

	p := drawnPage(t)
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	// Focus the coordinator pane and paste → routed to coordPane's live session.
	p.Machine().Advance() // rail → coord
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	p.PasteHandler()("hi-coord", noFocus)

	// Focus the agent pane and paste → routed to agentPane's live session.
	p.Machine().Advance() // coord → agent
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	p.PasteHandler()("hi-agent", noFocus)

	// Coordinator selected → details mode → agent paste is a no-op (no terminal).
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	testutil.Equal(t, p.detailsMode, true)
	p.PasteHandler()("ignored", noFocus)
}

func TestPanes_MouseRoutingByRegion(t *testing.T) {
	p := drawnPage(t)
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	mh := p.MouseHandler()

	var focused tview.Primitive
	setFocus := func(prim tview.Primitive) { focused = prim }

	// Click in the rail region (x=2): focus → rail, anchored to page.
	mh(tview.MouseLeftClick, tcell.NewEventMouse(2, 10, tcell.Button1, tcell.ModNone), setFocus)
	testutil.Equal(t, p.Machine().State(), FocusRail)
	testutil.Equal(t, focused, tview.Primitive(p))

	// Click in the coordinator region.
	mh(tview.MouseLeftClick, tcell.NewEventMouse(p.coordX+1, 10, tcell.Button1, tcell.ModNone), setFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)

	// Click in the agent region.
	mh(tview.MouseLeftClick, tcell.NewEventMouse(p.agentX+1, 10, tcell.Button1, tcell.ModNone), setFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)

	// Scroll (non-click) over the agent region: forwarded, focus unchanged.
	mh(tview.MouseScrollUp, tcell.NewEventMouse(p.agentX+1, 10, tcell.Button1, tcell.ModNone), setFocus)
}

func TestPanes_RegionAt(t *testing.T) {
	p := drawnPage(t)
	testutil.Equal(t, p.regionAt(2), FocusRail)
	testutil.Equal(t, p.regionAt(p.coordX+1), FocusCoord)
	testutil.Equal(t, p.regionAt(p.agentX+1), FocusAgent)
}

func TestPanes_SyncPanesLocal(t *testing.T) {
	p := drawnPage(t)
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	// Draw set pendingResize on the agent pane; SyncPanes applies it (the live
	// fakeSession records the Resize). Just assert it doesn't panic.
	p.SyncPanes()
}

func TestPanes_ForwardKeyDeadAndDetails(t *testing.T) {
	p := drawnPage(t)
	// Dead session → forwardKey drops the keystroke (no panic).
	dead := &fakeSession{id: "x", alive: false}
	p.AgentPane().SetSession(dead)
	p.forwardKey(p.AgentPane(), tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone))
	// PgDn scroll branch.
	p.forwardKey(p.AgentPane(), tcell.NewEventKey(tcell.KeyPgDn, 0, tcell.ModNone))

	// InputHandler in FocusAgent + details mode ignores keystrokes.
	testutil.Equal(t, selectRoleByName(p, "coord"), true)
	p.Machine().Advance()
	p.Machine().Advance()
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'z', tcell.ModNone), noFocus)
}
