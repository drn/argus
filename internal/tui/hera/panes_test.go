package hera

import (
	"strings"
	"testing"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/planview"
	"github.com/gdamore/tcell/v2"
)

// simScreenText renders sim's full cell grid to a newline-joined string, for
// substring assertions against whatever a pane's Draw painted.
func simScreenText(sim tcell.SimulationScreen) string {
	w, h := sim.Size()
	var b strings.Builder
	for row := 0; row < h; row++ {
		for col := 0; col < w; col++ {
			content, _, _ := sim.Get(col, row)
			b.WriteString(content)
		}
		b.WriteRune('\n')
	}
	return b.String()
}

// fakeSession is a minimal agentview.TerminalAdapter identified by task ID, so
// tests can assert which task's session a pane is feeding from.
type fakeSession struct {
	id     string
	alive  bool
	resize [2]uint16 // last (rows, cols) passed to Resize
	wrote  []byte    // bytes received via WriteInput (for forwardKey assertions)
	output []byte    // bytes the pane reads as PTY output (drives the emulator)
}

func (f *fakeSession) WriteInput(p []byte, origin agentview.InputOrigin) (int, error) {
	f.wrote = append(f.wrote, p...)
	return len(p), nil
}
func (f *fakeSession) Resize(rows, cols uint16) error { f.resize = [2]uint16{rows, cols}; return nil }
func (f *fakeSession) RecentOutput() []byte           { return f.output }
func (f *fakeSession) RecentOutputTail(n int) []byte {
	if n >= len(f.output) {
		return f.output
	}
	return f.output[len(f.output)-n:]
}
func (f *fakeSession) RecentOutputTailWithTotal(n int) ([]byte, uint64) {
	return f.RecentOutputTail(n), uint64(len(f.output))
}
func (f *fakeSession) TotalWritten() uint64 { return uint64(len(f.output)) }
func (f *fakeSession) Alive() bool          { return f.alive }
func (f *fakeSession) PTYSize() (int, int)  { return 80, 24 }

// resolverFor builds a SessionResolver over a fixed task→session map.
func resolverFor(sessions map[string]*fakeSession) SessionResolver {
	return func(taskID string) agentview.TerminalAdapter {
		if s, ok := sessions[taskID]; ok && s != nil {
			return s
		}
		return nil // nil interface — the lookup-miss case
	}
}

// addTask inserts a bare task row so binding FK constraints are satisfied.
func addTask(t *testing.T, d *db.DB, id string) {
	t.Helper()
	testutil.NoError(t, d.Add(&model.Task{ID: id, Name: id, Status: model.StatusInProgress, Project: "p", CreatedAt: time.Now()}))
}

// selectRoleByName drives the rail cursor to the role with the given name,
// triggering applySelection via the selection callback. Returns false if no
// such role row exists.
func selectRoleByName(p *HeraPage, name string) bool {
	r := p.Rail()
	for r.CursorIndex() > 0 {
		before := r.CursorIndex()
		r.CursorUp()
		if r.CursorIndex() == before {
			break
		}
	}
	for i := 0; i < r.Rows()+1; i++ {
		if sel := r.Selected(); sel != nil && sel.Name == name {
			return true
		}
		before := r.CursorIndex()
		r.CursorDown()
		if r.CursorIndex() == before {
			return false
		}
	}
	return false
}

// selectOrchByName lands the rail cursor on the orchestrator header with the
// given name. After the coordinator fold a header selection IS the coordinator
// selection (details mode), so tests that exercise coordinator behaviour select
// the header here rather than a (no-longer-rendered) coord child row.
func selectOrchByName(p *HeraPage, name string) bool {
	r := p.Rail()
	for r.CursorIndex() > 0 {
		before := r.CursorIndex()
		r.CursorUp()
		if r.CursorIndex() == before {
			break
		}
	}
	for i := 0; i < r.Rows()+1; i++ {
		if o := r.SelectedOrch(); o != nil && o.Name == name && r.Selected() == nil {
			return true
		}
		before := r.CursorIndex()
		r.CursorDown()
		if r.CursorIndex() == before {
			return false
		}
	}
	return false
}

// TestPanes_WorkerSelectionFeedsAgentPane is a locked must-have: selecting a
// worker role feeds the AGENT pane from that task's runner session, and the
// HERA pane from the orchestrator's coordinator.
func TestPanes_WorkerSelectionFeedsAgentPane(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	coordSess := &fakeSession{id: "t-coord", alive: true}
	wkrSess := &fakeSession{id: "t-wkr", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": coordSess, "t-wkr": wkrSess}))
	p.Refresh()

	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.detailsMode, false)
	// AGENT pane shows the worker's session; HERA pane the coordinator's.
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")
	testutil.Equal(t, p.CoordPane().Session().(*fakeSession).id, "t-coord")
	testutil.Equal(t, p.SelectionContext().TaskID(), "t-wkr")
}

