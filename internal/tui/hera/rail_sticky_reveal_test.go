package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	heramodel "github.com/drn/argus/internal/hera/model"
	"github.com/drn/argus/internal/testutil"
)

// railSelectRoleByName walks the cursor down until it lands on a role row
// with the given name, failing the test if no such row exists in the current
// build. Mirrors collapseByName's walk-and-act shape (rail_reveal_test.go).
func railSelectRoleByName(t *testing.T, r *Rail, name string) {
	t.Helper()
	r.cursor = 0
	for i := 0; i < len(r.rows); i++ {
		if r.rows[r.cursor].role != nil && r.rows[r.cursor].role.Name == name {
			return
		}
		r.CursorDown()
	}
	t.Fatalf("role row %q not found", name)
}

// TestRail_SelectedNeedsInputRoleStaysRevealedAfterItsFlagClears is BUG-071:
// w1 is revealed through P's closed fold only because it needs input, the
// operator selects it (the panes now show its session), and the NEXT
// rebuild's model shows nothing needing input anywhere (the operator
// answered the prompt). Before the fix, w1's row vanished mid-rebuild (its
// SubtreeNeedsInput rollup went false with it) and the cursor — restoreCursor
// found no matching role row — landed on whatever row the stale index now
// pointed at, yanking the operator off the exact row they were interacting
// with.
func TestRail_SelectedNeedsInputRoleStaysRevealedAfterItsFlagClears(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		heramodel.RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1", NeedsInput: true, SubtreeNeedsInput: true},
		heramodel.RoleView{RoleID: 102, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tw2"},
	)
	p.SubtreeNeedsInput = true
	r := NewRail()
	r.SetModel(heramodel.Model{Active: []heramodel.OrchView{p}})

	collapseByName(t, r, "P")
	railSelectRoleByName(t, r, "w1")
	testutil.Equal(t, r.Selected().Name, "w1")

	// Fresh model (as a real heramodel.BuildModel rebuild would hand over): w1's own
	// needs-input signal has cleared, and nothing else in the model needs
	// input either.
	p2 := coordOf(1, "P", 100, "tp",
		heramodel.RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1"},
		heramodel.RoleView{RoleID: 102, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tw2"},
	)
	r.SetModel(heramodel.Model{Active: []heramodel.OrchView{p2}})

	testutil.Equal(t, r.depthOf("w1") >= 0, true)
	sel := r.Selected()
	testutil.Equal(t, sel != nil, true)
	if sel != nil {
		testutil.Equal(t, sel.Name, "w1")
	}
	// w2 was never selected and never needed input — it must stay hidden; the
	// sticky reveal is scoped to the one selected path, not the whole subtree.
	testutil.Equal(t, r.depthOf("w2"), -1)
}

// TestRail_StickyRevealForcesIntermediateBridgingRowToo extends the above to
// a two-level nested reveal chain (mirrors
// TestRail_RevealNestedClosedCoordinatorsFullChain): the sticky leaf C sits
// under sub-coordinator B, itself nested (and closed) under P. Every
// needs-input signal along the chain clears on the rebuild; the whole
// revealed path — P's header, B's bridging row, and C's own row — must
// survive since C was the selected row.
func TestRail_StickyRevealForcesIntermediateBridgingRowToo(t *testing.T) {
	buildModel := func(needsInput bool) heramodel.Model {
		p := coordOf(1, "P", 100, "tp",
			heramodel.RoleView{RoleID: 101, Name: "B", Kind: db.HeraKindWorker, Live: true, TaskID: "tb", BridgeTaskID: "tb", SubtreeNeedsInput: needsInput},
			heramodel.RoleView{RoleID: 102, Name: "sib", Kind: db.HeraKindWorker, Live: true, TaskID: "tsib"},
		)
		p.SubtreeNeedsInput = needsInput
		b := coordOf(2, "B", 200, "tb",
			heramodel.RoleView{RoleID: 201, Name: "C", Kind: db.HeraKindWorker, Live: true, TaskID: "tc", NeedsInput: needsInput, SubtreeNeedsInput: needsInput},
			heramodel.RoleView{RoleID: 202, Name: "bsib", Kind: db.HeraKindWorker, Live: true, TaskID: "tbsib"},
		)
		b.SubtreeNeedsInput = needsInput
		return heramodel.Model{Active: []heramodel.OrchView{p, b}}
	}

	r := NewRail()
	r.SetModel(buildModel(true))

	collapseByName(t, r, "P")
	for r.rows[r.cursor].collOrchID != 2 {
		r.CursorDown()
	}
	r.ToggleCollapse() // collapse B (the nested sub-coordinator) too

	railSelectRoleByName(t, r, "C")
	testutil.Equal(t, r.Selected().Name, "C")

	// Rebuild with every needs-input signal cleared.
	r.SetModel(buildModel(false))

	testutil.Equal(t, r.depthOf("B") >= 0, true)
	testutil.Equal(t, r.depthOf("C") >= 0, true)
	sel := r.Selected()
	testutil.Equal(t, sel != nil, true)
	if sel != nil {
		testutil.Equal(t, sel.Name, "C")
	}
	// Unrelated siblings at every level stay hidden.
	testutil.Equal(t, r.depthOf("sib"), -1)
	testutil.Equal(t, r.depthOf("bsib"), -1)
}

// TestRail_RevealedRoleRefoldsAfterSelectionMovesAway is the non-regression
// complement: once the operator has moved the cursor off the revealed row
// (here, back onto P's own header) BEFORE the needs-input flag clears, the
// next rebuild is free to fold the now-unselected row away — stickiness
// tracks the CURRENT selection, not "anything ever revealed this session."
func TestRail_RevealedRoleRefoldsAfterSelectionMovesAway(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		heramodel.RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1", NeedsInput: true, SubtreeNeedsInput: true},
		heramodel.RoleView{RoleID: 102, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tw2"},
	)
	p.SubtreeNeedsInput = true
	r := NewRail()
	r.SetModel(heramodel.Model{Active: []heramodel.OrchView{p}})

	collapseByName(t, r, "P")
	railSelectRoleByName(t, r, "w1")

	// The operator navigates back up to P's header before the next rebuild.
	r.CursorUp()
	orch := r.SelectedOrch()
	testutil.Equal(t, orch != nil, true)
	if orch != nil {
		testutil.Equal(t, orch.Name, "P")
	}

	p2 := coordOf(1, "P", 100, "tp",
		heramodel.RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1"},
		heramodel.RoleView{RoleID: 102, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tw2"},
	)
	r.SetModel(heramodel.Model{Active: []heramodel.OrchView{p2}})

	testutil.Equal(t, r.depthOf("w1"), -1)
	orch = r.SelectedOrch()
	testutil.Equal(t, orch != nil, true)
	if orch != nil {
		testutil.Equal(t, orch.Name, "P")
	}
}

// TestRail_OrchestratorHeaderSelectionIsStickyToo covers the header-row case:
// a coordinator-spawned nested sub-team ("Q", sharing coordinator task "T"
// with root "P") renders its own separate rrOrch header row, revealed only
// through P's closed fold because Q's subtree needs input. That header row
// is the previously-selected row and must survive the same way when the
// subtree stops needing input.
func TestRail_OrchestratorHeaderSelectionIsStickyToo(t *testing.T) {
	buildModel := func(needsInput bool) heramodel.Model {
		p := coordOf(1, "P", 100, "T")
		q := coordOf(2, "Q", 200, "T",
			heramodel.RoleView{RoleID: 201, Name: "leaf", Kind: db.HeraKindWorker, Live: true, TaskID: "tleaf", NeedsInput: needsInput, SubtreeNeedsInput: needsInput},
		)
		q.SubtreeNeedsInput = needsInput
		p.SubtreeNeedsInput = needsInput
		return heramodel.Model{Active: []heramodel.OrchView{p, q}}
	}

	r := NewRail()
	r.SetModel(buildModel(true))

	collapseByName(t, r, "P")
	// Q nests directly under P as its own header row (coordinator-spawned
	// sub-team: P and Q share the same coordinator task "T"). Select Q's
	// header directly.
	for r.rows[r.cursor].orch == nil || r.rows[r.cursor].orch.Name != "Q" {
		r.CursorDown()
	}
	testutil.Equal(t, r.SelectedOrch().Name, "Q")

	r.SetModel(buildModel(false))

	testutil.Equal(t, r.hasOrchHeader("Q"), true)
	orch := r.SelectedOrch()
	testutil.Equal(t, orch != nil, true)
	if orch != nil {
		testutil.Equal(t, orch.Name, "Q")
	}
}

// TestRail_StickyRevealNoOpOnStaleRoleRef covers a selected role that no
// longer exists at all in the next rebuild's model (e.g. its binding was
// deleted/nuked out from under the operator between rebuilds, not merely
// cleared its needs-input flag) — applyStickyReveal must not panic and must
// leave the rebuild to proceed exactly as it would without the fix.
func TestRail_StickyRevealNoOpOnStaleRoleRef(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		heramodel.RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1", NeedsInput: true, SubtreeNeedsInput: true},
	)
	p.SubtreeNeedsInput = true
	r := NewRail()
	r.SetModel(heramodel.Model{Active: []heramodel.OrchView{p}})

	collapseByName(t, r, "P")
	railSelectRoleByName(t, r, "w1")

	// w1's role is gone entirely from the next model (not merely cleared).
	p2 := coordOf(1, "P", 100, "tp")
	r.SetModel(heramodel.Model{Active: []heramodel.OrchView{p2}})

	testutil.Equal(t, r.depthOf("w1"), -1)
}

// TestRail_StickyRevealNoOpOnStaleOrchRef mirrors the above for a selected
// orchestrator header whose orchestrator has disappeared entirely from the
// next rebuild's model.
func TestRail_StickyRevealNoOpOnStaleOrchRef(t *testing.T) {
	p := coordOf(1, "P", 100, "T")
	q := coordOf(2, "Q", 200, "T",
		heramodel.RoleView{RoleID: 201, Name: "leaf", Kind: db.HeraKindWorker, Live: true, TaskID: "tleaf", NeedsInput: true, SubtreeNeedsInput: true},
	)
	q.SubtreeNeedsInput = true
	p.SubtreeNeedsInput = true
	r := NewRail()
	r.SetModel(heramodel.Model{Active: []heramodel.OrchView{p, q}})

	collapseByName(t, r, "P")
	for r.rows[r.cursor].orch == nil || r.rows[r.cursor].orch.Name != "Q" {
		r.CursorDown()
	}
	testutil.Equal(t, r.SelectedOrch().Name, "Q")

	// Q is gone entirely from the next model.
	r.SetModel(heramodel.Model{Active: []heramodel.OrchView{p}})

	testutil.Equal(t, r.hasOrchHeader("Q"), false)
}
