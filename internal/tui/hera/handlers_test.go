package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

func noFocus(tview.Primitive) {}

func TestRail_InputHandlerKeys(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModel())
	h := r.InputHandler()

	// twoOrchModel's Active group renders behind its own "Active (2)" header
	// (add-kanban-focus-fold): rule(0), header(1), orch-1 header(2), wkr(3),
	// orch-2 header(4). The cursor auto-clamps forward onto the first
	// selectable row (2) since the header itself is never a landing spot.
	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.CursorIndex(), 3)
	h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, r.CursorIndex(), 2)

	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, r.CursorIndex(), 3)
	h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
	testutil.Equal(t, r.CursorIndex(), 2)

	// Space on the orch header collapses it (worker row vanishes); rule +
	// "Active (2)" header + orch-1 (collapsed) header + orch-2 header = 4 rows.
	h(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone), noFocus)
	testutil.Equal(t, r.Rows(), 4)

	// An unhandled key is a no-op.
	h(tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone), noFocus)
	testutil.Equal(t, r.Rows(), 4)
}

func TestPage_InputHandlerDelegatesToRail(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "t")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "t2")
	p := NewHeraPage(d)
	p.Refresh()

	// rule(0), "Active (1)" header(1), orch header(2), w(3) — cursor starts on
	// the orch header (2), first selectable row.
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 3)
}

// TestPage_CtrlAltArrowWalksFocus pins the Ctrl+Alt+Left/Right focus ladder:
// Right advances rail→coord→agent and Left retreats, mirroring Tab/BackTab so
// the regions can be reached without stealing a plain arrow from a focused pane.
func TestPage_CtrlAltArrowWalksFocus(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "t")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "t2")
	p := NewHeraPage(d)
	p.Refresh()
	h := p.InputHandler()
	mod := tcell.ModCtrl | tcell.ModAlt

	testutil.Equal(t, p.Machine().State(), FocusRail)
	h(tcell.NewEventKey(tcell.KeyRight, 0, mod), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	h(tcell.NewEventKey(tcell.KeyRight, 0, mod), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	h(tcell.NewEventKey(tcell.KeyRight, 0, mod), noFocus) // right-most, no-op
	testutil.Equal(t, p.Machine().State(), FocusAgent)

	h(tcell.NewEventKey(tcell.KeyLeft, 0, mod), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	h(tcell.NewEventKey(tcell.KeyLeft, 0, mod), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusRail)
	h(tcell.NewEventKey(tcell.KeyLeft, 0, mod), noFocus) // left-most, no-op
	testutil.Equal(t, p.Machine().State(), FocusRail)

	// A bare arrow (no Ctrl+Alt) must NOT walk the ladder — it falls through to
	// the focused region instead.
	h(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusRail)

	// EITHER modifier alone also walks the ladder — terminals are inconsistent
	// about which of Ctrl/Alt they report for this chord, so the loose check
	// accepts Ctrl-only and Alt-only too (mirrors the agent view's pane switch).
	h(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	h(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModAlt), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusRail)
}

func TestPage_PasteHandlerNoOp(t *testing.T) {
	p := NewHeraPage(nil)
	ph := p.PasteHandler()
	testutil.Equal(t, ph != nil, true)
	ph("ignored", noFocus) // must not panic
}

func TestPage_MouseHandlerAnchorsFocus(t *testing.T) {
	d := memDB(t)
	seedOrch(t, d, "o")
	p := NewHeraPage(d)
	p.Refresh()
	p.SetRect(0, 0, 100, 30)

	var focused tview.Primitive
	setFocus := func(prim tview.Primitive) { focused = prim }
	mh := p.MouseHandler()
	// A left click anywhere anchors focus back on the page wrapper.
	consumed, _ := mh(tview.MouseLeftClick, tcell.NewEventMouse(60, 12, tcell.Button1, tcell.ModNone), setFocus)
	_ = consumed
	testutil.Equal(t, focused, tview.Primitive(p))

	// Remote-mode page: still anchors focus, no rail hit-test.
	rp := NewHeraPage(nil)
	rp.SetRect(0, 0, 100, 30)
	focused = nil
	rmh := rp.MouseHandler()
	rmh(tview.MouseLeftClick, tcell.NewEventMouse(5, 5, tcell.Button1, tcell.ModNone), setFocus)
	testutil.Equal(t, focused, tview.Primitive(rp))
}

// TestRail_ScrollOffsetTracksCursor exercises adjustOffset's down- and
// up-scroll branches with more rows than the viewport height.
func TestRail_ScrollOffsetTracksCursor(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()

	roles := make([]RoleView, 20)
	for i := range roles {
		roles[i] = RoleView{RoleID: int64(100 + i), Name: "r", Live: true}
	}
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "o", Roles: roles}}})
	r.SetRect(0, 0, 30, 6) // tiny viewport forces scrolling

	// Drive the cursor to the bottom; offset must advance.
	for i := 0; i < 21; i++ {
		r.CursorDown()
	}
	r.Draw(sim)
	if r.offset == 0 {
		t.Error("expected offset to advance when cursor scrolled past viewport")
	}

	// Back to the top; offset must rewind to the first selectable row (2) — the
	// leading rule + "Active (1)" header (add-kanban-focus-fold) are never a
	// cursor landing spot, so the viewport follows the cursor there, not to
	// absolute row 0.
	for i := 0; i < 21; i++ {
		r.CursorUp()
	}
	r.Draw(sim)
	testutil.Equal(t, r.offset, 2)
}