// TestPanes_IsBoundToTask proves IsBoundToTask recognizes both the
// coordinator and the selected worker's task as "currently shown by the Hera
// view" — regardless of which pane has keyboard focus — and rejects an
// unrelated or empty task ID. The App uses this (via isViewingTaskSession) to
// decide whether a size-drift kick's exit handler should auto-restart in
// place instead of letting the task settle at InReview (BUG-076): the
// coordinator pane is bound the whole time a worker under it is selected, so
// a live coordinator must read as "bound" even while the rail cursor sits on
// the worker row.
func TestPanes_IsBoundToTask(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	coordSess := &fakeSession{id: "t-coord", alive: true}
	wkrSess := &fakeSession{id: "t-wkr", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": coordSess, "t-wkr": wkrSess}))
	p.Refresh()
	testutil.Equal(t, p.IsBoundToTask(""), false)

	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.IsBoundToTask("t-wkr"), true)   // agent pane
	testutil.Equal(t, p.IsBoundToTask("t-coord"), true) // coord pane, unfocused
	testutil.Equal(t, p.IsBoundToTask("unrelated-task"), false)
	testutil.Equal(t, p.IsBoundToTask(""), false)
}

// kickRecorder wires a HeraPage's RerenderKicker to a capturing slice plus a
// fake, test-controlled clock (see HeraPage.SetKickClock) so debounce tests
// can advance time deterministically without a real 300ms sleep.
type kickRecord struct {
	taskID string
	cols   uint16
}

func newKickRecorder(p *HeraPage) (kicks *[]kickRecord, advance func(time.Duration)) {
	var recorded []kickRecord
	p.SetRerenderKicker(func(taskID string, cols uint16, onDeferred func()) {
		recorded = append(recorded, kickRecord{taskID, cols})
	})
	now := time.Now()
	p.SetKickClock(func() time.Time { return now })
	return &recorded, func(d time.Duration) { now = now.Add(d) }
}

// newDeferringKickRecorder is like newKickRecorder but every recorded kick
// also immediately invokes its onDeferred callback — simulating the App
// resolving the decision as RerenderDeferBusy/RerenderDeferPrompt (BUG-077).
func newDeferringKickRecorder(p *HeraPage) (kicks *[]kickRecord, advance func(time.Duration)) {
	var recorded []kickRecord
	p.SetRerenderKicker(func(taskID string, cols uint16, onDeferred func()) {
		recorded = append(recorded, kickRecord{taskID, cols})
		if onDeferred != nil {
			onDeferred()
		}
	})
	now := time.Now()
	p.SetKickClock(func() time.Time { return now })
	return &recorded, func(d time.Duration) { now = now.Add(d) }
}

func kicksFor(kicks []kickRecord, taskID string) []uint16 {
	var cols []uint16
	for _, k := range kicks {
		if k.taskID == taskID {
			cols = append(cols, k.cols)
		}
	}
	return cols
}

// TestPanes_DrawInvokesRerenderKicker proves Draw evaluates the wired
// RerenderKicker with each pane's OWN fresh width — for BOTH the coordinator
// pane and the worker/agent pane — but debounces the actual invocation: the
// FIRST Draw after a genuine bind only arms the pending kick (KickDebounce
// design), it does not fire immediately. Only a LATER Draw, once the dwell
// has elapsed for the SAME bound task, actually invokes the kicker, exactly
// once. The check is evaluated from Draw (not bindPane) specifically because
// bindPane runs in the input handler, before Draw has had a chance to give a
// newly-shown pane (e.g. the agent pane, hidden while a coordinator was
// selected in details mode) a real rect — see maybeKickPaneRerender's doc
// comment.
func TestPanes_DrawInvokesRerenderKicker(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	coordSess := &fakeSession{id: "t-coord", alive: true}
	wkrSess := &fakeSession{id: "t-wkr", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": coordSess, "t-wkr": wkrSess}))
	kicks, advance := newKickRecorder(p)
	p.Refresh()

	// Select the worker BEFORE any Draw — this is exactly the sequence that
	// would silently miss the kick if it were evaluated in bindPane instead
	// of Draw: the agent pane has never been shown yet (details mode, since
	// Refresh's default selection lands on the coordinator/orch header), so
	// its tracked width is still zero at bind time.
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.agentBound, "t-wkr")
	testutil.Equal(t, p.coordBound, "t-coord")

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)
	p.Draw(sim)

	// First Draw after a genuine bind only ARMS the dwell — no kick yet.
	testutil.Equal(t, len(*kicks), 0)

	// Advance the clock past the dwell and draw again: the SAME tasks are
	// still bound, so the kick fires now, exactly once per pane.
	advance(KickDebounce)
	p.Draw(sim)

	wkrKicks := kicksFor(*kicks, "t-wkr")
	coordKicks := kicksFor(*kicks, "t-coord")
	if len(wkrKicks) != 1 {
		t.Fatalf("expected exactly one kick for the worker/agent pane bind, got %d", len(wkrKicks))
	}
	if len(coordKicks) != 1 {
		t.Fatalf("expected exactly one kick for the coordinator pane bind, got %d", len(coordKicks))
	}
	for _, c := range append(wkrKicks, coordKicks...) {
		if c == 0 {
			t.Errorf("kick used cols=0 — pane width wasn't resolved from a real rect")
		}
	}

	// A further Draw at the same bound tasks must not re-invoke the kicker.
	*kicks = nil
	p.Draw(sim)
	testutil.Equal(t, len(*kicks), 0)

	// Unbinding and rebinding to the SAME task must re-evaluate (kickedFor is
	// reset on unbind) rather than being silently suppressed forever — but it
	// still goes through the dwell again.
	p.bindPane(p.AgentPane(), &p.agentBound, &p.agentKickedFor, &p.agentKickPending, "", "agent")
	p.bindPane(p.AgentPane(), &p.agentBound, &p.agentKickedFor, &p.agentKickPending, "t-wkr", "agent")
	*kicks = nil
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 0) // armed, not yet fired
	advance(KickDebounce)
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 1)
}

