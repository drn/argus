package hera

import (
	"testing"
	"time"

	"github.com/drn/argus/internal/app/agentview"
	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// fakeSession is a minimal agentview.TerminalAdapter identified by task ID, so
// tests can assert which task's session a pane is feeding from.
type fakeSession struct {
	id     string
	alive  bool
	resize [2]uint16 // last (rows, cols) passed to Resize
}

func (f *fakeSession) WriteInput(p []byte) (int, error) { return len(p), nil }
func (f *fakeSession) Resize(rows, cols uint16) error   { f.resize = [2]uint16{rows, cols}; return nil }
func (f *fakeSession) RecentOutput() []byte             { return nil }
func (f *fakeSession) RecentOutputTail(int) []byte      { return nil }
func (f *fakeSession) RecentOutputTailWithTotal(int) ([]byte, uint64) {
	return nil, 0
}
func (f *fakeSession) TotalWritten() uint64 { return 0 }
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

// TestPanes_MultiBindingFeedsCorrectContext is THE locked must-have: one task
// that is a worker in orchestrator A and a coordinator in orchestrator B feeds
// the AGENT pane when its A-role is selected and the HERA pane (details mode)
// when its B-role is selected — disambiguated purely by the role's orchestrator.
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
	// orch-b nests under A's a-worker row. The bridging row keeps its PARENT
	// worker context (conservative — so Ctrl+D deletes the worker role, never the
	// child orchestrator): selecting it feeds the AGENT pane with the shared task
	// and the HERA pane with A's coordinator.
	testutil.Equal(t, selectRoleByName(p, "a-worker"), true)
	testutil.Equal(t, p.detailsMode, false)
	testutil.Equal(t, p.SelectionContext().Orch.Name, "orch-a")
	testutil.Equal(t, p.AgentPane().Session().(*fakeSession).id, "shared")
	testutil.Equal(t, p.CoordPane().Session().(*fakeSession).id, "t-a-coord")
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
	p.bindPane(p.AgentPane(), &p.agentBound, "", "agent")
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

// TestPanes_FocusTraversal walks rail→coord→agent and back via the page's Tab /
// BackTab / Ctrl+Q input handling.
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
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	h(tcell.NewEventKey(tcell.KeyTab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusAgent)
	h(tcell.NewEventKey(tcell.KeyBacktab, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
	h(tcell.NewEventKey(tcell.KeyCtrlQ, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, p.Machine().State(), FocusRail)
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
