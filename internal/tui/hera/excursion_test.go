package hera

import (
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// twoOrchModelNeedsInput builds twoOrchModel() with NeedsInput stamped on
// whichever roles' TaskID appears in taskIDs — a tiny variant used to drive
// the excursion state machine's count transitions across repeated SetModel
// calls without needing a DB-backed HeraPage.
func twoOrchModelNeedsInput(taskIDs ...string) Model {
	m := twoOrchModel()
	set := make(map[string]bool, len(taskIDs))
	for _, id := range taskIDs {
		set[id] = true
	}
	for oi := range m.Active {
		for ri := range m.Active[oi].Roles {
			role := &m.Active[oi].Roles[ri]
			role.NeedsInput = set[role.TaskID]
		}
	}
	return m
}

// TestModel_NeedsInputTotalCount pins the fold-independent whole-model count
// the excursion state machine tracks: every role's OWN needs-input signal
// (the NeedsInput flag OR a self-reported "blocked" status), across every
// section (Pinned/Active/Archived) AND Freelance, INCLUDING coordinator-kind
// roles (which never get their own rail row — they fold into the
// orchestrator header — but still count here).
func TestModel_NeedsInputTotalCount(t *testing.T) {
	m := Model{
		Active: []OrchView{
			{ID: 1, Name: "orch-1", Roles: []RoleView{
				{RoleID: 11, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t11", NeedsInput: true},
				{RoleID: 12, OrchID: 1, Name: "w1", Kind: db.HeraKindWorker, Live: true, TaskID: "t12", NeedsInput: true},
				{RoleID: 13, OrchID: 1, Name: "w2", Kind: db.HeraKindWorker, Live: true, TaskID: "t13", HasStatus: true, Status: db.HeraStatusBlocked},
				{RoleID: 14, OrchID: 1, Name: "w3", Kind: db.HeraKindWorker, Live: true, TaskID: "t14"}, // no signal
			}},
		},
		Freelance: []RoleView{
			{RoleID: 21, Name: "f1", Kind: db.HeraKindFreelance, Live: true, TaskID: "t21", NeedsInput: true},
		},
	}
	testutil.Equal(t, m.NeedsInputTotalCount(), 4)
}

func TestModel_NeedsInputTotalCount_ZeroOnEmptyModel(t *testing.T) {
	testutil.Equal(t, Model{}.NeedsInputTotalCount(), 0)
}

// TestRail_ExcursionSnapshot_CapturesOnFreshInterruption pins the core
// add-ctrlg-excursion invariant: a snapshot is taken the INSTANT the
// whole-rail needs-input count transitions from 0 to >=1 — not at keypress
// time — and RestoreExcursion re-applies exactly that captured fold state,
// discarding whatever the operator has since done to the fold.
func TestRail_ExcursionSnapshot_CapturesOnFreshInterruption(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput()) // count=0; orch-1 defaults expanded
	testutil.Equal(t, r.OrchCollapsed(1), false)
	testutil.Equal(t, r.HasExcursionSnapshot(), false)

	// Collapse orch-1 (cursor lands on its header by default) — this is the
	// operator's pre-interruption layout.
	r.ToggleCollapse()
	testutil.Equal(t, r.OrchCollapsed(1), true)

	// 0 -> 1 transition: the worker in orch-1 now needs input.
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	// Operator pokes at the fold WHILE the excursion is open.
	r.collapsed[1] = false

	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.OrchCollapsed(1), true) // back to the captured (collapsed) state
	testutil.Equal(t, r.HasExcursionSnapshot(), false)
}