// TestPanes_KickDebounce_FastTraversalNeverKicks is the kick-storm regression:
// a fast multi-row rail traversal (Cmd+Arrow across several rows) rebinds the
// agent pane to a DIFFERENT task on every hop, none of them staying bound for
// the full debounce dwell. None of the transiently-bound tasks should ever be
// kicked — each hop's pending kick must be discarded, un-fired, by the next
// hop's rebind.
func TestPanes_KickDebounce_FastTraversalNeverKicks(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	for _, name := range []string{"a", "b", "c"} {
		seedBoundRole(t, d, orch, name, db.HeraKindWorker, "t-"+name)
	}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-a": {id: "t-a", alive: true},
		"t-b": {id: "t-b", alive: true},
		"t-c": {id: "t-c", alive: true},
	}))
	kicks, advance := newKickRecorder(p)
	p.Refresh()

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)

	// Hop across all three rows in quick succession, well under the dwell
	// between each hop — exactly a fast Cmd+Arrow traversal.
	for _, name := range []string{"a", "b", "c"} {
		testutil.Equal(t, selectRoleByName(p, name), true)
		p.Draw(sim)
		advance(KickDebounce / 10)
	}

	testutil.Equal(t, len(*kicks), 0)

	// Confirm the mechanism isn't just permanently wedged: staying on the
	// LAST row past the dwell still kicks it.
	advance(KickDebounce)
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(*kicks, "t-c")) >= 1, true)
	testutil.Equal(t, len(kicksFor(*kicks, "t-a")), 0)
	testutil.Equal(t, len(kicksFor(*kicks, "t-b")), 0)
}

// TestPanes_KickDebounce_DwellAndStayStillKicks proves the anti-corruption
// kick is not silently lost — only delayed — for the legitimate case: a rail
// selection that stays put past the dwell still fires exactly once.
func TestPanes_KickDebounce_DwellAndStayStillKicks(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": {id: "t-wkr", alive: true}}))
	kicks, advance := newKickRecorder(p)
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)

	p.Draw(sim) // arms
	testutil.Equal(t, len(*kicks), 0)
	advance(KickDebounce)
	p.Draw(sim) // fires
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 1)
}

// TestPanes_KickDebounce_UnbindMidDwellThenRebindSameTask proves an unbind
// that happens mid-dwell doesn't let a rebind to the SAME task fire the kick
// any earlier than a fresh dwell would — bindPane's unbind path resets
// kickedFor, and the rebind's taskID comparison in maybeKickPaneRerender is
// what re-arms pending, so the dwell always restarts cleanly rather than
// firing on stale pending state (design.md Decision 2).
func TestPanes_KickDebounce_UnbindMidDwellThenRebindSameTask(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": {id: "t-wkr", alive: true}}))
	kicks, advance := newKickRecorder(p)
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)

	p.Draw(sim) // arms against t-wkr
	advance(KickDebounce / 2)

	// Unbind, then immediately rebind to the SAME task mid-dwell.
	p.bindPane(p.AgentPane(), &p.agentBound, &p.agentKickedFor, &p.agentKickPending, "", "agent")
	p.bindPane(p.AgentPane(), &p.agentBound, &p.agentKickedFor, &p.agentKickPending, "t-wkr", "agent")

	// Only half the ORIGINAL dwell has elapsed — not yet due.
	p.Draw(sim)
	testutil.Equal(t, len(*kicks), 0)

	// A full dwell past the rebind fires exactly once.
	advance(KickDebounce)
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 1)
}

// TestPanes_KickRetriesAfterDeferred is the BUG-077 regression: a kick that
// the App reports as deferred (agent.RerenderDeferBusy / RerenderDeferPrompt
// — simulated here by a kicker that immediately invokes its onDeferred
// callback) must NOT permanently stop maybeKickPaneRerender from trying
// again. Before the fix, kickedFor was set unconditionally at the moment
// kickRerender was CALLED, regardless of whether the underlying decision
// fired or deferred — so a pane bound while its agent was busy (the ordinary
// case for "mid active streaming") never got a second chance at the
// kill+resume for the rest of that bind, exactly the reported garbled-Hera-
// pane symptom. The retry must wait a full KickRetryInterval, not just
// another KickDebounce — see KickRetryInterval's doc comment.
func TestPanes_KickRetriesAfterDeferred(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": {id: "t-wkr", alive: true}}))
	kicks, advance := newDeferringKickRecorder(p)
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)

	p.Draw(sim) // arms
	testutil.Equal(t, len(*kicks), 0)
	advance(KickDebounce)
	p.Draw(sim) // fires — the recorder immediately defers it
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 1)

	// Staying bound past another KickDebounce (but short of KickRetryInterval)
	// must NOT retry yet — a deferred outcome waits the LONGER interval, not
	// the initial per-bind dwell.
	advance(KickDebounce)
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 1)

	// Once the full retry interval has elapsed, the SAME still-bound task is
	// retried — exactly once.
	advance(KickRetryInterval)
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 2)
}

