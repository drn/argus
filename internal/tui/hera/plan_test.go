package hera

import (
	"fmt"
	"testing"

	"github.com/drn/argus/internal/db"
	"github.com/drn/argus/internal/model"
	"github.com/drn/argus/internal/testutil"
	"github.com/drn/argus/internal/tui/planview"
	"github.com/drn/argus/internal/tui/theme"
	"github.com/drn/argus/internal/tui/widget"
)

// seedPlannedRole creates a planned (never-bound) worker role and returns it.
func seedPlannedRole(t *testing.T, d *db.DB, orchID int64, name string) *db.HeraRole {
	t.Helper()
	r, err := d.CreateHeraPlannedRole(db.CreateHeraRoleInput{
		OrchestratorID: orchID, Name: name, ArgusProject: "p", Prompt: "do " + name,
	})
	testutil.NoError(t, err)
	return r
}

// orchViewByName builds the model and returns the OrchView with the given name
// (across all sections), or nil. BuildModel populates OrchView.Blocks and the
// RoleView.Planned discriminator (Stage 2).
func orchViewByName(t *testing.T, d *db.DB, name string) *OrchView {
	t.Helper()
	m, err := BuildModel(d, nil)
	testutil.NoError(t, err)
	for _, sec := range [][]OrchView{m.Pinned, m.Active, m.Archived} {
		for i := range sec {
			if sec[i].Name == name {
				return &sec[i]
			}
		}
	}
	return nil
}

// findNode returns the planview.Node with the given ID, or false.
func findNode(nodes []planview.Node, id string) (planview.Node, bool) {
	for _, n := range nodes {
		if n.ID == id {
			return n, true
		}
	}
	return planview.Node{}, false
}

// --- RoleView.Planned discriminator (hera-view delta) ---

// TestRoleViewPlanned_NeverBoundIsPlanned mirrors "it should mark a never-bound
// worker role as planned and a bound (live or ended) role as not planned".
func TestRoleViewPlanned_NeverBoundIsPlanned(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	planned := seedPlannedRole(t, d, orch, "2a-planned")
	seedBoundRole(t, d, orch, "live", db.HeraKindWorker, "t-live")

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)

	byID := map[int64]RoleView{}
	for _, r := range ov.Roles {
		byID[r.RoleID] = r
	}
	// The planned (never-bound) worker is Planned.
	testutil.Equal(t, byID[planned.ID].Planned, true)
	// The coordinator and the live worker are NOT planned.
	for _, r := range ov.Roles {
		if r.Kind == db.HeraKindCoordinator || r.TaskID == "t-live" {
			testutil.Equal(t, r.Planned, false)
		}
	}
}

// TestRoleViewPlanned_EndedBindingIsNotPlanned: a worker that was materialized
// and whose binding then ended is NOT planned (the gater never re-materializes).
func TestRoleViewPlanned_EndedBindingIsNotPlanned(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	role, binding, err := d.CreateHeraRoleWithBinding(db.CreateHeraRoleInput{
		OrchestratorID: orch, Name: "ended", Kind: db.HeraKindWorker, ArgusProject: "p",
	}, "t-ended", "/wt/ended")
	testutil.NoError(t, err)
	testutil.NoError(t, d.EndHeraBinding(binding.ID, "done"))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	for _, r := range ov.Roles {
		if r.RoleID == role.ID {
			testutil.Equal(t, r.Planned, false)
		}
	}
}

// --- OrchView.Blocks population (hera-view delta D8) ---

// TestBuildModel_PopulatesOrchBlocks: BuildModel attaches the orchestrator's
// blocking edges to OrchView.Blocks (one bulk read).
func TestBuildModel_PopulatesOrchBlocks(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	a := seedPlannedRole(t, d, orch, "1a")
	b := seedPlannedRole(t, d, orch, "2a")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // 2a←1a

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	testutil.DeepEqual(t, ov.Blocks, []db.HeraBlock{{BlockedRoleID: b.ID, BlockerRoleID: a.ID}})
}

// --- heraPlanNodes projection (hera-view delta) ---

