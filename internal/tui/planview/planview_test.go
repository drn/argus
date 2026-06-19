package planview

import (
	"strings"
	"testing"

	"github.com/drn/argus/internal/testutil"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// node is a fixture builder for a planned plan node named by its short-id.
func node(name string) Node {
	return Node{ID: name, Name: name + "-role", State: StatePlanned, Planned: true}
}

// liveNode is a fixture builder for a live node with a bound-task state.
func liveNode(name string, st State) Node {
	return Node{ID: name, Name: name + "-role", State: st}
}

// --- Short-id parse + fallback (Requirement: Short-id node labels) ---

func TestParseShortID(t *testing.T) {
	tests := []struct {
		name      string
		in        string
		wantStage int
		wantMem   string
		wantLabel string
		wantOK    bool
	}{
		{"digit+letter", "2c-fact-checker", 2, "c", "2c", true},
		{"single digit single letter", "1a-research", 1, "a", "1a", true},
		{"multi-letter member", "10ab-thing", 10, "ab", "10ab", true},
		{"no prefix falls back to name", "researcher", 0, "", "researcher", false},
		{"no dash but parseable", "3b", 3, "b", "3b", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseShortID(tt.in)
			testutil.Equal(t, got.OK, tt.wantOK)
			testutil.Equal(t, got.Label, tt.wantLabel)
			if tt.wantOK {
				testutil.Equal(t, got.Stage, tt.wantStage)
				testutil.Equal(t, got.Member, tt.wantMem)
			}
		})
	}
}

// TestLabelOf_ShortIDFromName mirrors "Short-id parsed from the name prefix":
// a role named 2c-fact-checker labels its node 2c.
func TestLabelOf_ShortIDFromName(t *testing.T) {
	w := New()
	w.SetData([]Node{{ID: "n1", Name: "2c-fact-checker", State: StatePlanned, Planned: true}}, nil)
	testutil.Equal(t, w.LabelOf("n1"), "2c")
}

// TestLabelOf_FallsBackToTruncatedName mirrors "Unparseable name falls back to
// the truncated name".
func TestLabelOf_FallsBackToTruncatedName(t *testing.T) {
	w := New()
	w.SetData([]Node{{ID: "n1", Name: "researcher", State: StatePlanned, Planned: true}}, nil)
	got := w.LabelOf("n1")
	// Falls back to the (truncated) name, NOT empty and NOT a short-id.
	testutil.Equal(t, len(got) > 0, true)
	testutil.Contains(t, "researcher", got) // got is a prefix/substring of the name
}

// --- Stage = computed longest-path (Requirement: plan projects ... stage placement) ---

// TestStage_ComputedFromEdgesNotShortID mirrors "Stage is computed from edges,
// not the short-id number": a node whose short-id number says stage 1 but which
// is blocked by a stage-2 node lands at the computed longest-path layer.
func TestStage_ComputedFromEdgesNotShortID(t *testing.T) {
	w := New()
	// Names claim 1x stages, but the edges force a chain a→b→c (3 layers).
	// "1a" blocks "1b" blocks "1c" — short-id number is 1 for all three, but the
	// computed stages must be 0,1,2.
	w.SetData(
		[]Node{node("1a"), node("1b"), node("1c")},
		[]Edge{{From: "1a", To: "1b"}, {From: "1b", To: "1c"}},
	)
	sa, _ := w.StageOf("1a")
	sb, _ := w.StageOf("1b")
	sc, _ := w.StageOf("1c")
	testutil.Equal(t, sa, 0)
	testutil.Equal(t, sb, 1)
	testutil.Equal(t, sc, 2)
	testutil.Equal(t, w.Stages(), 3)
	// The label still reflects the short-id, not the computed stage.
	testutil.Equal(t, w.LabelOf("1c"), "1c")
}

// --- Parallel-group collapse (Requirement: Parallel groups auto-collapse) ---

// fanGroup builds a plan with one blocker (stage 0) feeding `members` siblings
// (stage 1) that share the blocker set and have no internal edges.
func fanGroup(members ...string) *Widget {
	w := New()
	nodes := []Node{node("0a")}
	var edges []Edge
	for _, m := range members {
		nodes = append(nodes, node(m))
		edges = append(edges, Edge{From: "0a", To: m})
	}
	w.SetData(nodes, edges)
	return w
}