// TestPanes_KickDoesNotRetryWhenResolved proves the mirror image: a kick that
// resolves WITHOUT deferring (never calls onDeferred — matching a fired
// RerenderKick or a no-drift RerenderSkip) must stay a one-shot even across a
// KickRetryInterval-sized gap, not just the single extra Draw the older
// TestPanes_DrawInvokesRerenderKicker checked.
func TestPanes_KickDoesNotRetryWhenResolved(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": {id: "t-wkr", alive: true}}))
	kicks, advance := newKickRecorder(p)
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)

	p.Draw(sim)
	advance(KickDebounce)
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 1)

	advance(KickRetryInterval * 2)
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(*kicks, "t-wkr")), 1)
}

// TestPanes_KickDeferredCallback_StaleAfterRebindIsNoop proves a late-arriving
// onDeferred callback from a PREVIOUS bind cannot clobber a DIFFERENT task's
// freshly-armed kick state after a rebind — mirroring the existing
// unbind/rebind-mid-dwell rigor (TestPanes_KickDebounce_UnbindMidDwellThenRebindSameTask)
// for the new retry path.
func TestPanes_KickDeferredCallback_StaleAfterRebindIsNoop(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "a", db.HeraKindWorker, "t-a")
	seedBoundRole(t, d, orch, "b", db.HeraKindWorker, "t-b")
	p := NewHeraPage(d)

	var pendingDeferred func()
	var recorded []kickRecord
	p.SetRerenderKicker(func(taskID string, cols uint16, onDeferred func()) {
		recorded = append(recorded, kickRecord{taskID, cols})
		pendingDeferred = onDeferred // capture — invoked manually, late, below
	})
	now := time.Now()
	p.SetKickClock(func() time.Time { return now })
	advance := func(d time.Duration) { now = now.Add(d) }

	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-a": {id: "t-a", alive: true},
		"t-b": {id: "t-b", alive: true},
	}))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "a"), true)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)

	p.Draw(sim)
	advance(KickDebounce)
	p.Draw(sim) // fires the kick for t-a; onDeferred captured, NOT yet invoked
	testutil.Equal(t, len(kicksFor(recorded, "t-a")), 1)
	deferredForA := pendingDeferred
	if deferredForA == nil {
		t.Fatal("expected onDeferred to be captured for t-a's kick")
	}

	// Rebind the SAME pane to a different task before the stale callback ever
	// fires, and let ITS kick fire too (first Draw only arms the fresh bind's
	// dwell — same two-Draw shape as every other kick test in this file).
	testutil.Equal(t, selectRoleByName(p, "b"), true)
	testutil.Equal(t, p.agentBound, "t-b")
	p.Draw(sim) // arms
	advance(KickDebounce)
	p.Draw(sim) // fires
	testutil.Equal(t, len(kicksFor(recorded, "t-b")), 1)
	testutil.Equal(t, p.agentKickedFor, "t-b")

	// The stale t-a callback arrives late — it must be a no-op: it must NOT
	// reset agentKickedFor (currently "t-b") or re-arm agentKickPending, which
	// would corrupt t-b's already-resolved state.
	deferredForA()
	testutil.Equal(t, p.agentKickedFor, "t-b")
	testutil.Equal(t, p.agentKickPending.taskID, "t-b")

	// Confirm it wasn't silently re-armed for a retry either.
	advance(KickRetryInterval * 2)
	p.Draw(sim)
	testutil.Equal(t, len(kicksFor(recorded, "t-a")), 1)
	testutil.Equal(t, len(kicksFor(recorded, "t-b")), 1)
}

// TestPanes_CoordinatorSelectionShowsDetails is a locked must-have: selecting a
// coordinator role renders the worker-list Details (no agent terminal) and
// feeds the HERA pane from the coordinator's session.
func TestPanes_CoordinatorSelectionShowsDetails(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	coordSess := &fakeSession{id: "t-coord", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": coordSess}))
	p.Refresh()

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)
	testutil.Equal(t, p.CoordPane().Session().(*fakeSession).id, "t-coord")
	// Agent pane is unbound in details mode.
	testutil.Nil(t, p.AgentPane().Session())
	testutil.Equal(t, p.SelectionContext().IsCoordinator(), true)
}

