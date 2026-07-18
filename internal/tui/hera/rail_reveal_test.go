package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// collapseByName walks the cursor down to the orchestrator header named
// `name` and toggles its fold — the test-harness equivalent of pressing Space
// on that row.
func collapseByName(t *testing.T, r *Rail, name string) {
	t.Helper()
	for i := 0; i < len(r.rows); i++ {
		if r.rows[r.cursor].orch != nil && r.rows[r.cursor].orch.Name == name {
			r.ToggleCollapse()
			return
		}
		r.CursorDown()
	}
	t.Fatalf("orchestrator header %q not found", name)
}

// TestRail_RevealSingleNeedsInputLeafThroughClosedFold is the headline
// add-hera-jump-question scenario: a coordinator is collapsed and exactly one
// descendant worker needs input — the rail must still render that worker's
// row (and the coordinator's own header, already marked) while every other
// sibling stays fully hidden.
func TestRail_RevealSingleNeedsInputLeafThroughClosedFold(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1", NeedsInput: true, SubtreeNeedsInput: true},
		RoleView{RoleID: 102, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tw2"},
	)
	p.SubtreeNeedsInput = true
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p}})

	// Expanded baseline: both workers visible.
	testutil.Equal(t, r.depthOf("w1") >= 0, true)
	testutil.Equal(t, r.depthOf("w2") >= 0, true)

	collapseByName(t, r, "P")

	// w1 (needs input) is revealed through the closed fold; w2 stays hidden.
	testutil.Equal(t, r.depthOf("w1") >= 0, true)
	testutil.Equal(t, r.depthOf("w2"), -1)
	// The header itself still renders exactly once.
	testutil.Equal(t, rootHeaderCount(r), 1)
}

// TestRail_RevealNestedClosedCoordinatorsFullChain covers the two-level ASCII
// example from the proposal: an outer coordinator P (closed) bridges a
// sub-coordinator via worker row "B" (also closed — B's own worker-bridge
// row IS its coordinator, so there is no separate orchestrator header for
// it) whose own worker C needs input. The rail must render P's header, the
// "B" bridging row (peeking through P's closed fold), and C's row — with
// every sibling at every level hidden.
func TestRail_RevealNestedClosedCoordinatorsFullChain(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		RoleView{RoleID: 101, Name: "B", Kind: db.HeraKindWorker, Live: true, TaskID: "tb", BridgeTaskID: "tb", SubtreeNeedsInput: true},
		RoleView{RoleID: 102, Name: "sib", Kind: db.HeraKindWorker, Live: true, TaskID: "tsib"},
	)
	p.SubtreeNeedsInput = true
	b := coordOf(2, "B", 200, "tb",
		RoleView{RoleID: 201, Name: "C", Kind: db.HeraKindWorker, Live: true, TaskID: "tc", NeedsInput: true, SubtreeNeedsInput: true},
		RoleView{RoleID: 202, Name: "bsib", Kind: db.HeraKindWorker, Live: true, TaskID: "tbsib"},
	)
	b.SubtreeNeedsInput = true
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p, b}})

	// Collapse the OUTER coordinator P first.
	collapseByName(t, r, "P")
	// B (the bridging worker row, which doubles as B's own coordinator) still
	// renders since it needs input via its subtree, "sib" is hidden.
	testutil.Equal(t, r.depthOf("B") >= 0, true)
	testutil.Equal(t, r.depthOf("sib"), -1)
	// C (the leaf) is revealed all the way through — B's own fold state
	// (still expanded at this point) doesn't matter yet.
	testutil.Equal(t, r.depthOf("C") >= 0, true)
	testutil.Equal(t, r.depthOf("bsib"), -1) // B's sibling worker hidden too

	// Now ALSO collapse B (the nested sub-coordinator's fold, independent of
	// P's). The reveal must still punch through BOTH closed folds down to C.
	for r.rows[r.cursor].collOrchID != 2 {
		r.CursorDown()
	}
	r.ToggleCollapse()
	testutil.Equal(t, r.depthOf("C") >= 0, true)
	testutil.Equal(t, r.depthOf("bsib"), -1)
}