// TestGroup_ContiguousCollapsesToRangeBox mirrors "Same-stage siblings collapse
// into a range box": 2a,2b,2c → [2a–2c].
func TestGroup_ContiguousCollapsesToRangeBox(t *testing.T) {
	w := fanGroup("2a", "2b", "2c")
	// Stage 1 has exactly one slot: the collapsed group.
	testutil.Equal(t, w.SlotCount(1), 1)
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.Label, "[2a–2c]")
	testutil.Equal(t, len(g.Members), 3)
}

// TestGroup_NonContiguousShowsSpanAndCount mirrors "Non-contiguous group shows
// the span and a count": 2a,2b,2f → [2a–2f +1].
func TestGroup_NonContiguousShowsSpanAndCount(t *testing.T) {
	w := fanGroup("2a", "2b", "2f")
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.Label, "[2a–2f +1]")
}

// TestGroup_AggregateStateCounts mirrors "Aggregate state counts on a collapsed
// group box": a group with mixed states reports per-state counts.
func TestGroup_AggregateStateCounts(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{
			node("0a"),
			liveNode("1a", StateDone),
			liveNode("1b", StateDone),
			liveNode("1c", StateWorking),
			node("1d"), // planned
		},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"},
			{From: "0a", To: "1c"}, {From: "0a", To: "1d"},
		},
	)
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.Counts[StateDone], 2)
	testutil.Equal(t, g.Counts[StateWorking], 1)
	testutil.Equal(t, g.Counts[StatePlanned], 1)
}

// TestGroup_SingleNodeIsNotAGroup: a stage with one node renders as an
// individual chip, not a group.
func TestGroup_SingleNodeIsNotAGroup(t *testing.T) {
	w := fanGroup("2a")
	_, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, false)
	testutil.Equal(t, w.SlotCount(1), 1) // one lone-node slot
}

// --- Partial dependency, option B (Requirement: Partial-dependency marker) ---

// TestPartialDep_MarksGroupAndFeedingMember mirrors "Partially-feeding group is
// marked": only 2b of [2a–2c] blocks downstream 3a → box shows [2a–2c ↘] and
// the feeding member is 2b.
func TestPartialDep_MarksGroupAndFeedingMember(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{node("0a"), node("2a"), node("2b"), node("2c"), node("3a")},
		[]Edge{
			{From: "0a", To: "2a"}, {From: "0a", To: "2b"}, {From: "0a", To: "2c"},
			// Only 2b feeds 3a.
			{From: "2b", To: "3a"},
		},
	)
	g, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, g.PartialFeed, true)
	testutil.Equal(t, g.FeedingMember, "2b")
	testutil.Contains(t, g.Label, "↘")
}

// --- Degenerate no-plan (Requirement: no plan authored renders live roles flat) ---

func TestNoPlan_FlatSingleStage(t *testing.T) {
	w := New()
	w.SetData([]Node{liveNode("w1", StateWorking), liveNode("w2", StateWorking)}, nil)
	testutil.Equal(t, w.NoPlan(), true)
	testutil.Equal(t, w.Stages(), 1)
	testutil.Equal(t, w.SlotCount(0), 2) // both live workers flat, no edges
}

// --- Navigation (Requirement: Four-way plan navigation with group fan-out) ---

// navGraph builds: stage0 [0a], stage1 group [1a–1c], stage2 [2a].
func navGraph() *Widget {
	w := New()
	w.SetData(
		[]Node{node("0a"), node("1a"), node("1b"), node("1c"), node("2a")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			// 2a is blocked by the whole group so it sits at stage 2.
			{From: "1a", To: "2a"}, {From: "1b", To: "2a"}, {From: "1c", To: "2a"},
		},
	)
	w.SetFocused(true)
	return w
}

// TestNav_UpDownChangesStageAndCollapses mirrors "Up/down changes stage and
// collapses": fanning a group then pressing ↓ moves to the next stage and
// collapses the group.
func TestNav_UpDownChangesStageAndCollapses(t *testing.T) {
	w := navGraph()
	// Move to stage 1 (the group) and fan it out.
	w.MoveStage(1)
	testutil.Equal(t, w.CursorPos().Stage, 1)
	w.ActivateCursor() // fan out
	testutil.Equal(t, w.Fanned(1, 0), true)

	// ↓ moves to stage 2 AND collapses the fanned group.
	w.MoveStage(1)
	testutil.Equal(t, w.CursorPos().Stage, 2)
	testutil.Equal(t, w.Fanned(1, 0), false)
}