// TestPanes_MultiBindingFeedsCorrectContext is THE locked must-have for the
// multi-binding disambiguator AND the BUG-004 fix: one task that is a worker in
// orchestrator A and the coordinator of orchestrator B nests B's header under
// A's a-worker row (the bridge). Selecting that bridging row is a SUB-COORDINATOR
// selection — Details mode for orchestrator B, NOT the agent terminal — while the
// MUTATION context still points at the parent worker role under A (so Ctrl+D
// deletes the worker, never the child orchestrator). The disambiguator that keeps
// these two facts separate is the orchestrator, never the bare task id.
func TestPanes_MultiBindingFeedsCorrectContext(t *testing.T) {
	d := memDB(t)
	orchA := seedOrch(t, d, "orch-a")
	orchB := seedOrch(t, d, "orch-b")

	aCoord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchA, Name: "a-coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	aWorker, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchA, Name: "a-worker", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	bCoord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: orchB, Name: "b-coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)

	addTask(t, d, "t-a-coord")
	addTask(t, d, "shared") // worker in A, coordinator in B
	for _, b := range []struct {
		role int64
		task string
		wt   string
	}{
		{aCoord.ID, "t-a-coord", "/wt/ac"},
		{aWorker.ID, "shared", "/wt/aw"},
		{bCoord.ID, "shared", "/wt/bc"},
	} {
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: b.role, ArgusTaskID: b.task, WorktreePath: b.wt})
		testutil.NoError(t, err)
	}

	acSess := &fakeSession{id: "t-a-coord", alive: true}
	sharedSess := &fakeSession{id: "shared", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-a-coord": acSess, "shared": sharedSess}))
	p.Refresh()

	// orch-b's coordinator IS the shared task, which is also a-worker under A, so
	// orch-b nests under A's a-worker row. That bridging row is a sub-coordinator:
	// selecting it shows orch-b's Details (no agent terminal), and the HERA pane
	// feeds from the sub-coord's OWN session (orch-b's coordinator = shared).
	testutil.Equal(t, selectRoleByName(p, "a-worker"), true)
	testutil.Equal(t, p.detailsMode, true)
	// The mutation context is unchanged — still the parent worker role under A.
	testutil.Equal(t, p.SelectionContext().Orch.Name, "orch-a")
	testutil.Equal(t, p.SelectionContext().BridgeChildOrchID, orchB)
	// HERA pane shows the sub-coord's own session; the agent terminal is unbound.
	testutil.Equal(t, p.CoordPane().Session().(*fakeSession).id, "shared")
	testutil.Nil(t, p.AgentPane().Session())
}

// TestPanes_SubCoordSelectionShowsDetails is the BUG-004 regression: a worker
// that became a sub-coordinator (it spawned a child orchestrator, so its worker
// row bridges that child) must render the child's Details pane — roster + plan
// graph — NOT the agent terminal. Before the fix the sub-coord was treated as a
// plain worker (Role.Kind==worker → IsCoordinator()==false) so it only ever
// showed in the agent pane and its detail view was unreachable.
func TestPanes_SubCoordSelectionShowsDetails(t *testing.T) {
	d := memDB(t)
	parent := seedOrch(t, d, "parent")
	child := seedOrch(t, d, "child")

	seedBoundRole(t, d, parent, "p-coord", db.HeraKindCoordinator, "t-pcoord")
	// The sub-coord task is bound BOTH as a worker under the parent and as the
	// coordinator of the child orchestrator (born-bound sub-coordinator spawn).
	subRole, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: parent, Name: "sub", Kind: db.HeraKindWorker, ArgusProject: "p"})
	testutil.NoError(t, err)
	childCoord, err := d.CreateHeraRole(db.CreateHeraRoleInput{OrchestratorID: child, Name: "c-coord", Kind: db.HeraKindCoordinator, ArgusProject: "p"})
	testutil.NoError(t, err)
	// And the child has its own plain worker, so the tree has a real subtree.
	seedBoundRole(t, d, child, "c-wkr", db.HeraKindWorker, "t-cwkr")

	addTask(t, d, "t-sub")
	for _, b := range []struct {
		role int64
		task string
	}{
		{subRole.ID, "t-sub"},
		{childCoord.ID, "t-sub"},
	} {
		_, err = d.CreateHeraBinding(db.CreateHeraBindingInput{RoleID: b.role, ArgusTaskID: b.task, WorktreePath: "/wt/" + b.task})
		testutil.NoError(t, err)
	}

	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-pcoord": {id: "t-pcoord", alive: true},
		"t-sub":    {id: "t-sub", alive: true},
		"t-cwkr":   {id: "t-cwkr", alive: true},
	}))
	p.Refresh()

	testutil.Equal(t, selectRoleByName(p, "sub"), true)
	// Sub-coord selection → Details mode (roster + plan), not the agent terminal.
	testutil.Equal(t, p.detailsMode, true)
	testutil.Nil(t, p.AgentPane().Session())
	// HERA pane shows the sub-coord's OWN session (== the child's coordinator).
	testutil.Equal(t, p.CoordPane().Session().(*fakeSession).id, "t-sub")
	// The Details + plan reflect the CHILD orchestrator: the page rebuilt the plan
	// for the child, whose worker roles project as plan nodes (the child's own
	// worker is in; coordinators — including the parent's — are never plan nodes).
	m := p.Rail().Model()
	nodes, _ := heraPlanNodesWithBridge(m.OrchByID(child), m.bridgeIndex())
	ids := planTaskIDs(nodes)
	testutil.Equal(t, ids["t-cwkr"], true)    // child's worker is a plan node
	testutil.Equal(t, ids["t-pcoord"], false) // the PARENT coordinator is not
	testutil.Equal(t, ids["t-sub"], false)    // the child's coordinator is not a plan node
	// Mutation context stays on the parent worker role (Ctrl+D safety preserved).
	testutil.Equal(t, p.SelectionContext().Orch.Name, "parent")
	testutil.Equal(t, p.SelectionContext().BridgeChildOrchID, child)
}

// planTaskIDs collapses a plan node set to a node-id presence map for assertions.
func planTaskIDs(nodes []planview.Node) map[string]bool {
	out := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		out[n.ID] = true
	}
	return out
}

// TestPanes_RemoteModeNeverFeeds proves remote-mode panes stay unavailable: no
// resolver, no session bound, no panic on Draw.
func TestPanes_RemoteModeNeverFeeds(t *testing.T) {
	p := NewHeraPage(nil) // remote
	testutil.Equal(t, p.IsRemote(), true)
	p.Refresh()
	p.applySelection() // guarded no-op
	p.Reconcile()      // guarded no-op
	p.SyncPanes()      // guarded no-op
	testutil.Nil(t, p.CoordPane().Session())
	testutil.Nil(t, p.AgentPane().Session())

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)
	p.Draw(sim) // banner path, no panic
}

