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

// TestRail_ExcursionSnapshot_DoesNotReArmAfterRestoreWithoutNewEntrant pins
// the CORRECTED re-arm rule (BUG-069 — see noteExcursionTransition): an
// explicit restore (ctrl+b) fired while problems remain (the set is still
// non-empty) clears the held snapshot, but the SAME still-outstanding
// problem reappearing across any number of subsequent rebuilds must NOT
// re-arm a fresh capture — only a genuinely new, distinct role id does (see
// TestRail_ExcursionSnapshot_StaleItemDoesNotFreezeAwayManualNavigation for
// the full manual-navigation scenario this enables). Before the fix this
// test's name described the OPPOSITE (buggy) behavior — an immediate
// re-arm on the very next rebuild regardless of novelty — which is exactly
// what silently discarded the operator's post-restore navigation in the
// live repro.
func TestRail_ExcursionSnapshot_DoesNotReArmAfterRestoreWithoutNewEntrant(t *testing.T) {
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

	// The set is UNCHANGED (still just t12) across several rebuilds — this
	// must NOT re-arm, since t12 is the SAME still-outstanding problem, not a
	// new one.
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), false)
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), false)

	testutil.Equal(t, r.RestoreExcursion(), false) // nothing held — silent no-op
	testutil.Equal(t, r.OrchCollapsed(1), true)    // untouched by the no-op restore
}

// TestRail_ExcursionSnapshot_EntrantAbsorbedWhileFrozenDoesNotReFreezeAfterDischarge
// pins a corner case of the set-membership fix: a role that appears WHILE an
// excursion is already frozen (folding into the same excursion, per the
// existing SubsequentInterruptionFoldsIntoSameExcursion contract) must be
// absorbed into the tracked baseline even though no capture happens for it —
// otherwise, once the operator later discharges the excursion, that same
// role would look "new" all over again on the very next rebuild and trigger
// a spurious re-freeze, even though the operator never had a chance to
// navigate in response to it specifically.
func TestRail_ExcursionSnapshot_EntrantAbsorbedWhileFrozenDoesNotReFreezeAfterDischarge(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput())      // orch-1 expanded (F1)
	r.SetModel(twoOrchModelNeedsInput("t12")) // 0 -> 1: freeze F1
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	// A second role (t21) starts needing input WHILE the excursion is still
	// held — folds into the SAME excursion (no recapture).
	r.SetModel(twoOrchModelNeedsInput("t12", "t21"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	// Discharge while BOTH t12 and t21 remain outstanding.
	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.HasExcursionSnapshot(), false)

	// The exact same pair reappearing (no new id) must NOT re-arm, even
	// though t21 was never individually captured by its own freeze.
	r.SetModel(twoOrchModelNeedsInput("t12", "t21"))
	testutil.Equal(t, r.HasExcursionSnapshot(), false)
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

// TestRail_ExcursionSnapshot_StaleItemDoesNotFreezeAwayManualNavigation is the
// live-repro regression test (Aaron, two back-to-back dogfood tests,
// 2026-07-24, BUG-069): discharging an excursion while a needs-input item is
// STILL outstanding (a stale "?" that never resolves) must NOT re-arm and
// freeze on the very next rebuild regardless of whether anything genuinely
// new happened — that permanently loses any manual fold/selection change
// made afterward until the next explicit restore, which is exactly what the
// operator saw: ctrl+b replayed a stale, much-earlier layout instead of
// their latest manual position. This failed against the shipped count-edge
// implementation before the set-membership fix.
func TestRail_ExcursionSnapshot_StaleItemDoesNotFreezeAwayManualNavigation(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput())      // count=0; orch-1 expanded (F1)
	r.SetModel(twoOrchModelNeedsInput("t12")) // 0 -> 1: freeze F1
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	// Explicit restore (ctrl+b) while t12 is STILL outstanding (never resolved).
	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.HasExcursionSnapshot(), false)

	// The operator manually navigates the rail while t12 remains the ONLY
	// outstanding problem (a stale item that never resolves) — this must NOT
	// be silently frozen away.
	r.ToggleCollapse() // collapse orch-1 (F2)
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), false) // still not re-armed — no new entrant

	r.collapsed[1] = false // expand again (F3) — more manual navigation
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), false) // still tracking live, not frozen

	// NOW a genuinely new, distinct interruption appears elsewhere (orch-2's
	// coordinator, t21) — only THIS should freeze, capturing the operator's
	// LATEST fold state (F3: expanded), not the stale F1/F2.
	r.SetModel(twoOrchModelNeedsInput("t12", "t21"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	r.collapsed[1] = true // poke once more (F4) to distinguish from F3 before restoring

	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.OrchCollapsed(1), false) // F3 (expanded) — latest position, not stale F1/F2
}