// TestNav_LeftRightMovesSlot: within a multi-slot stage ←/→ moves between slots.
func TestNav_LeftRightMovesSlot(t *testing.T) {
	// Two same-stage roots that share the EMPTY blocker set collapse into a
	// single group per D4 — that is spec-compliant, so we can't use two planned
	// roots here. The no-plan path (live roles, no planned nodes, no edges)
	// renders each live worker as its own lone-node slot, never grouped, giving a
	// genuine two-slot stage 0 that preserves this test's intent (←/→ between
	// slots).
	w := New()
	w.SetData([]Node{liveNode("w1", StateWorking), liveNode("w2", StateWorking)}, nil)
	w.SetFocused(true)
	testutil.Equal(t, w.CursorPos().Slot, 0)
	w.MoveSlot(1)
	testutil.Equal(t, w.CursorPos().Slot, 1)
	w.MoveSlot(-1)
	testutil.Equal(t, w.CursorPos().Slot, 0)
	// Clamp: stepping off the left edge is a no-op.
	w.MoveSlot(-1)
	testutil.Equal(t, w.CursorPos().Slot, 0)
}

// TestNav_EnterFansOutGroupLandsOnFirstMember mirrors "Enter fans out a group".
func TestNav_EnterFansOutGroupLandsOnFirstMember(t *testing.T) {
	w := navGraph()
	w.MoveStage(1) // cursor on the group slot
	testutil.Equal(t, w.Fanned(1, 0), false)
	w.ActivateCursor()
	testutil.Equal(t, w.Fanned(1, 0), true)
	// Cursor lands on the first member (member index 0 → node "1a").
	testutil.Equal(t, w.CursorPos().Member, 0)
	testutil.Equal(t, w.CurrentNodeID(), "1a")
}

// TestNav_SpaceCollapsesFannedGroup: Space on a fanned group collapses it.
func TestNav_SpaceCollapsesFannedGroup(t *testing.T) {
	w := navGraph()
	w.MoveStage(1)
	w.ActivateCursor() // fan out
	testutil.Equal(t, w.Fanned(1, 0), true)
	w.ActivateCursor() // collapse (Enter/Space toggle on a group)
	testutil.Equal(t, w.Fanned(1, 0), false)
}

// TestNav_MemberWalkInsideGroup mirrors "walk members on ←/→ inside a fanned-out
// group".
func TestNav_MemberWalkInsideGroup(t *testing.T) {
	w := navGraph()
	w.MoveStage(1)
	w.ActivateCursor() // fan out, member 0 = 1a
	w.MoveSlot(1)
	testutil.Equal(t, w.CurrentNodeID(), "1b")
	w.MoveSlot(1)
	testutil.Equal(t, w.CurrentNodeID(), "1c")
}

// TestNav_StepOffGroupEdgeExitsAndCollapses mirrors "Stepping off a group edge
// exits and collapses": from the last member, → collapses the group and moves
// to the next slot (or clamps at the stage edge).
func TestNav_StepOffGroupEdgeExitsAndCollapses(t *testing.T) {
	w := navGraph()
	w.MoveStage(1)
	w.ActivateCursor() // fan out
	w.MoveSlot(1)      // 1b
	w.MoveSlot(1)      // 1c (last member)
	testutil.Equal(t, w.CurrentNodeID(), "1c")
	// Step off the right edge: group collapses, cursor exits the group.
	w.MoveSlot(1)
	testutil.Equal(t, w.Fanned(1, 0), false)
	testutil.Equal(t, w.CursorPos().Member, -1) // no longer inside a group
}

// TestNav_StepOffGroupLeftEdgeExitsAndCollapses: from the first member, ←
// collapses the group and clamps at the stage's left edge (no slot to the left).
func TestNav_StepOffGroupLeftEdgeExitsAndCollapses(t *testing.T) {
	w := navGraph()
	w.MoveStage(1)
	w.ActivateCursor() // fan out, member 0 = 1a (first member)
	testutil.Equal(t, w.CurrentNodeID(), "1a")
	w.MoveSlot(-1) // step off the left edge
	testutil.Equal(t, w.Fanned(1, 0), false)
	testutil.Equal(t, w.CursorPos().Member, -1)
	testutil.Equal(t, w.CursorPos().Slot, 0) // clamped: stage 1 has one slot
}

