package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// TestRail_NextNeedsInputTaskID_EmptyWhenNoneNeed confirms a rail with no
// needs-input roles at all reports no candidate (Ctrl+G's safe no-op case).
func TestRail_NextNeedsInputTaskID_EmptyWhenNoneNeed(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.Refresh()

	id, ok := p.Rail().NextNeedsInputTaskID()
	testutil.Equal(t, ok, false)
	testutil.Equal(t, id, "")
}

// TestRail_NextNeedsInputTaskID_FindsWorkerCandidate confirms a plain worker's
// own needs-input signal is a reachable candidate regardless of where the
// cursor currently sits.
func TestRail_NextNeedsInputTaskID_FindsWorkerCandidate(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.SetNeedsInput([]string{"tw"})
	p.Refresh()

	r := p.Rail()
	for start := 0; start < len(r.rows); start++ {
		r.cursor = start
		id, ok := r.NextNeedsInputTaskID()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, id, "tw")
	}
}

// TestRail_NextNeedsInputTaskID_TopLevelCoordinatorOwnNeedIsNotACandidate
// documents a deliberate scoping decision: a TOP-LEVEL orchestrator's
// coordinator role is folded entirely into the rrOrch HEADER row
// (appendOrchWorkers skips db.HeraKindCoordinator, "folded into the header")
// — it never gets its own row.role-bearing row, so SelectByTaskID (which only
// ever matches row.role) could never land a jump on it. If the coordinator's
// OWN signal needs input (not merely a descendant's rollup) it must NOT be
// offered as a candidate — that would be a "found but unreachable" dead cycle
// stop. With no worker present, the coordinator's own need is therefore
// correctly invisible to the cycle.
func TestRail_NextNeedsInputTaskID_TopLevelCoordinatorOwnNeedIsNotACandidate(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	p := NewHeraPage(d)
	p.SetNeedsInput([]string{"tc"})
	p.Refresh()

	m := p.Rail().Model()
	testutil.Equal(t, m.OrchByID(orch).CoordRole().NeedsInput, true) // the signal IS set...

	id, ok := p.Rail().NextNeedsInputTaskID()
	testutil.Equal(t, ok, false) // ...but is not a reachable jump target
	testutil.Equal(t, id, "")
}

// TestRail_NextNeedsInputTaskID_HeaderRollupIsNotADistinctCandidate confirms
// a coordinator header showing "(?)" purely from a descendant's rollup
// (SubtreeNeedsInput) is never separately counted: only the actual blocked
// worker is a candidate, so it keeps re-selecting itself every cycle rather
// than the cycle also stopping on its coordinator's header.
func TestRail_NextNeedsInputTaskID_HeaderRollupIsNotADistinctCandidate(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.SetNeedsInput([]string{"tw"}) // only the worker needs input
	p.Refresh()

	m := p.Rail().Model()
	testutil.Equal(t, m.OrchByID(orch).CoordRole().SubtreeNeedsInput, true) // header rolls up
	testutil.Equal(t, m.OrchByID(orch).CoordRole().NeedsInput, false)       // but not its OWN signal

	r := p.Rail()
	for start := 0; start < len(r.rows); start++ {
		r.cursor = start
		id, ok := r.NextNeedsInputTaskID()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, id, "tw")
	}
}