// TestRail_RevealMultipleNeedsInputLeaves ensures the reveal does not stop at
// the first match: every needs-input descendant under a closed coordinator
// gets its own revealed path.
func TestRail_RevealMultipleNeedsInputLeaves(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1", NeedsInput: true, SubtreeNeedsInput: true},
		RoleView{RoleID: 102, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tw2", NeedsInput: true, SubtreeNeedsInput: true},
		RoleView{RoleID: 103, Name: "w3", Kind: db.HeraKindWorker, Live: true, TaskID: "tw3"},
	)
	p.SubtreeNeedsInput = true
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p}})

	collapseByName(t, r, "P")

	testutil.Equal(t, r.depthOf("w1") >= 0, true)
	testutil.Equal(t, r.depthOf("w2") >= 0, true)
	testutil.Equal(t, r.depthOf("w3"), -1)
}

// TestRail_NoRevealWhenNoDescendantNeedsInput is the regression guard: a
// closed coordinator with NO needs-input descendant renders exactly as before
// this feature — header only, nothing beneath it.
func TestRail_NoRevealWhenNoDescendantNeedsInput(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1"},
		RoleView{RoleID: 102, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tw2"},
	)
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p}})

	collapseByName(t, r, "P")

	testutil.Equal(t, r.depthOf("w1"), -1)
	testutil.Equal(t, r.depthOf("w2"), -1)
	testutil.Equal(t, rootHeaderCount(r), 1)
}

// TestRail_RevealDoesNotChangeSpaceToggleBehavior confirms the reveal is a
// pure rendering overlay: Space (ToggleCollapse) on a partially-revealed
// coordinator still fully expands it (revealing the previously-hidden
// sibling too), exactly as it would have before this feature existed, and
// toggling again fully re-collapses (back to the reveal-only view, not to
// nothing).
func TestRail_RevealDoesNotChangeSpaceToggleBehavior(t *testing.T) {
	p := coordOf(1, "P", 100, "tp",
		RoleView{RoleID: 101, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "tw1", NeedsInput: true, SubtreeNeedsInput: true},
		RoleView{RoleID: 102, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "tw2"},
	)
	p.SubtreeNeedsInput = true
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{p}})

	collapseByName(t, r, "P")
	testutil.Equal(t, r.depthOf("w2"), -1) // revealed view: w2 hidden

	// Toggle Space again on P → fully expands, w2 (previously pruned) is back.
	collapseByName(t, r, "P")
	testutil.Equal(t, r.depthOf("w1") >= 0, true)
	testutil.Equal(t, r.depthOf("w2") >= 0, true)

	// Toggle a third time → back to collapsed-with-reveal (not fully hidden).
	collapseByName(t, r, "P")
	testutil.Equal(t, r.depthOf("w1") >= 0, true)
	testutil.Equal(t, r.depthOf("w2"), -1)
}

// TestRail_PinnedRoleBridgedChildRevealsNeedsInputLeaf covers the reveal path
// specific to appendPinnedRole: a pinned non-root role ("w") floats into the
// Pinned section and bridges a child orchestrator ("sub") that is CLOSED.
// Before this feature a closed bridged child hid its whole subtree
// regardless of what it contained; now a needs-input leaf inside it still
// reveals through the fold, mirroring the general appendOrchWorkers reveal.
func TestRail_PinnedRoleBridgedChildRevealsNeedsInputLeaf(t *testing.T) {
	m := nestedSubCoordModel(true, false) // pin "w" (bridges "sub")
	for i := range m.Active {
		if m.Active[i].ID != 2 {
			continue
		}
		for j := range m.Active[i].Roles {
			if m.Active[i].Roles[j].Name == "leaf" {
				m.Active[i].Roles[j].NeedsInput = true
				m.Active[i].Roles[j].SubtreeNeedsInput = true
			}
		}
		m.Active[i].SubtreeNeedsInput = true
	}

	r := NewRail()
	r.collapsed[2] = true // "sub" starts closed
	r.SetModel(m)

	testutil.Equal(t, r.nestedRoleRows("leaf"), 1)
}
