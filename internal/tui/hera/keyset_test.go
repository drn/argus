package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// railPageWithCursorOnRole builds a page whose rail cursor rests on the first
// WORKER role of orchestrator "o" (cursor 0 = orch header, 1 = coord, 2 = worker).
func railPageWithCursorOnWorker(t *testing.T) (*HeraPage, *db.DB) {
	t.Helper()
	d := memDB(t)
	orch := seedOrch(t, d, "o")
	seedBoundRole(t, d, orch, "c", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "w", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.Refresh()
	// Move cursor to the worker role (row 2).
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus) // → coord
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
	testutil.Equal(t, p.Rail().CursorIndex(), 2)
}

func TestKeyset_NavStillWorksAlongsideMutations(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	p.OnArchiveToggle = func(Selection) {}
	h := p.InputHandler()
	// j/k still navigate (not swallowed by the mutation handler).
	h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 1)
	h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
	testutil.Equal(t, p.Rail().CursorIndex(), 2)
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

func TestKeyset_EnterLiveSessionDoesNotReattach(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	// Resolver returns a live session for the worker → Enter must NOT reattach.
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tw": {id: "tw", alive: true}}))
	called := false
	p.OnReattach = func(Selection) { called = true }

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)

	testutil.Equal(t, called, false)
	testutil.Equal(t, p.Machine().State(), FocusCoord)
}

func TestKeyset_RemoteModeMutationKeysInert(t *testing.T) {
	p := NewHeraPage(nil) // remote mode
	fired := false
	p.OnArchiveToggle = func(Selection) { fired = true }
	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'a', tcell.ModNone), noFocus)
	testutil.Equal(t, fired, false)
}
