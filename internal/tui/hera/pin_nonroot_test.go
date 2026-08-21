package hera

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
)

// --- fixtures -------------------------------------------------------------

// pinnedLeafModel: one active root orchestrator with a coordinator and a single
// pinned leaf worker.
func pinnedLeafModel() Model {
	return Model{Active: []OrchView{{ID: 1, Name: "root", Roles: []RoleView{
		{RoleID: 10, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc", BridgeTaskID: "tc"},
		{RoleID: 11, OrchID: 1, Name: "leaf", Kind: db.HeraKindWorker, Live: true, TaskID: "t11", BridgeTaskID: "t11", Pinned: true},
	}}}}
}

// nestedSubCoordModel: root → (worker "w" bridges) sub-coordinator orch "sub" →
// leaf worker. pinSub pins the bridging worker "w" (sub-coord pin); pinLeaf pins
// the deep leaf under "sub" (lineage test).
func nestedSubCoordModel(pinSub, pinLeaf bool) Model {
	return Model{Active: []OrchView{
		{ID: 1, Name: "root", Roles: []RoleView{
			{RoleID: 10, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "troot", BridgeTaskID: "troot"},
			{RoleID: 11, OrchID: 1, Name: "w", Kind: db.HeraKindWorker, Live: true, TaskID: "tc", BridgeTaskID: "tc", Pinned: pinSub},
		}},
		{ID: 2, Name: "sub", Roles: []RoleView{
			{RoleID: 20, OrchID: 2, Name: "subcoord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc", BridgeTaskID: "tc"},
			{RoleID: 21, OrchID: 2, Name: "leaf", Kind: db.HeraKindWorker, Live: true, TaskID: "t21", BridgeTaskID: "t21", Pinned: pinLeaf},
		}},
	}}
}

// --- row inspection seams (test-only) -------------------------------------

func (r *Rail) countRows(pred func(railRow) bool) int {
	n := 0
	for _, row := range r.rows {
		if pred(row) {
			n++
		}
	}
	return n
}

func (r *Rail) breadcrumbFor(name string) (string, bool) {
	for _, row := range r.rows {
		if row.kind == rrPinnedBreadcrumb && row.role != nil && row.role.Name == name {
			return row.breadcrumb, true
		}
	}
	return "", false
}

func hasPinnedHeader(r *Rail) bool {
	return r.countRows(func(row railRow) bool {
		return row.kind == rrSectionHeader && row.label == "Pinned"
	}) == 1
}

// nestedRoleRows counts ordinary nested role rows (not the floated breadcrumb
// entry) referencing the named role — used to prove single placement.
func (r *Rail) nestedRoleRows(name string) int {
	return r.countRows(func(row railRow) bool {
		return row.kind == rrRole && !row.breadcrumbCont && row.role != nil && row.role.Name == name
	})
}

// --- tests ----------------------------------------------------------------

// 1.2 + 1.7: a pinned leaf floats as a two-line breadcrumb entry under a Pinned
// header, and does not also render nested under its orchestrator.
func TestRail_PinnedLeafFloatsAsBreadcrumb(t *testing.T) {
	r := NewRail()
	r.SetModel(pinnedLeafModel())

	testutil.Equal(t, hasPinnedHeader(r), true)
	// Exactly one breadcrumb (line 1) + one continuation (line 2) for "leaf".
	testutil.Equal(t, r.countRows(func(row railRow) bool {
		return row.kind == rrPinnedBreadcrumb && row.role != nil && row.role.Name == "leaf"
	}), 1)
	testutil.Equal(t, r.countRows(func(row railRow) bool {
		return row.breadcrumbCont && row.role != nil && row.role.Name == "leaf"
	}), 1)
	// Single placement: not also nested under root.
	testutil.Equal(t, r.nestedRoleRows("leaf"), 0)
	// Lineage is the role's own orchestrator chain.
	bc, ok := r.breadcrumbFor("leaf")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, bc, "root › ")
}

// 1.3: lineage spans the full CanonicalParents chain for a deeply-nested role.
func TestRail_PinnedBreadcrumbLineageIsCanonicalChain(t *testing.T) {
	r := NewRail()
	r.SetModel(nestedSubCoordModel(false, true)) // pin the deep leaf under "sub"

	bc, ok := r.breadcrumbFor("leaf")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, bc, "root › sub › ")
	testutil.Equal(t, r.nestedRoleRows("leaf"), 0) // floated, not nested
}