// TestHeraPlanNodes_PlannedAndLiveWithEdges mirrors "it should project planned
// (never-bound) roles and live roles together as plan nodes with their blocking
// edges". Planned nodes carry State=StatePlanned; the blocking edge becomes a
// planview.Edge.
func TestHeraPlanNodes_PlannedAndLiveWithEdges(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	a := seedPlannedRole(t, d, orch, "1a-research")
	b := seedPlannedRole(t, d, orch, "2a-write")
	testutil.NoError(t, d.AddHeraBlock(b.ID, a.ID)) // 2a←1a

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)

	nodes, edges := heraPlanNodes(ov)

	// Both planned worker roles appear as nodes.
	testutil.Equal(t, len(nodes) >= 2, true)
	var seen1a, seen2a bool
	for _, n := range nodes {
		if n.Name == "1a-research" {
			seen1a = true
			testutil.Equal(t, n.Planned, true)
			testutil.Equal(t, n.State, planview.StatePlanned)
		}
		if n.Name == "2a-write" {
			seen2a = true
			testutil.Equal(t, n.Planned, true)
		}
	}
	testutil.Equal(t, seen1a, true)
	testutil.Equal(t, seen2a, true)

	// Exactly one dependency edge: 2a depends on 1a (To=2a, From=1a). Pin the
	// direction — From is the blocker (1a, upstream), To is the blocked (2a,
	// downstream) — so the stage layering matches dagview's convention.
	testutil.Equal(t, len(edges), 1)
	from2a, _ := findNode(nodes, edges[0].From)
	to2a, _ := findNode(nodes, edges[0].To)
	testutil.Equal(t, from2a.Name, "1a-research")
	testutil.Equal(t, to2a.Name, "2a-write")
}

// TestHeraPlanNodes_FailedResultIsStateFailed pins that a live node whose bound
// task reported a {"failed":true} result projects StateFailed (red ✕), winning
// over the workflow status (D7).
func TestHeraPlanNodes_FailedResultIsStateFailed(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "boom", db.HeraKindWorker, "t-boom")
	testutil.NoError(t, d.SetStatus("t-boom", model.StatusInReview))
	testutil.NoError(t, d.SetResult("t-boom", `{"failed":true}`))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	nodes, _ := heraPlanNodes(ov)
	n, ok := findNode(nodes, "t-boom")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, n.State, planview.StateFailed)
}

// TestHeraPlanNodes_LiveNodeColoursFromTaskStatus mirrors "a live node by its
// bound task status (including red ✕ on a failed result)". The projection
// stamps State from the bound task's status/result.
func TestHeraPlanNodes_LiveNodeColoursFromTaskStatus(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	// A live worker whose task is complete.
	seedBoundRole(t, d, orch, "done-wkr", db.HeraKindWorker, "t-done")
	testutil.NoError(t, d.SetStatus("t-done", model.StatusComplete))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	nodes, _ := heraPlanNodes(ov)

	n, ok := findNode(nodes, "t-done")
	testutil.Equal(t, ok, true)
	testutil.Equal(t, n.Planned, false)
	testutil.Equal(t, n.State, planview.StateDone)
}

// TestHeraPlanNodes_DegenerateNoPlanFlatStage mirrors "render the orchestrator's
// live roles as a flat edgeless stage with a 'no plan' hint when no plan is
// authored". With no planned nodes and no edges, the live workers project as
// nodes with no edges between them.
func TestHeraPlanNodes_DegenerateNoPlanFlatStage(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	seedBoundRole(t, d, orch, "w1", db.HeraKindWorker, "t-w1")
	seedBoundRole(t, d, orch, "w2", db.HeraKindWorker, "t-w2")

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	nodes, edges := heraPlanNodes(ov)

	// Live workers are present as nodes; no plan = no edges.
	_, ok1 := findNode(nodes, "t-w1")
	_, ok2 := findNode(nodes, "t-w2")
	testutil.Equal(t, ok1, true)
	testutil.Equal(t, ok2, true)
	testutil.Equal(t, len(edges), 0)
}

// TestHeraPlanNodes_NilOrchEmpty: a nil orchestrator yields no nodes/edges
// without panic (remote-mode / no-selection degradation).
func TestHeraPlanNodes_NilOrchEmpty(t *testing.T) {
	nodes, edges := heraPlanNodes(nil)
	testutil.Equal(t, len(nodes), 0)
	testutil.Equal(t, len(edges), 0)
}

