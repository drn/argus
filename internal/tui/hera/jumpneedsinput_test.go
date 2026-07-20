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

// TestRail_NextNeedsInputTaskID_TopLevelCoordinatorOwnNeedIsReachable
// (fix-ctrlg-coordinator-own-need) reverses a prior deliberate exclusion: a
// TOP-LEVEL orchestrator's coordinator role is folded entirely into the
// rrOrch HEADER row (appendOrchWorkers skips db.HeraKindCoordinator, "folded
// into the header") — it never gets its own role-bearing row, so it can only
// ever be reached through the header itself. needsInputTaskID now qualifies a
// header row via its coordinator's OWN signal (CoordRole().needsInputOwn()),
// and SelectByTaskID gained a matching header-row scan, so with no worker
// present, the coordinator's own need IS now a reachable candidate.
func TestRail_NextNeedsInputTaskID_TopLevelCoordinatorOwnNeedIsReachable(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	p := NewHeraPage(d)
	p.SetNeedsInput([]string{"tc"})
	p.Refresh()

	m := p.Rail().Model()
	testutil.Equal(t, m.OrchByID(orch).CoordRole().NeedsInput, true) // the signal is set...

	id, ok := p.Rail().NextNeedsInputTaskID()
	testutil.Equal(t, ok, true) // ...and is now a reachable jump target
	testutil.Equal(t, id, "tc")
}

// TestRail_SelectByTaskID_ResolvesTopLevelCoordinatorHeader (fix-ctrlg-
// coordinator-own-need) confirms SelectByTaskID's new header-row match pass
// can land directly on a top-level coordinator's own task id when no role row
// matches it — the coordinator is folded entirely into the rrOrch header, so
// the ONLY possible match is the header's CoordRole().TaskID.
func TestRail_SelectByTaskID_ResolvesTopLevelCoordinatorHeader(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	p := NewHeraPage(d)
	p.Refresh()

	r := p.Rail()
	testutil.Equal(t, r.SelectByTaskID("tc"), true)
	sel := r.Selection()
	testutil.Equal(t, sel.Role == nil, true)
	testutil.Equal(t, sel.Orch != nil && sel.Orch.ID == orch, true)
	testutil.Equal(t, sel.IsCoordinator(), true)
}

// TestHeraPage_JumpToNextNeedsInput_TopLevelCoordinatorOwnNeed (fix-ctrlg-
// coordinator-own-need) is the end-to-end contract: a top-level orchestrator
// with ONLY a coordinator (no worker at all) whose own signal needs input is
// now a reachable ctrl+g target, landing selection in the coordinator pane.
func TestHeraPage_JumpToNextNeedsInput_TopLevelCoordinatorOwnNeed(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "tc")
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"tc": {id: "tc", alive: true},
	}))
	p.SetNeedsInput([]string{"tc"})
	p.Refresh()

	testutil.Equal(t, p.JumpToNextNeedsInput(), true)
	sel := p.SelectionContext()
	testutil.Equal(t, sel.Role == nil, true)
	testutil.Equal(t, sel.Orch != nil && sel.Orch.ID == orch, true)
	testutil.Equal(t, sel.IsCoordinator(), true)
	testutil.Equal(t, sel.FocusTaskID(), "tc")
	testutil.Equal(t, p.Machine().State(), FocusCoord)
}

