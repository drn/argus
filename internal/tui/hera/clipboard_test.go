package hera

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// fullScreenText flattens every cell of a freshly-drawn page into one string so
// a test can assert a rendered border-title affordance is present.
func fullScreenText(t *testing.T, p *HeraPage, w, h int) string {
	t.Helper()
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	t.Cleanup(sim.Fini)
	sim.SetSize(w, h)
	p.SetRect(0, 0, w, h)
	p.Draw(sim)
	sim.Show()
	cells, cw, ch := sim.GetContents()
	var b strings.Builder
	for i := 0; i < cw*ch; i++ {
		if len(cells[i].Runes) > 0 {
			b.WriteRune(cells[i].Runes[0])
		}
	}
	return b.String()
}

// TestFocusedTerminalTaskID covers the focused-pane → task resolution that
// scopes the ctrl+y copy: coordinator pane → coord task, worker pane → worker
// task, and "" for the rail, coordinator-details mode, and remote.
func TestFocusedTerminalTaskID(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")

	coordSess := &fakeSession{id: "tc", alive: true}
	wkrSess := &fakeSession{id: "tw", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tc": coordSess, "tw": wkrSess}))
	p.Refresh()

	// Worker selected → agent pane bound to the worker, coord pane to the coord.
	testutil.Equal(t, selectRoleByName(p, "w"), true)
	testutil.Equal(t, p.detailsMode, false)

	p.Machine().SetRegion(FocusRail)
	testutil.Equal(t, p.FocusedTerminalTaskID(), "") // rail has no terminal
	p.Machine().SetRegion(FocusCoord)
	testutil.Equal(t, p.FocusedTerminalTaskID(), "tc")
	p.Machine().SetRegion(FocusAgent)
	testutil.Equal(t, p.FocusedTerminalTaskID(), "tw")

	// Coordinator selected → agent region is Details (no terminal), so FocusAgent
	// resolves to "" even though the region is "focused".
	testutil.Equal(t, selectOrchByName(p, "o"), true)
	testutil.Equal(t, p.detailsMode, true)
	p.Machine().SetRegion(FocusAgent)
	testutil.Equal(t, p.FocusedTerminalTaskID(), "")
	p.Machine().SetRegion(FocusCoord)
	testutil.Equal(t, p.FocusedTerminalTaskID(), "tc")

	// Remote mode never resolves a terminal task.
	rp := NewHeraPage(nil)
	rp.Machine().SetRegion(FocusCoord)
	testutil.Equal(t, rp.FocusedTerminalTaskID(), "")
}

// ctrlY is the key event ctrl+y dispatches.
func ctrlY() *tcell.EventKey { return tcell.NewEventKey(tcell.KeyCtrlY, 0, tcell.ModNone) }

// TestCtrlY_CopiesStagedFromFocusedPane: with a staged payload (clipReady) and a
// focused terminal pane, ctrl+y fires OnCopyClipboard with the FOCUSED pane's
// task and consumes the key (no PTY forward).
func TestCtrlY_CopiesStagedFromFocusedPane(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")

	coordSess := &fakeSession{id: "tc", alive: true}
	wkrSess := &fakeSession{id: "tw", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tc": coordSess, "tw": wkrSess}))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "w"), true)

	var copied string
	p.OnCopyClipboard = func(id string) { copied = id }
	p.SetClipboardHint(true)
	h := p.InputHandler()

	// Worker pane focused → copies the worker task.
	p.Machine().SetRegion(FocusAgent)
	h(ctrlY(), noFocus)
	testutil.Equal(t, copied, "tw")
	testutil.Equal(t, len(wkrSess.wrote), 0) // consumed, not forwarded to the PTY

	// Coordinator pane focused → copies the coordinator task.
	copied = ""
	p.Machine().SetRegion(FocusCoord)
	h(ctrlY(), noFocus)
	testutil.Equal(t, copied, "tc")
	testutil.Equal(t, len(coordSess.wrote), 0)
}

// TestCtrlY_AlwaysInterceptsWhenNotStaged: with no staged payload (clipReady
// false), ctrl+y is still intercepted — it fires OnCopyClipboard with the
// focused pane's task (so the App can flash "Nothing to copy") and never
// forwards the key to the PTY.
func TestCtrlY_AlwaysInterceptsWhenNotStaged(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")

	wkrSess := &fakeSession{id: "tw", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tc": {id: "tc", alive: true}, "tw": wkrSess}))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "w"), true)

	var copied string
	p.OnCopyClipboard = func(id string) { copied = id }
	p.SetClipboardHint(false) // nothing staged
	p.Machine().SetRegion(FocusAgent)

	h := p.InputHandler()
	h(ctrlY(), noFocus)
	testutil.Equal(t, copied, "tw")          // callback still fires, scoped to the focused pane
	testutil.Equal(t, len(wkrSess.wrote), 0) // consumed, never forwarded to the PTY
}

// TestCtrlY_InertOnRailAndDetails: ctrl+y never copies when the focused region
// has no terminal pane (rail, or coordinator-details mode), even with a staged
// payload, and never panics when the callback is unwired.
func TestCtrlY_InertOnRailAndDetails(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")

	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tc": {id: "tc", alive: true}, "tw": {id: "tw", alive: true}}))
	p.Refresh()

	var copied string
	p.OnCopyClipboard = func(id string) { copied = id }
	p.SetClipboardHint(true)
	h := p.InputHandler()

	// Rail focused (worker selected) → no terminal, nothing copied.
	testutil.Equal(t, selectRoleByName(p, "w"), true)
	p.Machine().SetRegion(FocusRail)
	h(ctrlY(), noFocus)
	testutil.Equal(t, copied, "")

	// Coordinator selected → agent region is Details; FocusAgent has no terminal.
	testutil.Equal(t, selectOrchByName(p, "o"), true)
	testutil.Equal(t, p.detailsMode, true)
	p.Machine().SetRegion(FocusAgent)
	h(ctrlY(), noFocus)
	testutil.Equal(t, copied, "")

	// Unwired callback + staged + focused terminal: must not panic.
	p.OnCopyClipboard = nil
	testutil.Equal(t, selectRoleByName(p, "w"), true)
	p.Machine().SetRegion(FocusCoord)
	h(ctrlY(), noFocus) // no panic
}

// TestHeraClipboardHint covers the pure border-hint helper.
func TestHeraClipboardHint(t *testing.T) {
	testutil.Equal(t, heraClipboardHint(false), "")
	testutil.Equal(t, heraClipboardHint(true), clipboardHintSuffix)
}

// TestCtrlY_HintRendersOnFocusedPane: with clipReady set and the worker pane
// focused, Draw advertises the affordance on the AGENT pane's border title (and
// not the coordinator's); clearing the hint removes it.
func TestCtrlY_HintRendersOnFocusedPane(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")

	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tc": {id: "tc", alive: true}, "tw": {id: "tw", alive: true}}))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "w"), true)
	p.Machine().SetRegion(FocusAgent)

	p.SetClipboardHint(true)
	testutil.Contains(t, fullScreenText(t, p, 120, 30), "(ctrl+y copy)")

	p.SetClipboardHint(false)
	if strings.Contains(fullScreenText(t, p, 120, 30), "(ctrl+y copy)") {
		t.Errorf("hint should be gone after SetClipboardHint(false)")
	}
}