// TestNav_MoveStageClampsAtEdges: ↑ at stage 0 and ↓ past the last stage clamp.
func TestNav_MoveStageClampsAtEdges(t *testing.T) {
	w := navGraph() // stages 0,1,2
	w.MoveStage(-1) // already at 0
	testutil.Equal(t, w.CursorPos().Stage, 0)
	w.MoveStage(5) // past the last stage (2)
	testutil.Equal(t, w.CursorPos().Stage, 2)
}

// TestNav_EmptyWidgetNavIsNoop: navigation on an empty widget never panics and
// leaves the cursor at the origin.
func TestNav_EmptyWidgetNavIsNoop(t *testing.T) {
	w := New()
	w.MoveStage(1)
	w.MoveSlot(1)
	w.ActivateCursor()
	testutil.Equal(t, w.CursorPos().Stage, 0)
	testutil.Equal(t, w.CursorPos().Slot, 0)
	testutil.Equal(t, w.CurrentNodeID(), "")
}

// TestNav_CollapsedGroupCursorHasNoNode: the cursor on a collapsed group names
// no node (a group is not itself a node).
func TestNav_CollapsedGroupCursorHasNoNode(t *testing.T) {
	w := navGraph()
	w.MoveStage(1) // on the collapsed group at stage 1
	_, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	testutil.Equal(t, w.CurrentNodeID(), "") // collapsed group → no node
}

// TestInputHandler_RoutesKeys exercises the InputHandler key routing: arrows and
// the j/k/h/l/Space/Enter aliases drive the same cursor moves as the methods.
func TestInputHandler_RoutesKeys(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	t.Run("arrows move stage and slot", func(t *testing.T) {
		w := navGraph()
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.CursorPos().Stage, 1)
		h(tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.CursorPos().Stage, 0)
	})
	t.Run("vim aliases move", func(t *testing.T) {
		w := navGraph()
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus)
		testutil.Equal(t, w.CursorPos().Stage, 1)
		h(tcell.NewEventKey(tcell.KeyRune, 'k', tcell.ModNone), noFocus)
		testutil.Equal(t, w.CursorPos().Stage, 0)
	})
	t.Run("Enter fans out a group", func(t *testing.T) {
		w := navGraph()
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone), noFocus) // to the group
		h(tcell.NewEventKey(tcell.KeyEnter, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.Fanned(1, 0), true)
	})
	t.Run("Space toggles a group and h/l walk members", func(t *testing.T) {
		w := navGraph()
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyRune, 'j', tcell.ModNone), noFocus) // to the group
		h(tcell.NewEventKey(tcell.KeyRune, ' ', tcell.ModNone), noFocus) // fan out
		testutil.Equal(t, w.Fanned(1, 0), true)
		h(tcell.NewEventKey(tcell.KeyRune, 'l', tcell.ModNone), noFocus) // member 1b
		testutil.Equal(t, w.CurrentNodeID(), "1b")
		h(tcell.NewEventKey(tcell.KeyRune, 'h', tcell.ModNone), noFocus) // back to 1a
		testutil.Equal(t, w.CurrentNodeID(), "1a")
	})
}

// --- Master-detail header (Requirement: Plan master-detail header) ---

// TestHeader_NodeView mirrors "Header shows the selected node": for a node the
// header shows its name, description, and feeds.
func TestHeader_NodeView(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{
			{ID: "1a", Name: "1a-research", State: StatePlanned, Planned: true, Description: "research the topic"},
			{ID: "2a", Name: "2a-write", State: StatePlanned, Planned: true, Description: "write it up"},
		},
		[]Edge{{From: "1a", To: "2a"}},
	)
	w.SetFocused(true)
	// Cursor starts at stage 0 slot 0 (node 1a).
	testutil.Equal(t, w.CurrentNodeID(), "1a")
	lines := w.HeaderLines()
	joined := joinLines(lines)
	testutil.Contains(t, joined, "1a-research")        // name
	testutil.Contains(t, joined, "research the topic") // description (prompt first line)
	testutil.Contains(t, joined, "2a")                 // feeds → 2a
}

