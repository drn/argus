package hera

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
)

// fourGroupModel seeds one top-level orchestrator per kanban group — used
// throughout this file to exercise the focus-fold invariant (add-kanban-
// focus-fold): exactly one group's member rows render at a time.
func fourGroupModel() Model {
	return Model{Active: []OrchView{
		{ID: 1, Name: "act", KanbanStatus: db.HeraKanbanActive},
		{ID: 2, Name: "bl", KanbanStatus: db.HeraKanbanBacklog},
		{ID: 3, Name: "blk", KanbanStatus: db.HeraKanbanBlocked},
		{ID: 4, Name: "dn", KanbanStatus: db.HeraKanbanDone},
	}}
}

// TestRail_OnlyFocusedGroupRendersMembers pins tasks.md 1.1: exactly one
// kanban group's member orchestrators render at a time — the other three
// render header+count only. All four headers always render regardless of
// which group is focused.
func TestRail_OnlyFocusedGroupRendersMembers(t *testing.T) {
	m := fourGroupModel()
	all := []string{"act", "bl", "blk", "dn"}
	cases := []struct {
		focus   db.HeraKanbanStatus
		visible string
	}{
		{db.HeraKanbanActive, "act"},
		{db.HeraKanbanBacklog, "bl"},
		{db.HeraKanbanBlocked, "blk"},
		{db.HeraKanbanDone, "dn"},
	}
	for _, tc := range cases {
		t.Run(string(tc.focus), func(t *testing.T) {
			r := NewRail()
			r.SetModel(m)
			r.focusedKanban = tc.focus // direct field write — in-package test seam
			r.buildRows()

			for _, name := range all {
				testutil.Equal(t, r.hasOrchHeader(name), name == tc.visible)
			}
			headers := 0
			for _, row := range r.rows {
				if _, ok := row.kanbanGroupHeader(); ok {
					headers++
				}
			}
			testutil.Equal(t, headers, 4) // all four group headers always render
		})
	}
}

// TestRail_StepCrossesGroupBoundaries pins tasks.md 1.3/1.4: stepping past the
// focused group's last (moving down) or first (moving up) row transparently
// expands the adjacent non-empty group, collapses the one just left, and
// lands the cursor on the new group's first/last member row — never resting
// on the header itself.
func TestRail_StepCrossesGroupBoundaries(t *testing.T) {
	m := Model{Active: []OrchView{
		{ID: 1, Name: "act1", KanbanStatus: db.HeraKanbanActive},
		{ID: 2, Name: "act2", KanbanStatus: db.HeraKanbanActive},
		{ID: 3, Name: "bl", KanbanStatus: db.HeraKanbanBacklog},
		{ID: 4, Name: "blk", KanbanStatus: db.HeraKanbanBlocked},
		{ID: 5, Name: "dn", KanbanStatus: db.HeraKanbanDone},
	}}
	r := NewRail()
	r.SetModel(m)
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanActive)
	testutil.Equal(t, r.SelectedOrch().Name, "act1")

	r.CursorDown() // → act2 (Active's last member)
	testutil.Equal(t, r.SelectedOrch().Name, "act2")

	// Stepping down from Active's LAST row crosses into Backlog: Backlog
	// expands, Active collapses to header-only, cursor lands on Backlog's
	// first (here, only) member row.
	r.CursorDown()
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanBacklog)
	testutil.Equal(t, r.SelectedOrch().Name, "bl")
	testutil.Equal(t, r.hasOrchHeader("act1"), false)
	testutil.Equal(t, r.hasOrchHeader("act2"), false)

	// Stepping back UP from Backlog's first (only) row crosses back into
	// Active, landing on Active's LAST member row (act2, not act1) — proving
	// the "last on up-crossing" half distinctly from "first on down-crossing".
	r.CursorUp()
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanActive)
	testutil.Equal(t, r.SelectedOrch().Name, "act2")
	testutil.Equal(t, r.hasOrchHeader("bl"), false)
}

// TestRail_StepSkipsEmptyIntermediateGroup pins tasks.md 1.5: crossing skips a
// genuinely empty intermediate group entirely (it renders no header row at
// all) — stepping down from Backlog's last row lands directly in Done's first
// row, never pausing on a nonexistent Blocked boundary.
func TestRail_StepSkipsEmptyIntermediateGroup(t *testing.T) {
	m := Model{Active: []OrchView{
		{ID: 1, Name: "act", KanbanStatus: db.HeraKanbanActive},
		{ID: 2, Name: "bl", KanbanStatus: db.HeraKanbanBacklog},
		{ID: 3, Name: "dn", KanbanStatus: db.HeraKanbanDone},
		// deliberately no Blocked-status orchestrator at all
	}}
	r := NewRail()
	r.SetModel(m)

	r.CursorDown() // act → crosses into Backlog, lands on bl
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanBacklog)
	testutil.Equal(t, r.SelectedOrch().Name, "bl")

	r.CursorDown() // bl → Blocked is empty (no row at all) → lands directly on dn
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanDone)
	testutil.Equal(t, r.SelectedOrch().Name, "dn")
}