// --- BUG-007: plan node icons 1:1 with the rail's statusIcon ---

// projectWorkerIcon builds a one-coordinator/one-worker orchestrator from the
// given worker RoleView, projects it, and returns the worker node's resolved Icon
// (nil for planned/failed, which use the State overlay).
func projectWorkerIcon(t *testing.T, wkr RoleView) *planview.NodeIcon {
	t.Helper()
	wkr.Kind = db.HeraKindWorker
	ov := &OrchView{ID: 1, Name: "orch", Roles: []RoleView{
		{RoleID: 1, Name: "coord", Kind: db.HeraKindCoordinator, Live: true, TaskID: "t-coord"},
		wkr,
	}}
	nodes, _ := heraPlanNodes(ov)
	for _, n := range nodes {
		if n.Name == wkr.Name {
			return n.Icon
		}
	}
	t.Fatalf("worker node %q not projected", wkr.Name)
	return nil
}

// TestPlanNodeIcon_LiveMatchesRailStatusIcon is the BUG-007 headline: a LIVE
// node's projected icon (glyph + style) is IDENTICAL to what the rail's
// statusIcon renders for the same role — done / working / idle / in-review /
// needs-input — because both go through the one shared classifier
// (widget.RoleStatusIcon). Frame 0 keeps the spinner comparison deterministic.
func TestPlanNodeIcon_LiveMatchesRailStatusIcon(t *testing.T) {
	cases := []struct {
		name string
		role RoleView
	}{
		{"done", RoleView{RoleID: 2, Name: "w-done", Live: true, TaskID: "t1", BridgeTaskID: "t1", HasStatus: true, Status: db.HeraStatusDone, TaskStatus: model.StatusInReview.String()}},
		{"working/active", RoleView{RoleID: 2, Name: "w-work", Live: true, TaskID: "t1", BridgeTaskID: "t1", TaskStatus: model.StatusInProgress.String()}},
		{"idle", RoleView{RoleID: 2, Name: "w-idle", Live: true, TaskID: "t1", BridgeTaskID: "t1", HasStatus: true, Status: db.HeraStatusIdle}},
		{"in-review/ready", RoleView{RoleID: 2, Name: "w-rev", Live: true, TaskID: "t1", BridgeTaskID: "t1", ReadyToClose: true}},
		{"needs-input", RoleView{RoleID: 2, Name: "w-ni", Live: true, TaskID: "t1", BridgeTaskID: "t1", NeedsInput: true}},
		{"live-quiet", RoleView{RoleID: 2, Name: "w-live", Live: true, TaskID: "t1", BridgeTaskID: "t1"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantGlyph, wantStyle := statusIcon(&tc.role, false, 0)
			icon := projectWorkerIcon(t, tc.role)
			testutil.Equal(t, icon != nil, true)
			// Style is always 1:1. The glyph is 1:1 too; for the animated case the
			// projection stores the frame-0 glyph and flags Animated (the widget
			// re-resolves the live frame at Draw), so frame-0 here matches.
			testutil.Equal(t, icon.Style, wantStyle)
			testutil.Equal(t, icon.Glyph, wantGlyph)
		})
	}
}

// effectiveGlyph mirrors how the planview widget resolves a node's RENDERED
// glyph (planview.nodeGlyph / headerStatusGlyph): an Animated icon re-resolves to
// the live spinner frame at Draw; otherwise the stored glyph is shown. The
// needs-input parity check must compare THIS — what the operator actually sees —
// not the raw Icon.Glyph, because a correct Icon.Glyph that is wrongly flagged
// Animated still renders the spinner (BUG-012).
func effectiveGlyph(icon *planview.NodeIcon, frame int) rune {
	if icon == nil {
		return 0
	}
	if icon.Animated {
		return widget.SpinnerFrame(frame)
	}
	return icon.Glyph
}