// TestHeader_GroupView mirrors "Header shows the selected group": for a
// collapsed group the header shows the range/title, members, and downstream.
func TestHeader_GroupView(t *testing.T) {
	w := New()
	w.SetData(
		[]Node{node("0a"), node("1a"), node("1b"), node("1c"), node("2a")},
		[]Edge{
			{From: "0a", To: "1a"}, {From: "0a", To: "1b"}, {From: "0a", To: "1c"},
			{From: "1a", To: "2a"}, {From: "1b", To: "2a"}, {From: "1c", To: "2a"},
		},
	)
	w.SetFocused(true)
	w.MoveStage(1) // cursor on the collapsed group at stage 1
	_, ok := w.GroupAt(1, 0)
	testutil.Equal(t, ok, true)
	lines := w.HeaderLines()
	joined := joinLines(lines)
	testutil.Contains(t, joined, "[1a–1c]") // group range
	testutil.Contains(t, joined, "2a")      // downstream target
}

// TestHeader_FixedHeightBudget mirrors "header height is budgeted exactly":
// HeaderHeight is a fixed positive value and HeaderLines never exceeds it.
func TestHeader_FixedHeightBudget(t *testing.T) {
	w := New()
	w.SetData([]Node{node("1a")}, nil)
	w.SetFocused(true)
	h := w.HeaderHeight()
	testutil.Equal(t, h > 0, true)
	testutil.Equal(t, len(w.HeaderLines()) <= h, true)
}

// --- Sub-coordinator drill-in (Requirement: Sub-coordinator drill-in) ---

// TestDrillIn_EnterPushesChildPlan mirrors "Enter drills into a sub-coordinator":
// Enter on a drillable node pushes a child plan and increments the nav depth.
func TestDrillIn_EnterPushesChildPlan(t *testing.T) {
	w := New()
	var drilled string
	w.OnDrillIn = func(id string) {
		drilled = id
		// The page wires OnDrillIn to project + push the child plan.
		w.PushOrch("child", []Node{node("c1"), node("c2")}, []Edge{{From: "c1", To: "c2"}})
	}
	w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}}, nil)
	w.SetFocused(true)
	testutil.Equal(t, w.CurrentNodeID(), "sub")
	w.ActivateCursor() // drill in
	testutil.Equal(t, drilled, "sub")
	testutil.Equal(t, w.DrillDepth(), 1)
	// The child plan is now displayed: c2 is blocked by c1 → 2 stages.
	testutil.Equal(t, w.Stages(), 2)
}

// TestDrillIn_EscPopsToParent mirrors "Esc pops back to the parent": after
// drilling in, PopOrch returns to the parent plan.
func TestDrillIn_EscPopsToParent(t *testing.T) {
	w := New()
	w.OnDrillIn = func(string) {
		w.PushOrch("child", []Node{node("c1")}, nil)
	}
	w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}, node("2a")}, []Edge{{From: "sub", To: "2a"}})
	w.SetFocused(true)
	w.ActivateCursor() // drill into "sub"
	testutil.Equal(t, w.DrillDepth(), 1)
	w.PopOrch()
	testutil.Equal(t, w.DrillDepth(), 0)
	// Back at the parent: the parent's nodes are visible again.
	_, ok := w.StageOf("sub")
	testutil.Equal(t, ok, true)
}

// TestDrillIn_PlainLeafEnterFiresOnEnter mirrors "Enter on a plain leaf node
// opens its agent view (unchanged behavior)": a non-drillable, non-group node
// fires OnEnter, NOT OnDrillIn.
func TestDrillIn_PlainLeafEnterFiresOnEnter(t *testing.T) {
	w := New()
	var entered, drilled string
	w.OnEnter = func(id string) { entered = id }
	w.OnDrillIn = func(id string) { drilled = id }
	w.SetData([]Node{liveNode("1a", StateWorking)}, nil)
	w.SetFocused(true)
	testutil.Equal(t, w.CurrentNodeID(), "1a")
	w.ActivateCursor()
	testutil.Equal(t, entered, "1a")
	testutil.Equal(t, drilled, "") // NOT a drill-in
}

// TestDrillIn_DrillableMarkerPresent mirrors "A sub-coordinator node SHALL carry
// a visible drillable marker": the drillable node's chip label carries a marker.
func TestDrillIn_DrillableMarkerPresent(t *testing.T) {
	w := New()
	w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}}, nil)
	label := w.LabelOf("sub")
	// The label (or its rendered chip) carries a drillable affordance (▸ / ⊕).
	hasMarker := containsAny(label, "▸", "⊕")
	testutil.Equal(t, hasMarker, true)
}