// 1.5: a pinned role whose orchestrator is itself pinned stays nested (no float).
func TestRail_PinnedRoleUnderPinnedOrchStaysNested(t *testing.T) {
	m := Model{Pinned: []OrchView{{ID: 1, Name: "root", Pinned: true, Roles: []RoleView{
		{RoleID: 10, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc", BridgeTaskID: "tc"},
		{RoleID: 11, OrchID: 1, Name: "leaf", Kind: db.HeraKindWorker, Live: true, TaskID: "t11", BridgeTaskID: "t11", Pinned: true},
	}}}}
	r := NewRail()
	r.SetModel(m)

	// No standalone breadcrumb entry; the role renders nested under the pinned root.
	testutil.Equal(t, r.countRows(func(row railRow) bool { return row.kind == rrPinnedBreadcrumb }), 0)
	testutil.Equal(t, r.nestedRoleRows("leaf"), 1)
}

// 1.6: a pinned sub-coordinator hoists its whole subtree into the Pinned block,
// in both fold states, rendering the child exactly once.
func TestRail_PinnedSubCoordHoistsSubtree(t *testing.T) {
	r := NewRail()
	r.SetModel(nestedSubCoordModel(true, false)) // pin the bridging worker "w"

	// "w" floats as a breadcrumb entry carrying the child orch id.
	var crumb *railRow
	for i := range r.rows {
		if r.rows[i].kind == rrPinnedBreadcrumb && r.rows[i].role != nil && r.rows[i].role.Name == "w" {
			crumb = &r.rows[i]
		}
	}
	testutil.Equal(t, crumb != nil, true)
	testutil.Equal(t, crumb.collOrchID, int64(2)) // bridges "sub"
	// The hoisted child's leaf renders beneath it, exactly once, and "w" does not
	// also render nested in the active tree.
	testutil.Equal(t, r.nestedRoleRows("leaf"), 1)
	testutil.Equal(t, r.nestedRoleRows("w"), 0)

	// Collapse the hoisted subtree: the leaf disappears but "w" stays; the child
	// is not leaked to a top-level root.
	r.collapsed[2] = true
	r.SetModel(nestedSubCoordModel(true, false))
	testutil.Equal(t, r.nestedRoleRows("leaf"), 0)
	testutil.Equal(t, r.hasOrchHeader("sub"), false)
	testutil.Equal(t, r.countRows(func(row railRow) bool {
		return row.kind == rrPinnedBreadcrumb && row.role != nil && row.role.Name == "w"
	}), 1)
}

// 1.7b: the Pinned header shows when only a non-root role is pinned.
func TestRail_PinnedHeaderShowsForRoleOnly(t *testing.T) {
	r := NewRail()
	r.SetModel(pinnedLeafModel()) // no pinned orchestrator
	testutil.Equal(t, hasPinnedHeader(r), true)
}

// 1.8: cursor anchors on the breadcrumb line, skips the continuation, and
// re-pins by role id after a rebuild.
func TestRail_PinnedBreadcrumbCursorAnchorAndSkip(t *testing.T) {
	r := NewRail()
	r.SetModel(pinnedLeafModel())

	// Navigate to the breadcrumb row (root header is row 1 after the section
	// header). Step down until the cursor lands on the "leaf" breadcrumb.
	landed := false
	for i := 0; i < 10; i++ {
		row := r.rows[r.CursorIndex()]
		if row.kind == rrPinnedBreadcrumb && row.role != nil && row.role.Name == "leaf" {
			landed = true
			break
		}
		r.CursorDown()
	}
	testutil.Equal(t, landed, true)
	// The cursor never rests on a continuation line.
	testutil.Equal(t, r.rows[r.CursorIndex()].breadcrumbCont, false)

	// Rebuild: cursor re-pins onto the same breadcrumb by role id.
	r.SetModel(pinnedLeafModel())
	cur := r.rows[r.CursorIndex()]
	testutil.Equal(t, cur.kind == rrPinnedBreadcrumb && cur.role != nil && cur.role.Name == "leaf", true)
}

// 1.9: under an active filter a non-matching floated pinned role is omitted (and
// the Pinned header prunes when empty); a matching one stays.
func TestRail_PinnedRoleFilterStates(t *testing.T) {
	r := NewRail()
	r.filterQuery = "leaf"
	r.SetModel(pinnedLeafModel())
	testutil.Equal(t, hasPinnedHeader(r), true)
	testutil.Equal(t, r.countRows(func(row railRow) bool { return row.kind == rrPinnedBreadcrumb }), 1)

	r.filterQuery = "zzz-no-match"
	r.SetModel(pinnedLeafModel())
	testutil.Equal(t, hasPinnedHeader(r), false)
	testutil.Equal(t, r.countRows(func(row railRow) bool { return row.kind == rrPinnedBreadcrumb }), 0)
}

// 1.10: a pinned role with an unresolvable orchestrator is skipped (not rendered
// without lineage).
func TestRail_PinnedRoleUnresolvableParentNotFloated(t *testing.T) {
	m := Model{Active: []OrchView{{ID: 1, Name: "root", Roles: []RoleView{
		{RoleID: 10, OrchID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "tc", BridgeTaskID: "tc"},
		// OrchID 99 has no OrchView → breadcrumb unresolvable.
		{RoleID: 11, OrchID: 99, Name: "orphan", Kind: db.HeraKindWorker, Live: true, TaskID: "t11", BridgeTaskID: "t11", Pinned: true},
	}}}}
	r := NewRail()
	r.SetModel(m)
	testutil.Equal(t, r.countRows(func(row railRow) bool { return row.kind == rrPinnedBreadcrumb }), 0)
}

// 1.4 + render smoke: the breadcrumb trail and name draw on the SimulationScreen,
// and an over-wide trail is left-truncated with a leading "…".
func TestRail_PinnedBreadcrumbDrawsAndLeftTruncates(t *testing.T) {
	sim := tcell.NewSimulationScreen("UTF-8")
	testutil.NoError(t, sim.Init())
	defer sim.Fini()
	sim.SetSize(40, 12)

	r := NewRail()
	r.SetFocused(true)
	r.SetModel(pinnedLeafModel())
	r.SetRect(0, 0, 40, 12)
	r.Draw(sim)
	sim.Show()

	full := simText(sim, 40, 12)
	testutil.Equal(t, strings.Contains(full, "root"), true) // breadcrumb trail
	testutil.Equal(t, strings.Contains(full, "leaf"), true) // continuation name

	// Left-truncation unit: a trail wider than the width keeps the rightmost text.
	testutil.Equal(t, truncRunesLeft("aaaa › bbbb › cccc › ", 8), "…cccc › ")
	testutil.Equal(t, truncRunesLeft("short", 10), "short")
	testutil.Equal(t, truncRunesLeft("anything", 0), "")
}

// 1.1: BuildModel projects hera_roles.pinned_at into RoleView.Pinned.
func TestBuildModel_RoleViewPinned(t *testing.T) {
	d := memDB(t)
	orchID := seedOrch(t, d, "root")
	seedBoundRole(t, d, orchID, "coord", db.HeraKindCoordinator, "tc")
	worker := seedBoundRole(t, d, orchID, "leaf", db.HeraKindWorker, "t11")
	testutil.NoError(t, d.PinHeraRole(worker.ID))

	m, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)

	var got *RoleView
	for i := range m.Active {
		for j := range m.Active[i].Roles {
			if m.Active[i].Roles[j].RoleID == worker.ID {
				got = &m.Active[i].Roles[j]
			}
		}
	}
	testutil.Equal(t, got != nil, true)
	testutil.Equal(t, got.Pinned, true)

	testutil.NoError(t, d.UnpinHeraRole(worker.ID))
	m2, err := BuildModel(d, nil, nil, nil, nil)
	testutil.NoError(t, err)
	for i := range m2.Active {
		for j := range m2.Active[i].Roles {
			if m2.Active[i].Roles[j].RoleID == worker.ID {
				testutil.Equal(t, m2.Active[i].Roles[j].Pinned, false)
			}
		}
	}
}

// simText reads the whole simulation screen into a newline-joined string.
func simText(sim tcell.SimulationScreen, w, h int) string {
	var b strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			s, _, _ := sim.Get(x, y)
			b.WriteString(s)
		}
		b.WriteByte('\n')
	}
	return b.String()
}