// TestPlanNodeIcon_NeedsInputNotAnimated is the BUG-012 headline: a needs-input
// role whose bound task is ALSO live + in_progress (IsActive) must render the
// static needs-input "?" on its plan node, 1:1 with the rail — NOT the working
// spinner. needs-input OUTRANKS active in the shared classifier, so the resolved
// glyph is "?"; the projection must therefore NOT flag the icon Animated (which
// would make the widget swap "?" for the live spinner frame at Draw, the exact
// parity break). Covered through the EFFECTIVE rendered glyph so the assertion
// catches the Animated-override even when Icon.Glyph itself is correct. Both the
// role's OWN signal and a descendant subtree-rollup case are exercised.
func TestPlanNodeIcon_NeedsInputNotAnimated(t *testing.T) {
	t.Run("own signal (blocked + in_progress)", func(t *testing.T) {
		// A worker blocked on a prompt while its task is still in_progress: active
		// AND needs-input. The rail shows "?"; the plan node must too.
		role := RoleView{
			RoleID: 2, Name: "2b-prompter", Kind: db.HeraKindWorker,
			Live: true, TaskID: "t1", BridgeTaskID: "t1",
			TaskStatus: model.StatusInProgress.String(),
			HasStatus:  true, Status: db.HeraStatusBlocked,
		}
		testutil.Equal(t, role.IsActive(), true)        // genuinely working...
		testutil.Equal(t, role.ShowsNeedsInput(), true) // ...AND blocked on input
		wantGlyph, wantStyle := statusIcon(&role, false, 0)
		testutil.Equal(t, wantGlyph, theme.IconNeedsInput)

		icon := projectWorkerIcon(t, role)
		testutil.Equal(t, icon != nil, true)
		testutil.Equal(t, icon.Animated, false) // the fix: must not animate "?"
		testutil.Equal(t, icon.Style, wantStyle)
		// The EFFECTIVE rendered glyph (what the widget paints) is the static "?".
		testutil.Equal(t, effectiveGlyph(icon, 0), wantGlyph)
	})

	t.Run("descendant subtree rollup", func(t *testing.T) {
		// R(coord tr, worker w→tc) → C(coord tc, worker wc→twc). The leaf wc needs
		// input; the bridging sub-coordinator w is itself live + in_progress
		// (active). After the rollup w.SubtreeNeedsInput is true, so the rail shows
		// "?" on w — and so must w's plan node, not the spinner.
		r := orchView(1, "R", "tr", wk("w", "tc"))
		c := orchView(2, "C", "tc", wk("wc", "twc"))
		m := Model{Active: []OrchView{r, c}}
		roleByName(t, &m, 2, "wc").NeedsInput = true
		m.rollupNeedsInput()

		w := roleByName(t, &m, 1, "w")
		testutil.Equal(t, w.IsActive(), true)        // bridging sub-coord is working...
		testutil.Equal(t, w.SubtreeNeedsInput, true) // ...with a descendant blocked
		wantGlyph, wantStyle := statusIcon(w, false, 0)
		testutil.Equal(t, wantGlyph, theme.IconNeedsInput)

		nodes, _ := heraPlanNodesWithBridge(m.OrchByID(1), m.bridgeIndex())
		n, ok := findNode(nodes, planNodeID(w))
		testutil.Equal(t, ok, true)
		testutil.Equal(t, n.Icon != nil, true)
		testutil.Equal(t, n.Icon.Animated, false)
		testutil.Equal(t, n.Icon.Style, wantStyle)
		testutil.Equal(t, effectiveGlyph(n.Icon, 0), wantGlyph)
	})
}

// TestPlanNodeIcon_WorkingIsAnimated: the genuinely-active "working" node is
// flagged Animated so the plan view renders the live spinner frame (1:1 with the
// rail's animated row), not a frozen glyph.
func TestPlanNodeIcon_WorkingIsAnimated(t *testing.T) {
	icon := projectWorkerIcon(t, RoleView{RoleID: 2, Name: "w", Live: true, TaskID: "t1", BridgeTaskID: "t1", TaskStatus: model.StatusInProgress.String()})
	testutil.Equal(t, icon != nil, true)
	testutil.Equal(t, icon.Animated, true)
}

