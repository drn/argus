package planview

import (
	"testing"

	"github.com/drn/argus/internal/testutil"
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
	// stage0 has two independent roots → two slots.
	w := New()
	w.SetData([]Node{node("0a"), node("0b")}, nil)
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