// TestRail_KanbanKeyRefocusesOnStatusChange pins tasks.md 1.6: pressing `m`/`M`
// on the selected top-level coordinator moves it to a different kanban group
// — modeled here as the SAME primitive the real key handler drives (Ops.
// KanbanStep mutates the DB, then doRefresh calls Rail.SetModel with the
// rebuilt model) — and the coordinator stays selected, with its new group now
// focused.
func TestRail_KanbanKeyRefocusesOnStatusChange(t *testing.T) {
	build := func(status db.HeraKanbanStatus) Model {
		return Model{Active: []OrchView{
			{ID: 1, Name: "act", KanbanStatus: db.HeraKanbanActive},
			{ID: 2, Name: "coord", KanbanStatus: status, Roles: []RoleView{
				{RoleID: 21, OrchID: 2, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc"},
			}},
		}}
	}
	r := NewRail()
	r.SetModel(build(db.HeraKanbanBacklog))

	r.CursorDown() // act → crosses into Backlog, lands on "coord"'s header
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanBacklog)
	testutil.Equal(t, r.SelectedOrch().Name, "coord")

	// Simulate the m/M mutation: "coord"'s underlying KanbanStatus flips to
	// Done and the page rebuilds via SetModel (heraKanbanStep → heraRefresh →
	// doRefresh → rail.SetModel — see internal/tui/heraactions.go).
	r.SetModel(build(db.HeraKanbanDone))

	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanDone)
	testutil.Equal(t, r.SelectedOrch() != nil, true)
	testutil.Equal(t, r.SelectedOrch().Name, "coord") // stays selected across the jump
}

// TestRail_SelectByTaskIDRefocusesNonFocusedGroup pins tasks.md 1.7:
// SelectByTaskID targeting a role whose top-level orchestrator belongs to a
// currently non-focused kanban group re-focuses that group BEFORE locating
// the row, so the jump succeeds instead of silently failing (the row would
// not exist in the built rows otherwise).
func TestRail_SelectByTaskIDRefocusesNonFocusedGroup(t *testing.T) {
	m := Model{Active: []OrchView{
		{ID: 1, Name: "act", KanbanStatus: db.HeraKanbanActive, Roles: []RoleView{
			{RoleID: 11, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "ta"},
		}},
		{ID: 2, Name: "bl", KanbanStatus: db.HeraKanbanBacklog, Roles: []RoleView{
			{RoleID: 21, OrchID: 2, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tb"},
			{RoleID: 22, OrchID: 2, Name: "wkr", Kind: db.HeraKindWorker, Live: true, TaskID: "tw"},
		}},
	}}
	r := NewRail()
	r.SetModel(m)
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanActive) // default focus
	testutil.Equal(t, r.hasOrchHeader("bl"), false)           // Backlog not focused yet

	ok := r.SelectByTaskID("tw") // "wkr" lives under "bl", in the non-focused Backlog group
	testutil.Equal(t, ok, true)
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanBacklog)
	testutil.Equal(t, r.Selected() != nil, true)
	testutil.Equal(t, r.Selected().Name, "wkr")
}

// TestRail_EnsureAncestorsExpandedRefocusesNonFocusedGroup pins tasks.md 1.8:
// EnsureAncestorsExpanded likewise re-focuses the target's kanban group when
// its top-level ancestor sits in a non-focused group — even when no
// per-orchestrator collapse flag needed flipping (nothing here was folded).
func TestRail_EnsureAncestorsExpandedRefocusesNonFocusedGroup(t *testing.T) {
	root := orchView(1, "R", "tr", wk("w", "tc"))
	root.KanbanStatus = db.HeraKanbanBacklog
	child := orchView(2, "C", "tc", wk("wc", "twc"))
	act := OrchView{ID: 3, Name: "act", KanbanStatus: db.HeraKanbanActive}
	r := NewRail()
	r.SetModel(Model{Active: []OrchView{act, root, child}})
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanActive)
	testutil.Equal(t, r.hasOrchHeader("R"), false) // Backlog not focused yet

	r.EnsureAncestorsExpanded(2) // C's canonical top-level ancestor is R (Backlog)
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanBacklog)
	testutil.Equal(t, r.hasOrchHeader("R"), true)
	testutil.Equal(t, r.hasOrchHeader("C"), false) // C nests under R, not its own header
}

// TestRail_DefaultFocusedGroupIsActive pins tasks.md 1.9: the default focused
// group is `active` — both before any model is ever set (NewRail's default),
// and on the very first non-empty build when nothing prior resolves, EVEN
// WHEN the only orchestrator present belongs to a different group entirely.
func TestRail_DefaultFocusedGroupIsActive(t *testing.T) {
	r := NewRail()
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanActive)

	r.SetModel(Model{Active: []OrchView{{ID: 1, Name: "o", KanbanStatus: db.HeraKanbanBacklog}}})
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanActive)
}

// TestRail_KanbanFocusNotPersisted pins tasks.md 1.10: kanban fold is derived,
// never persisted — a RailStateStore round-trip carries per-orchestrator/
// Freelance/Archive fold (via SelectionRef re-resolution) but no dedicated
// kanban-fold field, unlike collapsed/freelance_collapsed/archive_collapsed.
func TestRail_KanbanFocusNotPersisted(t *testing.T) {
	fs := &fakeStore{}
	r := NewRail()
	r.SetStateStore(fs)
	m := fourGroupModel()
	r.SetModel(m)

	// Cross into Backlog (re-focuses away from the `active` default); the
	// crossing cursor move persists the new selection.
	r.CursorDown()
	testutil.Equal(t, r.FocusedKanban(), db.HeraKanbanBacklog)
	if len(fs.saved) == 0 {
		t.Fatal("expected the boundary-crossing cursor move to persist the new selection")
	}
	raw := fs.saved[len(fs.saved)-1]
	if strings.Contains(strings.ToLower(raw), "kanban") {
		t.Fatalf("persisted rail state must not carry a kanban-fold field, got: %s", raw)
	}

	// A FRESH Rail restoring that SAME blob re-derives its focused group from
	// the restored selection ref on the first model build — not from any
	// persisted kanban-fold field — landing back on Backlog because that's
	// where the restored selection is.
	r2 := NewRail()
	r2.SetStateStore(&fakeStore{load: raw})
	r2.SetModel(m)
	testutil.Equal(t, r2.FocusedKanban(), db.HeraKanbanBacklog)
}