// TestPlanNodeIcon_PlannedAndFailedUseStateOverlay: the two plan-view-specific
// states the rail has no concept of leave Icon nil → the widget renders the State
// overlay (planned ○ / failed ✕).
func TestPlanNodeIcon_PlannedAndFailedUseStateOverlay(t *testing.T) {
	planned := projectWorkerIcon(t, RoleView{RoleID: 2, Name: "w-planned", Planned: true})
	testutil.Nil(t, planned)

	failed := projectWorkerIcon(t, RoleView{RoleID: 2, Name: "w-failed", Live: true, TaskID: "t1", BridgeTaskID: "t1", TaskStatus: model.StatusInReview.String(), TaskResult: `{"failed":true}`})
	testutil.Nil(t, failed)
}

// --- Refresh preserves plan cursor + fanned state (BUG-1/2 page-level) ---

// TestRefresh_PreservesPlanCursorAndFanned is the page-level regression for the
// dogfood bug: applySelection runs on every ~1s refresh tick and used to call
// SetData unconditionally, resetting the operator's plan-view cursor to
// stage0/slot0 and collapsing any fanned group ~1s after they moved. The fix
// routes a same-orchestrator re-projection through UpdateData. Here we select a
// coordinator (details mode), drive the plan cursor down and fan out a parallel
// group, then fire two more refresh cycles and assert the cursor + fanned state
// survive.
func TestRefresh_PreservesPlanCursorAndFanned(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	// A two-stage plan: 1a feeds three parallel stage-2 workers (2a/2b/2c) that
	// share the same blocker, so they collapse into one group at stage 1.
	a := seedPlannedRole(t, d, orch, "1a-root")
	b := seedPlannedRole(t, d, orch, "2a-x")
	c := seedPlannedRole(t, d, orch, "2b-y")
	e := seedPlannedRole(t, d, orch, "2c-z")
	for _, blocked := range []*db.HeraRole{b, c, e} {
		testutil.NoError(t, d.AddHeraBlock(blocked.ID, a.ID))
	}

	coordSess := &fakeSession{id: "t-coord", alive: true}
	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{"t-coord": coordSess}))
	p.Refresh()

	testutil.Equal(t, selectOrchByName(p, "orch"), true)
	testutil.Equal(t, p.detailsMode, true)

	pl := p.Plan()
	// Stage 1 is the parallel group [2a–2c]. Move the cursor there and fan it out,
	// landing on the first member.
	pl.MoveStage(1)
	_, isGroup := pl.GroupAt(pl.CursorPos().Stage, pl.CursorPos().Slot)
	testutil.Equal(t, isGroup, true)
	pl.ActivateCursor()      // fan out the group
	pl.MoveSlot(1)           // walk to the second member
	before := pl.CursorPos() // {Stage:1, Slot:0, Member:1}
	testutil.Equal(t, pl.Fanned(before.Stage, before.Slot), true)
	testutil.Equal(t, before.Member, 1)

	// Two more refresh ticks on the SAME coordinator (no structural change).
	p.Refresh()
	p.Refresh()

	testutil.DeepEqual(t, pl.CursorPos(), before)
	testutil.Equal(t, pl.Fanned(before.Stage, before.Slot), true)
}

// TestRefresh_DifferentCoordinatorResetsPlanCursor: switching to a DIFFERENT
// coordinator is a genuine selection change, so the plan cursor resets (SetData,
// not UpdateData) — the preservation is scoped to same-orchestrator refreshes.
func TestRefresh_DifferentCoordinatorResetsPlanCursor(t *testing.T) {
	d := memDB(t)
	orchA := seedOrch(t, d, "orch-a")
	orchB := seedOrch(t, d, "orch-b")
	seedBoundRole(t, d, orchA, "a-coord", db.HeraKindCoordinator, "t-a-coord")
	seedBoundRole(t, d, orchB, "b-coord", db.HeraKindCoordinator, "t-b-coord")
	// Each orch gets a two-stage plan so the cursor can leave stage 0.
	for _, orch := range []int64{orchA, orchB} {
		root := seedPlannedRole(t, d, orch, "1a-root")
		leaf := seedPlannedRole(t, d, orch, "2a-leaf")
		testutil.NoError(t, d.AddHeraBlock(leaf.ID, root.ID))
	}

	p := NewHeraPage(d)
	p.SetSessionResolver(resolverFor(map[string]*fakeSession{
		"t-a-coord": {id: "t-a-coord", alive: true},
		"t-b-coord": {id: "t-b-coord", alive: true},
	}))
	p.Refresh()

	testutil.Equal(t, selectOrchByName(p, "orch-a"), true)
	pl := p.Plan()
	pl.MoveStage(1)
	testutil.Equal(t, pl.CursorPos().Stage, 1)

	// Switch coordinators: the plan cursor must reset to stage 0.
	testutil.Equal(t, selectOrchByName(p, "orch-b"), true)
	testutil.Equal(t, pl.CursorPos().Stage, 0)
}