// TestRail_NextNeedsInputTaskID_CyclesForwardThroughMultipleWithoutRepeating
// is the core cycling contract: repeated calls advance through EACH
// needs-input worker in turn (never immediately re-returning the one the
// cursor already sits on), wrapping back to the first once every candidate
// has been visited.
func TestRail_NextNeedsInputTaskID_CyclesForwardThroughMultipleWithoutRepeating(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "wkr-a", db.HeraKindWorker, "ta")
	seedBoundRole(t, d, orch, "wkr-b", db.HeraKindWorker, "tb")
	seedBoundRole(t, d, orch, "free", db.HeraKindFreelance, "tf")
	p := NewHeraPage(d)
	p.SetNeedsInput([]string{"ta", "tb", "tf"})
	p.Refresh()

	r := p.Rail()
	r.cursor = 0 // parked on the coordinator header (not itself a candidate)

	seen := map[string]bool{}
	var order []string
	for i := 0; i < 3; i++ {
		id, ok := r.NextNeedsInputTaskID()
		testutil.Equal(t, ok, true)
		if seen[id] {
			t.Fatalf("round %d re-selected %q before visiting every candidate: order so far %v", i, id, order)
		}
		seen[id] = true
		order = append(order, id)
		testutil.Equal(t, r.SelectByTaskID(id), true) // simulate landing there, advancing the cursor
	}
	testutil.Equal(t, len(seen), 3)
	testutil.Equal(t, seen["ta"], true)
	testutil.Equal(t, seen["tb"], true)
	testutil.Equal(t, seen["tf"], true)

	// The 4th call wraps back to the first candidate visited.
	id, ok := r.NextNeedsInputTaskID()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, id, order[0])
}

// TestRail_NextNeedsInputTaskID_RevealedBehindClosedFold confirms a blocked
// worker nested under a COLLAPSED coordinator is still found: the rail's
// partial-fold reveal (add-hera-jump-question's sibling feature) already
// surfaces that specific row even while the fold stays visually closed, and
// NextNeedsInputTaskID scans the built rows exactly as they render.
func TestRail_NextNeedsInputTaskID_RevealedBehindClosedFold(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.SetNeedsInput([]string{"tw"})
	p.Refresh()

	r := p.Rail()
	r.seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.ID == orch })
	r.ToggleCollapse()
	testutil.Equal(t, r.OrchCollapsed(orch), true)

	id, ok := r.NextNeedsInputTaskID()
	testutil.Equal(t, ok, true)
	testutil.Equal(t, id, "tw")
}

// TestHeraPage_JumpToNextNeedsInput_ExpandsAncestorAndSelects is the
// end-to-end contract (add-hera-jump-question's Ctrl+G): reusing JumpToTask,
// it expands a collapsed ancestor, selects the role, and returns true —
// mirroring TestJumpToTask_ExpandsCollapsedAncestorAndReturnsTrue but driven
// from the needs-input cycle rather than a known task id.
func TestHeraPage_JumpToNextNeedsInput_ExpandsAncestorAndSelects(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	seedBoundRole(t, d, orch, "wkr", db.HeraKindWorker, "tw")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"tc": {id: "tc", alive: true},
		"tw": {id: "tw", alive: true},
	}))
	p.SetNeedsInput([]string{"tw"})
	p.Refresh()

	r := p.Rail()
	r.seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.ID == orch })
	r.ToggleCollapse()
	testutil.Equal(t, r.OrchCollapsed(orch), true)

	testutil.Equal(t, p.JumpToNextNeedsInput(), true)
	testutil.Equal(t, r.OrchCollapsed(orch), false) // ancestor re-expanded
	testutil.Equal(t, p.SelectionContext().TaskID(), "tw")
	testutil.Equal(t, p.Machine().State(), FocusAgent)
}

// TestHeraPage_JumpToNextNeedsInput_NoneNeedsInput is the safe no-op case —
// confirms it doesn't crash or mutate selection when there is nothing (or
// only an unreachable top-level-coordinator-own-need) to jump to.
func TestHeraPage_JumpToNextNeedsInput_NoneNeedsInput(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	p := NewHeraPage(d)
	p.Refresh()

	testutil.Equal(t, p.JumpToNextNeedsInput(), false)
}

// TestHeraPage_JumpToNextNeedsInput_RemoteIsNoop mirrors JumpToTask's own
// remote-mode guard.
func TestHeraPage_JumpToNextNeedsInput_RemoteIsNoop(t *testing.T) {
	remote := NewHeraPage(nil) // remote: no hera reader
	testutil.Equal(t, remote.JumpToNextNeedsInput(), false)
}