// TestRail_ExcursionSnapshot_ColdStartDoesNotCaptureBogusSnapshot is the
// live-repro regression test for BUG-070 (Aaron, 2026-07-25, discovered
// dogfood-testing the BUG-069 fix — reproduced once, then "worked as
// expected" on a retry without relaunching, exactly matching a cold-start-
// only trigger): a fresh Rail's VERY FIRST SetModel call — right after a TUI
// launch/relaunch — already sees a stale, pre-existing needs-input role that
// predates the launch. That call must NOT freeze a snapshot: buildRows has
// never run yet, so currentRef() has no rows to resolve and r.collapsed only
// holds whatever the PREVIOUS session persisted, not this session's
// operator navigation. The eventual freeze (once a genuinely new, distinct
// entrant appears) must reflect the operator's actual current-session
// position, and the cursor restore must not silently no-op.
func TestRail_ExcursionSnapshot_ColdStartDoesNotCaptureBogusSnapshot(t *testing.T) {
	r := NewRail()
	// The FIRST SetModel call this Rail instance ever sees already has a
	// stale, pre-existing needs-input role (t12) — simulates relaunching the
	// TUI while a permanently-stuck "?" predates the launch.
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), false) // no bogus cold-start capture

	// The operator navigates THIS session: collapses orch-2 (its
	// coordinator, t21, folds into the header row) and selects orch-1's
	// worker role.
	testutil.Equal(t, r.SelectByTaskID("t21"), true) // cursor -> orch-2 header
	r.ToggleCollapse()
	testutil.Equal(t, r.OrchCollapsed(2), true)
	testutil.Equal(t, r.SelectByTaskID("t12"), true) // cursor -> t12's worker row

	// The same stale problem persisting across rebuilds must still not arm —
	// no genuinely new entrant (mirrors idle ticks after the relaunch).
	r.SetModel(twoOrchModelNeedsInput("t12"))
	testutil.Equal(t, r.HasExcursionSnapshot(), false)

	// A genuinely new, distinct interruption appears elsewhere — NOW it
	// should freeze, capturing the operator's actual current-session
	// position (orch-2 collapsed, cursor on t12's role), not a cold-start
	// ghost.
	r.SetModel(twoOrchModelNeedsInput("t12", "t21"))
	testutil.Equal(t, r.HasExcursionSnapshot(), true)

	// Poke the fold + cursor once more mid-excursion to prove the eventual
	// restore reflects the real capture, not merely "whatever it already was".
	r.collapsed[2] = false
	r.SelectByTaskID("t21")

	testutil.Equal(t, r.RestoreExcursion(), true)
	testutil.Equal(t, r.OrchCollapsed(2), true)  // real capture, not the poke or a cold-start ghost
	testutil.Equal(t, r.currentRef(), int64(12)) // cursor re-pinned to t12's role — not a silent no-op
}

// TestRail_ExcursionSnapshot_ColdStartWithNoPriorNeedsInputArmsNormally pins
// the non-regression complement of the BUG-070 fix: a fresh Rail whose first
// SetModel call has NO needs-input yet (the ordinary case every other test in
// this file exercises) must still arm exactly as before once a real
// interruption arrives — the cold-start guard must not delay or suppress
// arming beyond that literal first call.
func TestRail_ExcursionSnapshot_ColdStartWithNoPriorNeedsInputArmsNormally(t *testing.T) {
	r := NewRail()
	r.SetModel(twoOrchModelNeedsInput()) // first-ever call, count=0
	testutil.Equal(t, r.HasExcursionSnapshot(), false)

	r.SetModel(twoOrchModelNeedsInput("t12")) // 0 -> 1 on the SECOND call
	testutil.Equal(t, r.HasExcursionSnapshot(), true)
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

// TestModel_NeedsInputRoleIDs pins the set-keyed sibling of
// NeedsInputTotalCount the BUG-069 fix relies on: same membership, same
// traversal, keyed by role id rather than counted.
func TestModel_NeedsInputRoleIDs(t *testing.T) {
	m := twoOrchModelNeedsInput("t12", "t21")
	ids := m.needsInputRoleIDs()
	testutil.Equal(t, len(ids), 2)
	testutil.Equal(t, ids[12], true) // orch-1's worker (TaskID t12)
	testutil.Equal(t, ids[21], true) // orch-2's coordinator (TaskID t21)
	testutil.Equal(t, ids[11], false)

	testutil.Nil(t, Model{}.needsInputRoleIDs())
}

// TestRail_CurrentRef_ZeroOnArchiveExpandoRow documents a known, PRE-EXISTING
// gap (see currentRef's doc) that BUG-069 does not fix: currentRef() has no
// identity encoding for a fold row that isn't a role or an orchestrator
// header, so a cursor resting on the bottom Archive expando (or a
// per-coordinator Archive expando, or the Freelance fold header) at capture
// time yields selRef==0, and restoreCursor(0) is then a no-op. Confirmed
// here so the limitation is explicit rather than silently rediscovered.
func TestRail_CurrentRef_ZeroOnArchiveExpandoRow(t *testing.T) {
	r := NewRail()
	r.SetModel(Model{
		Active: []OrchView{
			{ID: 1, Name: "orch-1", Roles: []RoleView{
				{RoleID: 11, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t11"},
			}},
		},
		Archived: []OrchView{
			{ID: 2, Name: "orch-2-archived", Archived: true, Roles: []RoleView{
				{RoleID: 21, OrchID: 2, Name: "coord2", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t21"},
			}},
		},
	})
	idx := -1
	for i, row := range r.rows {
		if row.kind == rrArchiveExpando && row.collArchive {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatal("expected a bottom Archive expando row")
	}
	r.cursor = idx
	testutil.Equal(t, r.currentRef(), int64(0))

	// restoreCursor(0) is a documented no-op: the cursor stays put rather
	// than being re-pinned to the (unencodable) expando row.
	r.cursor = 0
	r.restoreCursor(0)
	testutil.Equal(t, r.cursor, 0)
}