// TestPanes_BindLifecycle exercises bind → no-op-on-same-task → unbind.
func TestPanes_BindLifecycle(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	sess := &fakeSession{id: "t-wkr", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": sess}))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.agentBound, "t-wkr")

	// Re-applying the same selection is a no-op (session pointer unchanged).
	prev := p.AgentPane().Session()
	p.applySelection()
	testutil.Equal(t, p.AgentPane().Session(), prev)

	// Unbind: bindPane with "" clears the pane.
	p.bindPane(p.AgentPane(), &p.agentBound, &p.agentKickedFor, &p.agentKickPending, "", "agent")
	testutil.Equal(t, p.agentBound, "")
	testutil.Nil(t, p.AgentPane().Session())
}

// TestPanes_ReconcileLateBind proves a session that comes up after binding gets
// attached on the next tick reconcile.
func TestPanes_ReconcileLateBind(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	sessions := map[string]*fakeSession{} // no live sessions yet
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(sessions))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Nil(t, p.AgentPane().Session()) // bound to task, no live session

	// Session comes up; reconcile attaches it.
	sessions["t-wkr"] = &fakeSession{id: "t-wkr", alive: true}
	p.Reconcile()
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")
}

// TestPanes_ReconcileReplacesDeadSession is the BUG-013 regression: a pane
// holding a present-but-DEAD session (the daemon tore the stream down on a
// StreamLost relay / bounce while the agent process stayed alive, flipping
// Alive() false) MUST be re-resolved to a fresh live handle on the next tick —
// not left frozen until a full TUI restart. reconcileOne previously bailed on
// any present session and only ever late-bound a nil one.
func TestPanes_ReconcileReplacesDeadSession(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	live1 := &fakeSession{id: "t-wkr", alive: true}
	coordSess := &fakeSession{id: "t-coord", alive: true}
	sessions := map[string]*fakeSession{"t-coord": coordSess, "t-wkr": live1}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(sessions))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")

	// The stream dies while the agent process lives, then the provider re-dials a
	// FRESH live handle (distinct pointer) — exactly what the daemon client does
	// on a cache-miss Get after eviction.
	live1.alive = false
	live2 := &fakeSession{id: "t-wkr", alive: true}
	sessions["t-wkr"] = live2

	p.Reconcile()
	got := p.AgentPane().Session().(*fakeSession)
	testutil.Equal(t, got, live2) // dead handle replaced, not left frozen
}

// TestPanes_ReconcileResetsVTOnSessionSwap is the recycle-pane-artifact
// regression: a recycle_coord kill+respawn keeps the SAME task ID, so
// reconcileOne — not bindPane, which no-ops on an unchanged task — is the ONLY
// site that observes the session swap. It must fully reset the pane's VT/view
// state (ResetVT), not rely on SetSession's narrower reset (which only clears
// emu/emuFedTotal/scrollOffset/paintCacheValid). Diff mode is used here as a
// deterministic, exported-API-only stand-in for that broader state: Draw()
// short-circuits entirely while diffMode is set, so a pane left showing a diff
// overlay when the old session died would keep repainting that stale overlay
// forever instead of ever showing the freshly-recycled session's output —
// the same class of "prior session's cells survive the swap" bug that let the
// reported garbled text linger at the top of a just-recycled coordinator pane.
func TestPanes_ReconcileResetsVTOnSessionSwap(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	live1 := &fakeSession{id: "t-wkr", alive: true}
	coordSess := &fakeSession{id: "t-coord", alive: true}
	sessions := map[string]*fakeSession{"t-coord": coordSess, "t-wkr": live1}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(sessions))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "t-wkr")

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 10)
	p.AgentPane().SetRect(0, 0, 40, 10)

	// The pane happens to be showing a diff overlay (e.g. the user opened a
	// file diff on it) at the moment the coordinator recycles.
	p.AgentPane().EnterDiffMode("--- a/f\n+++ b/f\n@@ -1 +1 @@\n-old\n+new\n", "STALE-DIFF-MARKER")
	testutil.Equal(t, p.AgentPane().InDiffMode(), true)
	p.AgentPane().Draw(sim)
	sim.Show()
	testutil.Equal(t, strings.Contains(simScreenText(sim), "STALE-DIFF-MARKER"), true)

	// recycle_coord: same task, session killed and replaced by a fresh,
	// distinct live handle.
	live1.alive = false
	live2 := &fakeSession{id: "t-wkr", alive: true}
	sessions["t-wkr"] = live2

	p.Reconcile()

	got := p.AgentPane().Session().(*fakeSession)
	testutil.Equal(t, got, live2) // dead handle replaced, not left frozen
	// BUG: without ResetVT, diffMode (and the rest of the state SetSession
	// doesn't touch) survives the swap — the recycled session's own output
	// would never render.
	testutil.Equal(t, p.AgentPane().InDiffMode(), false)

	p.AgentPane().Draw(sim)
	sim.Show()
	testutil.Equal(t, strings.Contains(simScreenText(sim), "STALE-DIFF-MARKER"), false)
}

