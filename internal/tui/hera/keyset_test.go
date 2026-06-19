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

func TestKeyset_EOLKeysFire(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	var got string
	p.OnClearArchive = func(Selection) { got = "clear-archive" }
	p.OnNewCoordinator = func(Selection) { got = "new-coord" }

	h := p.InputHandler()
	cases := []struct {
		ev   *tcell.EventKey
		want string
	}{
		{tcell.NewEventKey(tcell.KeyRune, 'C', tcell.ModNone), "clear-archive"},
		{tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone), "new-coord"},
	}
	for _, c := range cases {
		got = ""
		h(c.ev, noFocus)
		testutil.Equal(t, got, c.want)
	}
}

// TestKeyset_RetireAndRailPruneUnbound pins BUG-022: `R` and the rail-wide
// `Ctrl+R` are no longer EOL keys — pressing them fires nothing end-of-life and
// (for `R`) does not leak into the other selection callbacks.
func TestKeyset_RetireAndRailPruneUnbound(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	fired := false
	// Wire every selection callback so a stray dispatch would be caught.
	p.OnClearArchive = func(Selection) { fired = true }
	p.OnArchiveToggle = func(Selection) { fired = true }
	p.OnDelete = func(Selection) { fired = true }
	p.OnNewCoordinator = func(Selection) { fired = true }

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'R', tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyCtrlR, 0, tcell.ModNone), noFocus)
	testutil.Equal(t, fired, false)
}

// TestKeyset_NewCoordFiresOnEmptySelection verifies the selection-INDEPENDENT
// `n` (bootstrap key) fires even when nothing is selected, while the
// selection-gated `C` does not.
func TestKeyset_NewCoordFiresOnEmptySelection(t *testing.T) {
	d := memDB(t)
	p := NewHeraPage(d) // empty rail, no orchestrators
	p.Refresh()
	var fired []string
	p.OnNewCoordinator = func(Selection) { fired = append(fired, "new-coord") }
	// Selection-gated keys should NOT fire on the empty rail.
	p.OnClearArchive = func(Selection) { fired = append(fired, "clear-archive") }

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyRune, 'C', tcell.ModNone), noFocus)

	testutil.Equal(t, len(fired), 1)
	testutil.Equal(t, fired[0], "new-coord")
}

// TestKeyset_EOLKeysSuppressedWhileFiltering verifies that while the rail is in
// `/` filter INPUT mode, the EOL keys are treated as filter input, not commands.
func TestKeyset_EOLKeysSuppressedWhileFiltering(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	fired := false
	p.OnNewCoordinator = func(Selection) { fired = true }
	p.OnClearArchive = func(Selection) { fired = true }

	h := p.InputHandler()
	h(tcell.NewEventKey(tcell.KeyRune, '/', tcell.ModNone), noFocus) // enter filter input
	testutil.Equal(t, p.RailFiltering(), true)
	h(tcell.NewEventKey(tcell.KeyRune, 'n', tcell.ModNone), noFocus)
	h(tcell.NewEventKey(tcell.KeyRune, 'C', tcell.ModNone), noFocus)
	testutil.Equal(t, fired, false)
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
	// Enter on a worker row focuses the AGENT (right) pane, not the coord pane.
	testutil.Equal(t, p.Machine().State(), FocusAgent)
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
	// Enter on a live worker row focuses the AGENT (right) pane.
	testutil.Equal(t, p.Machine().State(), FocusAgent)
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
	// Enter on a coordinator row focuses the COORD (middle) pane.
	testutil.Equal(t, p.Machine().State(), FocusCoord)
}

// TestKeyset_EnterCoordinatorFocusesCoordPane verifies that Enter on a live
// coordinator header (navigate-only) advances focus to FocusCoord, not FocusAgent.
func TestKeyset_EnterCoordinatorFocusesCoordPane(t *testing.T) {
	p, _ := railPageWithCursorOnWorker(t)
	h := p.InputHandler()
	// Move cursor back to the orchestrator header (coordinator row 0).
	h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
	// Wire a live session for the coordinator so it is navigate-only.
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"tc": {id: "tc", alive: true}}))

	h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)

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