// TestHeraPage_JumpToNextNeedsInput_CoordSpawnedNestedSubteamOwnNeed
// (fix-ctrlg-coordinator-own-need) covers the coordinator-spawned nested
// sub-team shape: one coordinator agent/task simultaneously drives BOTH a
// parent orchestrator P and a nested child orchestrator Q (the
// hera_new_orchestrator self-promotion shape — mirrors the seeding pattern in
// TestModel_CoordSpawnedSubteamBridge, model_test.go). Only Q's own
// coordinator role is marked as needing input. With P collapsed (hiding Q's
// header), ctrl+g must still find it (via the rail's partial-fold reveal),
// expand P, and land on a coordinator selection for the shared task.
//
// Because P and Q's coordinator roles share the IDENTICAL underlying task
// (one physical agent, two role bindings), SelectByTaskID's plain task-id
// match — by design, see design.md Decision 3 — always resolves to whichever
// header is FIRST in rail row order. A header row is placed unconditionally
// before any of its nested children's rows, so that is structurally always
// the PARENT's header (P), never the child's, regardless of which one's own
// signal triggered the candidate scan. This is not a regression: it is the
// same first-match convention TestRail_SelectByTaskID_SharedTaskMultiHeader...
// below exercises directly.
func TestHeraPage_JumpToNextNeedsInput_CoordSpawnedNestedSubteamOwnNeed(t *testing.T) {
	p := coordOf(1, "P", 100, "tc",
		RoleView{RoleID: 101, Name: "pw", Kind: db.HeraKindWorker, Live: true, TaskID: "tpw", BridgeTaskID: "tpw"})
	q := coordOf(2, "Q", 200, "tc",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tqw"})
	m := Model{Active: []OrchView{p, q}}
	roleByName(t, &m, 2, "coord").NeedsInput = true // only Q's own coordinator need
	m.rollupNeedsInput()                            // propagates SubtreeNeedsInput for the fold-reveal

	page := NewHeraPage(memDB(t))
	page.Rail().SetModel(m)
	page.applySelection() // mirrors doRefresh's post-SetModel re-sync

	page.Rail().seekCursor(t, func(row railRow) bool { return row.orch != nil && row.orch.Name == "P" })
	page.Rail().ToggleCollapse()
	testutil.Equal(t, page.Rail().OrchCollapsed(1), true)
	page.Rail().cursor = 0 // cursor starts away from the target, like real usage

	testutil.Equal(t, page.JumpToNextNeedsInput(), true)
	testutil.Equal(t, page.Rail().OrchCollapsed(1), false) // parent ancestor re-expanded
	sel := page.SelectionContext()
	testutil.Equal(t, sel.Role == nil, true)
	testutil.Equal(t, sel.IsCoordinator(), true)
	testutil.Equal(t, sel.FocusTaskID(), "tc")
	testutil.Equal(t, sel.Orch.Name, "P") // first-match: the parent header, see comment above
	testutil.Equal(t, page.Machine().State(), FocusCoord)
}

// TestRail_SelectByTaskID_SharedTaskMultiHeaderConsistentlyResolvesFirstMatch
// (fix-ctrlg-coordinator-own-need, design.md Decision 3) is the shared-task
// multi-header edge case: P and Q's coordinator roles share the SAME task, and
// BOTH their own needs-input signals are set (the realistic outcome when a
// live coordinator agent's task is flagged, since every role bound to that
// task lights up independently). ctrl+g must consistently resolve to the SAME
// first-matching header (P, the parent) regardless of where the cursor
// starts — never alternate between P and Q, never crash, never spin.
func TestRail_SelectByTaskID_SharedTaskMultiHeaderConsistentlyResolvesFirstMatch(t *testing.T) {
	p := coordOf(1, "P", 100, "tc",
		RoleView{RoleID: 101, Name: "pw", Kind: db.HeraKindWorker, Live: true, TaskID: "tpw", BridgeTaskID: "tpw"})
	q := coordOf(2, "Q", 200, "tc",
		RoleView{RoleID: 201, Name: "qw", Kind: db.HeraKindWorker, Live: true, TaskID: "tqw", BridgeTaskID: "tqw"})
	m := Model{Active: []OrchView{p, q}}
	roleByName(t, &m, 1, "coord").NeedsInput = true
	roleByName(t, &m, 2, "coord").NeedsInput = true

	r := NewRail()
	r.SetModel(m)
	testutil.Equal(t, r.depthOf("P"), 0)
	testutil.Equal(t, r.depthOf("Q"), 1) // nested under P, confirming the coord-bridge shape

	for start := 0; start < r.Rows(); start++ {
		r.cursor = start
		id, ok := r.NextNeedsInputTaskID()
		testutil.Equal(t, ok, true)
		testutil.Equal(t, id, "tc")
		testutil.Equal(t, r.SelectByTaskID(id), true)
		sel := r.Selection()
		testutil.Equal(t, sel.Role == nil, true)
		if sel.Orch != nil {
			testutil.Equal(t, sel.Orch.Name, "P") // always the first match, never Q
		} else {
			t.Errorf("start=%d: Selection().Orch is nil", start)
		}
	}
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
// confirms it doesn't crash or mutate selection when nothing needs input.
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