// TestPanes_ReconcileLeavesDeadSessionWhenNoLiveReplacement proves the pane is
// NOT thrashed when no live replacement exists: a finished session (provider
// returns nil, process really gone) keeps its dead handle so its buffered final
// output still backs log replay, and a not-yet-evicted identical handle is left
// for a later tick rather than needlessly resetting the emulator.
func TestPanes_ReconcileLeavesDeadSessionWhenNoLiveReplacement(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	dead := &fakeSession{id: "t-wkr", alive: true}
	sessions := map[string]*fakeSession{"t-coord": {id: "t-coord", alive: true}, "t-wkr": dead}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(sessions))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	// Process really gone: handle dead AND provider yields nothing live.
	dead.alive = false
	delete(sessions, "t-wkr")
	p.Reconcile()
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession), dead) // dead handle retained

	// Provider returns the SAME dead handle (cache not yet evicted) — also retained.
	sessions["t-wkr"] = dead
	p.Reconcile()
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession), dead)
}

// TestPanes_ForwardKeyReResolvesDeadSession proves the dropped-keystroke path is
// no longer a silent dead end: a keystroke into a pane whose session went dead
// re-resolves a fresh live handle and the keystroke reaches it.
func TestPanes_ForwardKeyReResolvesDeadSession(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	live1 := &fakeSession{id: "t-wkr", alive: true}
	sessions := map[string]*fakeSession{"t-wkr": live1}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(sessions))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	// Stream dies; provider re-dials a fresh live handle.
	live1.alive = false
	live2 := &fakeSession{id: "t-wkr", alive: true}
	sessions["t-wkr"] = live2

	p.forwardKey(p.AgentPane(), tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	// The keystroke reached the FRESH handle, not the dead one.
	testutil.Equal(t, string(live2.wrote), "x")
	testutil.Equal(t, len(live1.wrote), 0)
}

// TestPanes_ForwardKeyDropsWhenNoLiveSession proves a keystroke into a pane with
// no live session and no live replacement is dropped without panic (and the dead
// handle is left untouched).
func TestPanes_ForwardKeyDropsWhenNoLiveSession(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	dead := &fakeSession{id: "t-wkr", alive: false}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": dead}))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	p.forwardKey(p.AgentPane(), tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	testutil.Equal(t, len(dead.wrote), 0) // dropped, not delivered to a dead handle
}

// TestPanes_ForwardKeyEnterRevivesDeadPane is the BUG-001 regression: Enter into a
// focused pane with no live session fires OnReattach (the "Session not running -
// press Enter to start" overlay's promise) targeting the pane's bound task, while a
// non-Enter key into the same dead pane is still dropped (no revive, no write).
func TestPanes_ForwardKeyEnterRevivesDeadPane(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	dead := &fakeSession{id: "t-wkr", alive: false}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-coord": {id: "t-coord", alive: true},
		"t-wkr":   dead,
	}))
	var got []Selection
	p.OnReattach = func(sel Selection) { got = append(got, sel) }
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	// Enter on the dead agent pane revives the worker — its bound task, not the
	// coordinator's.
	p.forwardKey(p.AgentPane(), tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone))
	testutil.Equal(t, len(got), 1)
	testutil.Equal(t, got[0].FocusTaskID(), "t-wkr")
	testutil.Equal(t, len(dead.wrote), 0) // never written to a dead handle

	// A non-Enter key into the dead pane does NOT revive.
	p.forwardKey(p.AgentPane(), tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	testutil.Equal(t, len(got), 1)
}

// TestSmoke_HeraPaneEnterRevivesDeadSession drives the real event loop: a worker
// selection with a dead agent session, focus walked into the agent pane, then
// Enter — which must fire the revive path (BUG-001). Exercises the page
// InputHandler → forwardKey → pane InputHandler → OnReattach chain end to end.
func TestSmoke_HeraPaneEnterRevivesDeadSession(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")

	deadWkr := &fakeSession{id: "t-wkr", alive: false}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-coord": {id: "t-coord", alive: true},
		"t-wkr":   deadWkr,
	}))
	var revived []string
	p.OnReattach = func(sel Selection) { revived = append(revived, sel.FocusTaskID()) }
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)
	p.Draw(sim) // teach the focus machine both right regions are present

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)                // rail → coord
	h(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt), noFocus) // coord → agent
	testutil.Equal(t, p.Machine().State(), FocusAgent)

	// Enter on the focused dead-session pane fires the revive for the worker task.
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, len(revived), 1)
	testutil.Equal(t, revived[0], "t-wkr")
}

