package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// railPageWithCursorOnWorker builds a page whose rail cursor rests on the first
// WORKER role of orchestrator "o". After the coordinator fold the rows are
// cursor 0 = orch header (the coordinator) and cursor 1 = the worker, so one
// Down lands on the worker.
func railPageWithCursorOnWorker(t *testing.T) (*HeraPage, *db.DB) {
	t.Helper()
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.Refresh()
	// Move cursor to the worker role (row 1; coord is folded into the header).
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus) // → worker
	return p, d
}

func TestKeyset_FiresCallbacksOnSelectedRole(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	var got string
	var gotSel Selection
	record := func(name string) func(Selection) {
		return func(s Selection) { got = name; gotSel = s }
	}
	p.OnSpawnWorker = record("spawn")
	p.OnRename = record("rename")
	p.OnArchiveToggle = record("archive")
	p.OnPinToggle = record("pin")
	p.OnStatusAdvance = record("adv")
	p.OnStatusRevert = record("rev")
	p.OnDelete = record("delete")
	p.OnAdopt = record("adopt")

	h := p.InputHandler()
	cases := []struct {
		ev   *tcell.EventKey
		want string
	}{
		{tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone), "spawn"},
		{tcell.NewEventKey(tcell.KeyRune, 'r', tcell.ModNone), "rename"},
		{tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), "archive"},
		{tcell.NewEventKey(tcell.KeyRune, 'P', tcell.ModNone), "pin"},
		{tcell.NewEventKey(tcell.KeyRune, 's', tcell.ModNone), "adv"},
		{tcell.NewEventKey(tcell.KeyRune, 'S', tcell.ModNone), "rev"},
		{tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone), "delete"},
		{tcell.NewEventKey(tcell.KeyRune, 'J', tcell.ModNone), "adopt"},
	}
	for _, c := range cases {
		got = ""
		h(c.ev, noFocus)
		testutil.Equal(t, got, c.want)
		// Every fire targets the SELECTED worker role under orchestrator "o".
		testutil.Equal(t, gotSel.Role != nil, true)
		testutil.Equal(t, gotSel.Role.Kind, db.HeraKindWorker)
	}
}

func TestKeyset_NilCallbacksAreNoOps(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	h := p.InputHandler()
	// No callbacks wired → keys must not panic and must fall through cleanly.
	h(tcell.NewEventKey(tcell.KeyRune, 'w', tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyCtrlD, 0, tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), noFocus)
	// Cursor unaffected by unconsumed mutation keys with no callback.
	testutil.Equal(t, p.Rail().CursorIndex(), 1)
}

func TestKeyset_NavStillWorksAlongsideMutations(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	p.OnArchiveToggle = func(Selection) {}
	h := p.InputHandler()
	// j/k still navigate (not swallowed by the mutation handler). Cursor starts
	// on the worker (row 1); k → header (0); j → worker (1).
	h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 0)
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 1)
}

func TestKeyset_EnterReattachOnDeadSessionThenFocus(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	// No resolver wired → p.resolve == nil → every task is treated as dead.
	var reattached Selection
	called := false
	p.OnReattach = func(s Selection) { reattached = s; called = true }

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)

	testutil.Equal(t, called, true)
	testutil.Equal(t, reattached.TaskID(), "tw")
	// Focus advanced into the coord pane (Enter "enters").
	testutil.Equal(t, p.Machine().State(), FocusCoord)
}

func TestKeyset_EnterLiveWorkerFiresReattach(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	// Resolver returns a live session for the worker. Enter STILL fires reattach
	// for a live worker — a SIGTSTP'd worker is "alive" but suspended, so the
	// App-side handler is what decides whether to actually revive it.
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tw": {id: "tw", alive: true}}))
	var got Selection
	called := false
	p.OnReattach = func(s Selection) { got = s; called = true }

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)

	testutil.Equal(t, called, true)
	testutil.Equal(t, got.TaskID(), "tw")
	testutil.Equal(t, p.Machine().State(), FocusCoord)
}

func TestKeyset_EnterLiveCoordinatorDoesNotReattach(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	// Move the cursor up to the coordinator role (row 1).
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
	// Coordinator has a live session → Enter must NOT reattach (navigate-only).
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tc": {id: "tc", alive: true}}))
	called := false
	p.OnReattach = func(Selection) { called = true }

	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)

	testutil.Equal(t, called, false)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
}

func TestKeyset_EnterDeadCoordinatorReattaches(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus) // → coord header
	// No resolver wired → coordinator treated as dead → Enter reattaches it.
	var got Selection
	called := false
	p.OnReattach = func(s Selection) { got = s; called = true }

	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)

	testutil.Equal(t, called, true)
	// The coordinator is folded into the orchestrator header (rail-nesting), so the
	// reattached selection carries no Role row; its task resolves via FocusTaskID.
	testutil.Equal(t, got.FocusTaskID(), "tc")
}

func TestKeyset_RemoteModeMutationKeysInert(t *testing.T) {
	p := NewHeraPage(nil) // remote mode
	fired := false
	p.OnArchiveToggle = func(Selection) { fired = true }
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), noFocus)
	testutil.Equal(t, fired, false)
}