// TestRail_ExcursionSnapshot_SubsequentInterruptionFoldsIntoSameExcursion
// pins "a 3rd ? firing is the same excursion": a second (or third) needs-
// input signal appearing while a snapshot is already held must NOT recapture
// — the ORIGINAL pre-interruption layout survives, even though the operator
// changed the fold in between.
func TestRail_ExcursionSnapshot_SubsequentInterruptionFoldsIntoSameExcursion(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput()) // count=0; orch-1 expanded (F1)
	testutil.Equal(t, r.OrchCollapsed(1), false)

	r.SetModel(twoOrchModelNeedsInput("t12")) // 0 -> 1: captures F1 (expanded)
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	r.ToggleCollapse() // operator collapses orch-1 mid-excursion (F2)
	testutil.Equal(t, r.OrchCollapsed(1), true)

	// A second problem appears (count 1 -> 2): must NOT recapture F2.
	r.SetModel(twoOrchModelNeedsInput("t12", "t21"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)
	testutil.Equal(t, r.OrchCollapsed(1), true) // unaffected by SetModel itself

	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.OrchCollapsed(1), false) // restored to F1, not F2 — proves no recapture happened
}

// TestRail_ExcursionSnapshot_ReArmsAfterExplicitRestoreMidExcursion pins the
// one re-arm path noteExcursionTransition documents: an explicit restore
// (ctrl+b) fired while problems remain (count still >=1) clears the held
// snapshot, so the VERY NEXT rebuild re-arms a fresh one from whatever the
// operator has done to the fold since.
func TestRail_ExcursionSnapshot_ReArmsAfterExplicitRestoreMidExcursion(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput()) // orch-1 expanded (F1)
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	// Explicit manual restore (ctrl+b) while the problem is still outstanding.
	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.HasExcursionSnapshot(), false)
	testutil.Equal(t, r.OrchCollapsed(1), false) // restored to F1 (expanded)

	// Operator collapses orch-1 (F3) after the manual restore.
	r.ToggleCollapse()
	testutil.Equal(t, r.OrchCollapsed(1), true)

	// Count is UNCHANGED (still just t12, still >=1) — this must still re-arm,
	// since excursion == nil now (only the 0->1 edge OR excursion==nil re-arms).
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	r.collapsed[1] = false // poke again to distinguish "restored" from "current"

	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.OrchCollapsed(1), true) // F3 (collapsed) was the re-armed snapshot
}

// TestRail_ExcursionSnapshot_RestoreNoOpWhenNoneHeld covers ctrl+b's silent
// no-op path (nothing to discharge) and ctrl+g's count==0 fallthrough when no
// excursion was ever opened.
func TestRail_ExcursionSnapshot_RestoreNoOpWhenNoneHeld(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput())
	testutil.Equal(t, r.HasExcursionSnapshot(), false)
	testutil.Equal(t, r.RestoreExcursion(), false)
}

// TestRail_ExcursionSnapshot_ClearsWhenCountReturnsToZero confirms a fully
// resolved excursion (count back to 0) leaves the snapshot held (discharge is
// an explicit ctrl+g/ctrl+b action, never automatic) but the NEXT genuine
// interruption starts a brand-new excursion rather than reusing the stale one.
func TestRail_ExcursionSnapshot_ClearsWhenCountReturnsToZero(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput()) // orch-1 expanded (F1)
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	// The problem resolves on its own (count back to 0) — the snapshot is NOT
	// auto-discharged; it waits for an explicit ctrl+g/ctrl+b.
	r.SetModel(twoOrchModelNeedsInput())
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.HasExcursionSnapshot(), false)

	// A fresh interruption after the discharge starts a new excursion.
	r.ToggleCollapse() // F2 (collapsed)
	r.SetModel(twoOrchModelNeedsInput("t21"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)
	r.collapsed[1] = false
	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.OrchCollapsed(1), true) // F2, the fresh capture — not F1
}

// TestRail_NeedsInputCount mirrors Model.NeedsInputTotalCount through the
// Rail accessor ctrl+g/ctrl+b read at keypress time.
func TestRail_NeedsInputCount(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput())
	testutil.Equal(t, r.NeedsInputCount(), 0)
	r.SetModel(twoOrchModelNeedsInput("t12", "t21"))
	testutil.Equal(t, r.NeedsInputCount(), 2)
}