// TestPanes_FocusTraversal walks rail→coord→agent and back. Entering a pane
// from the rail is still Tab; once a terminal pane is focused the focus ladder
// is Ctrl+Alt+←/→ (Tab/Shift-Tab now pass through to the PTY — BUG-019), and
// Ctrl+Q escapes back to the rail.
func TestPanes_FocusTraversal(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": {id: "t-wkr", alive: true}}))
	p.Refresh()

	// Draw once so the focus machine learns both right regions are present.
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)
	p.Draw(sim)

	h := p.InputHandler()
	testutil.Equal(t, p.Machine().State(), FocusRail)
	// Rail-focused Tab still ENTERS a pane (the entry affordance is preserved).
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	// Once in a pane, pane↔pane movement is Ctrl+Alt+→ / Ctrl+Alt+← (Tab now
	// reaches the PTY instead of walking the ladder).
	h(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	h(tcell.NewEventKey(tcell.KeyLeft, 0, tcell.ModCtrl|tcell.ModAlt), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	// Ctrl+Q always escapes back to the rail — the no-trap guarantee.
	h(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusRail)
}

// TestPanes_TabPassesThroughToFocusedPane is the BUG-019 regression: once a
// terminal pane is focused, Tab and Shift-Tab must reach the agent PTY (so the
// agent's own autocomplete works, e.g. `/plugi`+Tab → `/plugin`) instead of
// walking the focus ladder. The keystroke bytes are the standard HT / CSI Z.
func TestPanes_TabPassesThroughToFocusedPane(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	coordSess := &fakeSession{id: "t-coord", alive: true}
	wkrSess := &fakeSession{id: "t-wkr", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": coordSess, "t-wkr": wkrSess}))
	p.Refresh()

	// Select a worker so the agent pane is a terminal (not details) and the
	// coordinator pane feeds the orchestrator's coordinator session.
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	testutil.Equal(t, p.detailsMode, false)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)
	p.Draw(sim)

	h := p.InputHandler()
	// Enter the coordinator pane from the rail.
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)

	// Tab and Shift-Tab now pass THROUGH to the coordinator PTY; focus stays put.
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	h(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	testutil.Equal(t, string(coordSess.wrote), "\t\x1b[Z")

	// Move to the agent pane via the focus ladder, then confirm passthrough there.
	h(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	testutil.Equal(t, string(wkrSess.wrote), "\t\x1b[Z")
}

// TestPanes_TabWalksLadderInDetailsMode proves the passthrough is scoped to
// TERMINAL panes: when a coordinator is selected the agent region renders the
// read-only Details/tree (no PTY), so Tab/Shift-Tab keep walking the focus
// ladder rather than being swallowed by a non-existent terminal.
func TestPanes_TabWalksLadderInDetailsMode(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": {id: "t-coord", alive: true}}))
	p.Refresh()

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)

	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(120, 30)
	p.SetRect(0, 0, 120, 30)
	p.Draw(sim)

	h := p.InputHandler()
	// rail → coord (Tab enters), coord → agent (Tab advances: coord pane is a
	// terminal so Tab there passes through, but the FIRST Tab from the rail still
	// enters). Then from the details (FocusAgent) region Backtab retreats — the
	// details region is not a terminal, so the ladder still works.
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	// Advance to the details (agent) region via the ladder.
	h(tcell.NewEventKey(tcell.KeyRight, 0, tcell.ModCtrl|tcell.ModAlt), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	// In details mode Backtab is NOT swallowed by a PTY — it retreats to coord.
	h(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
}

// TestPanes_ForwardKeyToFocusedSession proves keystrokes routed to a focused
// terminal pane reach its live session.
func TestPanes_ForwardKeyToFocusedSession(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	sess := &fakeSession{id: "t-wkr", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": sess}))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	// forwardKey to a live agent pane should not panic and PgUp scrolls.
	p.forwardKey(p.AgentPane(), tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	p.forwardKey(p.AgentPane(), tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone))
}

// TestPanes_ForwardKeySnapsScrollToBottom is the BUG-008 regression: typing into a
// scrolled-up Hera pane must snap the view back to the live tail (scrollOffset=0)
// so the input echoes on screen, while a scrollback-navigation key (PgUp/PgDn) must
// NOT snap.
func TestPanes_ForwardKeySnapsScrollToBottom(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "t-wkr")
	sess := &fakeSession{id: "t-wkr", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-wkr": sess}))
	p.Refresh()
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)

	pane := p.AgentPane()

	// Scroll up into history, then type — the keystroke must snap to bottom.
	pane.ScrollUp(10)
	testutil.Equal(t, pane.ScrollOffset() > 0, true)
	p.forwardKey(pane, tcell.NewEventKey(tcell.KeyRune, 'x', tcell.ModNone))
	testutil.Equal(t, pane.ScrollOffset(), 0)
	testutil.Equal(t, string(sess.wrote), "x")

	// A scrollback key (PgUp) must NOT snap — it scrolls up.
	p.forwardKey(pane, tcell.NewEventKey(tcell.KeyPgUp, 0, tcell.ModNone))
	testutil.Equal(t, pane.ScrollOffset() > 0, true)
}

// TestPanes_NoSyncOnDraw pins the CLAUDE.md UX-rendering rule: drawing the Hera
// view (rail + live coord pane + agent pane + details) never calls screen.Sync.
func TestPanes_NoSyncOnDraw(t *testing.T) {
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

	base := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, base.Init())
	defer base.Fini()
	base.SetSize(120, 30)
	sc := &syncCountingScreen{SimulationScreen: base}
	p.SetRect(0, 0, 120, 30)

	// Worker selection (agent terminal mode) + coordinator selection (details
	// mode) — both draw paths.
	testutil.Equal(t, selectRoleByName(p, "wkr"), true)
	p.Draw(sc)
	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	p.Draw(sc)
	testutil.Equal(t, sc.syncCount, 0)
}

// syncCountingScreen wraps a SimulationScreen and counts Sync() calls.
type syncCountingScreen struct {
	tcell.SimulationScreen
	syncCount int
}

func (s *syncCountingScreen) Sync() {
	s.syncCount++
	s.SimulationScreen.Sync()
}