// TestInputHandler_EscRouting pins the Esc routing contract the page relies on:
// Esc at DrillDepth>0 pops the nav stack and fires OnDrillOut; at the root it is
// a no-op so the page-level routing can escape the pane.
func TestInputHandler_EscRouting(t *testing.T) {
	noFocus := func(tview.Primitive) {}
	t.Run("Esc at root no-ops and does not fire OnDrillOut", func(t *testing.T) {
		w := New()
		var popped bool
		w.OnDrillOut = func() { popped = true }
		w.SetData([]Node{liveNode("1a", StateWorking)}, nil)
		w.SetFocused(true)
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.DrillDepth(), 0)
		testutil.Equal(t, popped, false)
	})
	t.Run("Esc at drill-depth>0 pops and fires OnDrillOut", func(t *testing.T) {
		w := New()
		var popped int
		w.OnDrillOut = func() { popped++ }
		w.OnDrillIn = func(string) { w.PushOrch("child", []Node{node("c1")}, nil) }
		w.SetData([]Node{{ID: "sub", Name: "1a-subcoord", State: StateWorking, Drillable: true}}, nil)
		w.SetFocused(true)
		w.ActivateCursor() // drill in → depth 1
		testutil.Equal(t, w.DrillDepth(), 1)
		h := w.InputHandler()
		h(tcell.NewEventKey(tcell.KeyEscape, 0, tcell.ModNone), noFocus)
		testutil.Equal(t, w.DrillDepth(), 0)
		testutil.Equal(t, popped, 1)
	})
}

// TestDrillIn_MultiLevelPushPop exercises a depth≥2 drill stack: pushing two
// child orchestrators then popping back through both restores each parent in
// LIFO order.
func TestDrillIn_MultiLevelPushPop(t *testing.T) {
	w := New()
	w.SetData([]Node{node("root1"), node("root2")}, []Edge{{From: "root1", To: "root2"}})
	w.SetFocused(true)
	testutil.Equal(t, w.DrillDepth(), 0)

	// Drill two levels deep.
	w.PushOrch("L1", []Node{node("l1a"), node("l1b")}, []Edge{{From: "l1a", To: "l1b"}})
	testutil.Equal(t, w.DrillDepth(), 1)
	testutil.Equal(t, w.Title(), "L1")
	w.PushOrch("L2", []Node{node("l2a")}, nil)
	testutil.Equal(t, w.DrillDepth(), 2)
	testutil.Equal(t, w.Title(), "L2")
	_, ok := w.StageOf("l2a")
	testutil.Equal(t, ok, true)

	// Pop back through both levels (LIFO): L2 → L1 → root.
	w.PopOrch()
	testutil.Equal(t, w.DrillDepth(), 1)
	testutil.Equal(t, w.Title(), "L1")
	_, ok = w.StageOf("l1a")
	testutil.Equal(t, ok, true)
	w.PopOrch()
	testutil.Equal(t, w.DrillDepth(), 0)
	_, ok = w.StageOf("root1")
	testutil.Equal(t, ok, true)
	// Past the root, PopOrch is a no-op.
	w.PopOrch()
	testutil.Equal(t, w.DrillDepth(), 0)
}

// TestHeader_EmptyDescriptionFallback: a node with no Description renders the
// "(no description)" placeholder in the header.
func TestHeader_EmptyDescriptionFallback(t *testing.T) {
	w := New()
	w.SetData([]Node{{ID: "1a", Name: "1a-research", State: StatePlanned, Planned: true}}, nil)
	w.SetFocused(true)
	testutil.Equal(t, w.CurrentNodeID(), "1a")
	joined := joinLines(w.HeaderLines())
	testutil.Contains(t, joined, "(no description)")
}

// TestHeader_LongNameStaysWithinBudget: a node with a very long name still
// yields exactly headerContentRows lines (the fixed-height budget never grows).
func TestHeader_LongNameStaysWithinBudget(t *testing.T) {
	w := New()
	long := "1a-" + strings.Repeat("verylongsegment-", 12)
	w.SetData([]Node{{ID: "1a", Name: long, State: StatePlanned, Planned: true, Description: "d"}}, nil)
	w.SetFocused(true)
	lines := w.HeaderLines()
	testutil.Equal(t, len(lines), headerContentRows)
	testutil.Equal(t, len(lines) <= w.HeaderHeight(), true)
	// The full (untruncated) name is the header's first line — the strip clips it
	// at Draw, but HeaderLines reports the content faithfully.
	testutil.Equal(t, lines[0], long)
}

func joinLines(lines []string) string {
	out := ""
	for _, l := range lines {
		out += l + "\n"
	}
	return out
}

func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
	}
	return false
}