// --- Cancelled planned node rendering (make-hera-plan-living B3) ---

// TestRoleViewCancelled_SetFromCancelledAt: a role whose CancelledAt is set
// projects Cancelled=true in the RoleView.
func TestRoleViewCancelled_SetFromCancelledAt(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	r := seedPlannedRole(t, d, orch, "1a-to-cancel")
	testutil.NoError(t, d.CancelHeraPlannedNode(r.ID))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	found := false
	for _, rv := range ov.Roles {
		if rv.RoleID == r.ID {
			found = true
			testutil.Equal(t, rv.Cancelled, true)
		}
	}
	testutil.Equal(t, found, true)
}

// TestHeraPlanNodes_CancelledProjectsStateCancelled: a cancelled planned node
// (CancelledAt set) projects State=StateCancelled and remains in the node list
// (NOT omitted — it stays visible in the plan DAG as grey ✕).
func TestHeraPlanNodes_CancelledProjectsStateCancelled(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	cancelled := seedPlannedRole(t, d, orch, "1a-cancelled")
	active := seedPlannedRole(t, d, orch, "2a-active")
	testutil.NoError(t, d.CancelHeraPlannedNode(cancelled.ID))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)
	nodes, _ := heraPlanNodes(ov)

	// Both nodes appear (cancelled is visible, not dropped).
	nc, okC := findNode(nodes, fmt.Sprintf("plan:%d", cancelled.ID))
	na, okA := findNode(nodes, fmt.Sprintf("plan:%d", active.ID))
	testutil.Equal(t, okC, true)
	testutil.Equal(t, okA, true)

	// Cancelled node → StateCancelled; active planned node → StatePlanned.
	testutil.Equal(t, nc.State, planview.StateCancelled)
	testutil.Equal(t, na.State, planview.StatePlanned)
}

// TestHeraPlanNodes_CancelledWinsOverPlanned: a node that is both planned
// (never materialized) AND cancelled projects StateCancelled, not StatePlanned.
// This pins the priority order: Cancelled > Planned.
func TestHeraPlanNodes_CancelledWinsOverPlanned(t *testing.T) {
	d := memDB(t)
	orch := seedOrch(t, d, "orch")
	seedBoundRole(t, d, orch, "coord", db.HeraKindCoordinator, "t-coord")
	r := seedPlannedRole(t, d, orch, "1a-cancel-planned")
	testutil.NoError(t, d.CancelHeraPlannedNode(r.ID))

	ov := orchViewByName(t, d, "orch")
	testutil.Equal(t, ov != nil, true)

	// Confirm the RoleView carries Planned=true AND Cancelled=true (double flag).
	for _, rv := range ov.Roles {
		if rv.RoleID == r.ID {
			testutil.Equal(t, rv.Planned, true)
			testutil.Equal(t, rv.Cancelled, true)
		}
	}

	nodes, _ := heraPlanNodes(ov)
	n, ok := findNode(nodes, fmt.Sprintf("plan:%d", r.ID))
	testutil.Equal(t, ok, true)
	// Cancelled wins — renders StateCancelled, NOT StatePlanned.
	testutil.Equal(t, n.State, planview.StateCancelled)
}

// TestPlanNodeIcon_CancelledUsesStateOverlay: a cancelled node leaves Icon nil
// so the widget renders the State overlay (grey ✕) rather than an Icon glyph.
func TestPlanNodeIcon_CancelledUsesStateOverlay(t *testing.T) {
	icon := projectWorkerIcon(t, RoleView{RoleID: 2, Name: "w-cancelled", Cancelled: true})
	testutil.Nil(t, icon)
}
